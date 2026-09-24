package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func objectMetaEdit(t *testing.T, h *harness) ObjectMetadataEditDTO {
	t.Helper()
	before, err := h.inspector.Object(context.Background(), h.bookmarkID(), "fixtures", "normal/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	edit := ObjectMetadataEditDTO{Meta: map[string]string{}}
	for k, v := range before.Meta {
		edit.Meta[k] = v
	}
	edit.Meta["Camera"] = "sony-fx6"
	return edit
}

func refuseSQL(t *testing.T, h *harness, query string) {
	t.Helper()
	if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(query)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func factsOn(t *testing.T, h *harness) *mutationfacts.Store {
	t.Helper()
	return mutationfacts.NewStore(h.db)
}

func reopenFacts(t *testing.T, h *harness) *mutationfacts.Store {
	t.Helper()
	d, err := db.Open(context.Background(), h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return mutationfacts.NewStore(d)
}

func newObjectPOSTs(before, after []string) int {
	n := 0
	for _, line := range after[len(before):] {
		if strings.HasPrefix(line, "POST ") && strings.Contains(line, "hello.txt") {
			n++
		}
	}
	return n
}

func TestObjectMetadataIntentAbortIssuesZeroPOST(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	refuseSQL(t, h, `CREATE TRIGGER refuse_intent BEFORE INSERT ON mutation_facts
		WHEN NEW.outcome='intent'
		BEGIN SELECT RAISE(ABORT,'fixture required intent refusal'); END`)
	before := h.mock.RequestLog()
	_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
		objectMetaEdit(t, h))
	if err == nil {
		t.Fatal("intent abort must fail the call")
	}
	if newObjectPOSTs(before, h.mock.RequestLog()) != 0 {
		t.Fatalf("intent abort issued POST: %v", h.mock.RequestLog()[len(before):])
	}
	all, err := factsOn(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("facts after intent abort = %+v", all)
	}
}

func TestObjectMetadataOutcomeAbortAfterPOSTIsResidue(t *testing.T) {
	for _, tc := range []struct {
		name, query string
	}{
		{"facts", `CREATE TRIGGER refuse_outcome BEFORE UPDATE ON mutation_facts
			WHEN NEW.outcome!='intent'
			BEGIN SELECT RAISE(ABORT,'fixture required outcome refusal'); END`},
		{"audit", `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events
			BEGIN SELECT RAISE(ABORT,'fixture required audit refusal'); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			refuseSQL(t, h, tc.query)
			before := h.mock.RequestLog()
			_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
				objectMetaEdit(t, h))
			if !errors.Is(err, errMetadataResidue) {
				t.Fatalf("err = %v, want residue", err)
			}
			if newObjectPOSTs(before, h.mock.RequestLog()) == 0 {
				t.Fatal("expected POST before outcome abort")
			}
			detail, err := h.inspector.Object(context.Background(), h.bookmarkID(), "fixtures", "normal/hello.txt")
			if err != nil {
				t.Fatal(err)
			}
			if detail.Meta["Camera"] != "sony-fx6" {
				t.Fatalf("remote snapshot = %v, want written metadata", detail.Meta)
			}
			all, err := reopenFacts(t, h).All(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 {
				t.Fatalf("reopened facts = %+v", all)
			}
			if all[0].Outcome == mutationfacts.OutcomeApplied {
				t.Fatalf("reopen identity applied after residue: %+v", all[0])
			}
			if all[0].Outcome != mutationfacts.OutcomeIntent {
				t.Fatalf("reopen outcome = %s, want intent", all[0].Outcome)
			}
		})
	}
}

func TestObjectMetadataOutcomesSurviveReopen(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	edit := objectMetaEdit(t, h)

	applied, err := h.mutate.UpdateObjectMetadata(ctx, h.portrait(), "fixtures", "normal/hello.txt", "", edit)
	if err != nil || applied.Outcome != "applied" {
		t.Fatalf("applied = %+v err=%v", applied, err)
	}

	conflict, err := h.mutate.UpdateObjectMetadata(ctx, h.portrait(), "fixtures", "normal/hello.txt", "not-the-fingerprint", edit)
	if err != nil || conflict.Outcome != "conflict" {
		t.Fatalf("conflict = %+v err=%v", conflict, err)
	}

	var mismatchOnce bool
	h.mock.AfterRequest = func(method, path string) {
		if method != "POST" || !strings.Contains(path, "hello.txt") || mismatchOnce {
			return
		}
		mismatchOnce = true
		h.mock.PatchObjectMeta("fixtures", "normal/hello.txt", "Stray", "1")
	}
	mismatch, err := h.mutate.UpdateObjectMetadata(ctx, h.portrait(), "fixtures", "normal/hello.txt", "", edit)
	if err != nil || mismatch.Outcome != "verify_mismatch" {
		t.Fatalf("mismatch = %+v err=%v", mismatch, err)
	}

	reopened := reopenFacts(t, h)
	all, err := reopened.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("facts = %d, want 3: %+v", len(all), all)
	}
	got := []mutationfacts.Outcome{all[0].Outcome, all[1].Outcome, all[2].Outcome}
	want := []mutationfacts.Outcome{
		mutationfacts.OutcomeApplied, mutationfacts.OutcomeConflict, mutationfacts.OutcomeVerifyMismatch,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reopen outcomes = %v, want %v", got, want)
		}
	}
}

func TestObjectMetadataVerifyReadFailureIsUnverified(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.AfterRequest = func(method, path string) {
		if method == "POST" && strings.Contains(path, "hello.txt") {
			h.mock.FailNextObjectHEADs(1)
		}
	}
	_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
		objectMetaEdit(t, h))
	if !errors.Is(err, swiftcontrol.ErrUnverifiedWrite) {
		t.Fatalf("err = %v, want unverified", err)
	}
	if errors.Is(err, errMetadataResidue) {
		t.Fatal("verify-read failure is unverified, not residue")
	}
	all, err := factsOn(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Outcome != mutationfacts.OutcomeUnverified {
		t.Fatalf("facts = %+v", all)
	}
	events, err := h.auditSvc.RecentFor(context.Background(), h.bookmarkID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Result != string(audit.ResultUnverified) {
		t.Fatalf("audit = %+v", events)
	}
	if events[0].Result == string(audit.ResultFailed) {
		t.Fatal("verify-read failure must not be failed")
	}
}

func TestObjectMetadataTransportFailureIsUnverified(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.DropNextObjectPOST()
	_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
		objectMetaEdit(t, h))
	if !errors.Is(err, swiftcontrol.ErrUnverifiedWrite) {
		t.Fatalf("err = %v, want unverified", err)
	}
	all, err := factsOn(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Outcome != mutationfacts.OutcomeUnverified {
		t.Fatalf("facts = %+v", all)
	}
	events, err := h.auditSvc.RecentFor(context.Background(), h.bookmarkID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Result != string(audit.ResultUnverified) {
		t.Fatalf("audit = %+v", events)
	}
}

func TestObjectMetadataStoreRefusalIsFailed(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.SetObjectPOSTStatus(http.StatusForbidden)
	_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
		objectMetaEdit(t, h))
	if err == nil || errors.Is(err, swiftcontrol.ErrUnverifiedWrite) {
		t.Fatalf("err = %v, want store refusal", err)
	}
	all, err := factsOn(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Outcome != mutationfacts.OutcomeFailed {
		t.Fatalf("facts = %+v", all)
	}
	detail, err := h.inspector.Object(context.Background(), h.bookmarkID(), "fixtures", "normal/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := detail.Meta["Camera"]; ok {
		t.Fatal("refused POST must not apply metadata")
	}
}

func TestObjectMetadataTransportFailureOutcomeAbortIsResidue(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.DropNextObjectPOST()
	refuseSQL(t, h, `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events
		BEGIN SELECT RAISE(ABORT,'fixture required audit refusal'); END`)
	_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
		objectMetaEdit(t, h))
	if !errors.Is(err, errMetadataResidue) {
		t.Fatalf("err = %v, want residue", err)
	}
	all, err := reopenFacts(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Outcome == mutationfacts.OutcomeApplied || all[0].Outcome == mutationfacts.OutcomeFailed {
		t.Fatalf("reopen = %+v", all)
	}
}

func TestObjectMetadataFactClassifiesNilUnverified(t *testing.T) {
	edit := ObjectMetadataEditDTO{Meta: map[string]string{"Camera": "1"}}
	outcome, result, _, _ := objectMetadataFact(nil, swiftcontrol.ErrUnverifiedWrite, edit)
	if outcome != mutationfacts.OutcomeUnverified || result != audit.ResultUnverified {
		t.Fatalf("nil+unverified = %s %s", outcome, result)
	}
	if !objectMetadataPosted(nil, swiftcontrol.ErrUnverifiedWrite) {
		t.Fatal("unverified error is posted for residue")
	}
	failed, failedRes, _, _ := objectMetadataFact(nil, errors.New("head failed"), edit)
	if failed != mutationfacts.OutcomeFailed || failedRes != audit.ResultFailed {
		t.Fatalf("nil+other = %s %s", failed, failedRes)
	}
}

func TestObjectMetadataFactsSurviveAuditRetention(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	if _, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
		objectMetaEdit(t, h)); err != nil {
		t.Fatal(err)
	}
	pruned, err := audit.NewStore(h.db).Prune(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned < 1 {
		t.Fatalf("pruned %d audit rows, want at least the metadata event", pruned)
	}
	all, err := factsOn(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Outcome != mutationfacts.OutcomeApplied {
		t.Fatalf("facts after audit prune = %+v", all)
	}
}
