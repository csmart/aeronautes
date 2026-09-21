package systemtest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestObjectMetadataAccountMock(t *testing.T) {
	objectMetadataAccountCases(t, findMockHarness)
}

func TestObjectMetadataAccountSwift(t *testing.T) {
	objectMetadataAccountCases(t, New)
}

func objectMetadataAccountCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, tc := range []struct {
		name          string
		switchAccount bool
		conflict      bool
		loseReply     bool
		refuseRecord  string
		outcome       mutationfacts.Outcome
	}{
		{name: "same_account_control", outcome: mutationfacts.OutcomeApplied},
		{name: "switch_after_post", switchAccount: true, outcome: mutationfacts.OutcomeApplied},
		{name: "switch_before_conflict", switchAccount: true, conflict: true, outcome: mutationfacts.OutcomeConflict},
		{name: "switch_before_lost_reply", switchAccount: true, loseReply: true, outcome: mutationfacts.OutcomeUnverified},
		{name: "switch_before_outcome_refusal", switchAccount: true, refuseRecord: "outcome", outcome: mutationfacts.OutcomeIntent},
		{name: "switch_before_audit_refusal", switchAccount: true, refuseRecord: "audit", outcome: mutationfacts.OutcomeIntent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeHarness(t)
			if h.Secondary == nil {
				t.Skip("two owned Swift accounts are required")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			container := h.Container(h.Primary, "object-account")
			// Identical resource names prevent a misrouted operation from
			// passing merely because the other account lacks the object.
			h.rawPut(h.Secondary, "/"+container, nil, nil)
			t.Cleanup(func() { h.purge(h.Secondary, container) })
			const object = "target.txt"
			for _, account := range []*Account{h.Primary, h.Secondary} {
				h.Seed(account, container, object, []byte("unchanged content"), map[string]string{
					"Content-Type": "text/plain", "X-Object-Meta-Value": "before",
				})
				h.Seed(account, container, "untouched.txt", []byte("collateral control"), nil)
			}
			detail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, container, object)
			if err != nil {
				t.Fatal(err)
			}
			beforePrimary := h.Snapshot(h.Primary, container)
			beforeSecondary := h.Snapshot(h.Secondary, container)
			storage, err := url.Parse(h.Primary.storageURL)
			if err != nil {
				t.Fatal(err)
			}
			method := http.MethodPost
			fingerprint := detail.EditableFingerprint
			if tc.conflict {
				method = http.MethodHead
				fingerprint = "stale-fingerprint"
			}
			barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
				Operation: t.Name(), Method: method,
				EscapedPath: storage.EscapedPath() + "/" + container + "/" + object, Occurrence: 1,
			}, tc.loseReply, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			metadataWindowProxy(t, h, h.Primary, barrier)
			type saved struct {
				result app.MutationResultDTO
				err    error
			}
			done := make(chan saved, 1)
			go func() {
				result, err := h.Mutate.UpdateObjectMetadata(ctx, container, object, fingerprint,
					app.ObjectMetadataEditDTO{Meta: map[string]string{"Value": "after"}, ContentType: "text/plain"})
				done <- saved{result, err}
			}()
			event, err := barrier.Wait(ctx)
			if err != nil || event.StatusCode/100 != 2 {
				t.Fatalf("selected response = %+v, err = %v", event, err)
			}
			intent, err := mutationfacts.NewStore(h.database).All(ctx)
			if err != nil || len(intent) != 1 {
				t.Fatalf("intent before release = %+v, err = %v", intent, err)
			}
			if intent[0].Outcome != mutationfacts.OutcomeIntent || intent[0].BookmarkID != h.Primary.BookmarkID {
				t.Fatalf("admitted attempt has the wrong intent: %+v", intent[0])
			}
			if tc.switchAccount {
				if _, err := h.Browse.Connect(ctx, h.Secondary.BookmarkID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.refuseRecord != "" {
				query := `CREATE TRIGGER refuse_object_outcome BEFORE UPDATE ON mutation_facts
					WHEN NEW.outcome!='intent'
					BEGIN SELECT RAISE(ABORT,'fixture outcome refusal'); END`
				if tc.refuseRecord == "audit" {
					query = `CREATE TRIGGER refuse_object_audit BEFORE INSERT ON audit_events
						WHEN NEW.operation='object.metadata.update'
						BEGIN SELECT RAISE(ABORT,'fixture audit refusal'); END`
				}
				if err := h.database.WriteTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, query)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			var got saved
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := barrier.Err(); err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.refuseRecord != "":
				if got.err == nil || !strings.Contains(got.err.Error(), "object metadata write residue") || got.result.Outcome != "" {
					t.Fatalf("required record refusal = %+v, err = %v", got.result, got.err)
				}
			case tc.loseReply:
				if !errors.Is(got.err, swiftcontrol.ErrUnverifiedWrite) || got.result.Outcome != "" {
					t.Fatalf("lost reply = %+v, err = %v", got.result, got.err)
				}
			default:
				if got.err != nil || got.result.Outcome != string(tc.outcome) {
					t.Fatalf("save = %+v, err = %v, want %s", got.result, got.err, tc.outcome)
				}
			}

			afterPrimary := h.Snapshot(h.Primary, container)
			afterSecondary := h.Snapshot(h.Secondary, container)
			h.AssertNoCollateral(beforeSecondary, afterSecondary)
			h.AssertNoCollateral(beforePrimary, afterPrimary, container+"/"+object)
			before := beforePrimary.Containers[container].Objects[object]
			after := afterPrimary.Containers[container].Objects[object]
			value := "after"
			if tc.conflict {
				value = "before"
			}
			if after.Meta["Value"] != value || after.Digest != before.Digest || after.Size != before.Size {
				t.Fatalf("independent primary state = %+v, want metadata %q with unchanged content", after, value)
			}

			reopened, err := db.Open(ctx, h.statePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reopened.Close(); err != nil {
					t.Error(err)
				}
			})
			facts, err := mutationfacts.NewStore(reopened).All(ctx)
			if err != nil || len(facts) != 1 {
				t.Fatalf("reopened facts = %+v, err = %v", facts, err)
			}
			fact := facts[0]
			if fact.Identity != intent[0].Identity || fact.BookmarkID != h.Primary.BookmarkID || fact.Operation != "object.metadata.update" || fact.Resource != container+"/"+object || fact.Outcome != tc.outcome {
				t.Errorf("reopened fact changed the attempt's identity or outcome: %+v", fact)
			}
			events, err := audit.NewStore(reopened).List(ctx, audit.Query{OperationPrefix: "object.metadata.update"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.refuseRecord != "" {
				if len(events) != 0 {
					t.Fatalf("failed outcome transaction left an audit event: %+v", events)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("raw audit = %+v", events)
			}
			if events[0].BookmarkID != h.Primary.BookmarkID || events[0].BookmarkID != fact.BookmarkID {
				t.Errorf("stored audit relabelled the admitted metadata write: fact=%+v audit=%+v", fact, events[0])
			}
			if events[0].Operation != fact.Operation || events[0].Resource != fact.Resource || string(events[0].Result) != string(fact.Outcome) || events[0].Summary != fact.Summary || !reflect.DeepEqual(events[0].TransIDs, fact.TransIDs) {
				t.Errorf("audit and durable outcome describe different attempts: fact=%+v audit=%+v", fact, events[0])
			}
		})
	}
}
