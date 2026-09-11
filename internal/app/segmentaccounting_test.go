package app

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestDLOAccountingErrorReportsManifestEffect(t *testing.T) {
	for _, fault := range []string{"control", "setup", "index", "index_and_audit"} {
		t.Run(fault, func(t *testing.T) {
			cache := deletionAccountingCache(t)
			h := newHarness(t)
			h.mock.AddContainer("manifests", "standard")
			h.mock.AddContainer("segments", "standard")
			for _, name := range []string{"scope/a", "scope/b"} {
				seedObject(h.mock, "segments", name, "owned segment", nil)
			}
			h.mock.PutObject("manifests", "selected", swifttest.Object{Manifest: "segments/scope/"})
			var barrier *swifttest.HTTPResponseBarrier
			var crafted *bulkIdentityServer
			if strings.HasPrefix(fault, "index") {
				crafted = &bulkIdentityServer{inner: h.mock, reply: map[string]any{
					"Number Deleted": 0, "Number Not Found": 0, "Response Status": "200 OK", "Response Body": "",
					"Errors": [][2]string{{"/segments/scope/a", "500 Internal Server Error"}, {"/segments/scope/b", "500 Internal Server Error"}},
				}}
				var err error
				barrier, err = swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
					Operation: "forced-dlo-accounting", Method: http.MethodPost,
					EscapedPath: "/v1/AUTH_test", RawQuery: "bulk-delete", Occurrence: 1,
				}, false, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(barrier.Wrap(crafted))
				t.Cleanup(server.Close)
				t.Cleanup(barrier.Close)
				h.authURL = server.URL + "/auth/v1.0"
			}
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
			if fault == "setup" {
				if err := os.WriteFile(filepath.Join(cache, "aeronautes"), []byte("owned cache obstruction"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "index_and_audit" {
				if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
					_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER refuse_partial_segments BEFORE INSERT ON audit_events
						WHEN NEW.operation='segments.delete' BEGIN SELECT RAISE(ABORT,'fixture segment audit refusal'); END`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			type outcome struct {
				result *SegmentsDeleteResultDTO
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := service.DeleteManifestWithSegments(t.Context(), SegmentsDeleteRequestDTO{
					BookmarkID: bookmark, Container: "manifests", Name: "selected", Confirm: "selected", Force: true,
				})
				done <- outcome{result, err}
			}()
			if barrier != nil {
				if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
					t.Fatalf("admitted segment batch: %+v %v", event, err)
				}
				refuseDeletionAccounting(t, cache)
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
			}
			var got outcome
			select {
			case got = <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("segment deletion did not return")
			}
			if barrier != nil && barrier.Err() != nil {
				t.Fatal(barrier.Err())
			}
			if got.result == nil || !got.result.ManifestDeleted {
				t.Fatalf("manifest effect lost: %+v %v", got.result, got.err)
			}
			incomplete := strings.HasPrefix(fault, "index")
			if got.result.DeletionAccountingIncomplete != incomplete {
				t.Errorf("incorrect segment counts qualifier: %+v", got.result)
			}
			if fault == "control" {
				if got.err != nil || got.result.SegmentsDeleted != 2 || got.result.SegmentsFailed != 0 {
					t.Fatalf("healthy DLO cleanup control: %+v %v", got.result, got.err)
				}
			} else {
				if !errors.Is(got.err, deletion.ErrIdentityAccounting) || !strings.Contains(got.err.Error(), "the manifest is gone") || got.result.SegmentsDeleted != 0 || got.result.SegmentsFailed != 0 {
					t.Fatalf("segment storage failure lost prior effect: %+v %v", got.result, got.err)
				}
				if incomplete && (!strings.Contains(got.err.Error(), "final failure and unresolved totals are unavailable") || !strings.Contains(got.err.Error(), "some requests may have taken effect")) {
					t.Errorf("error-only frontend result loses unavailable totals: %v", got.err)
				}
				if fault == "index_and_audit" && (!strings.Contains(got.err.Error(), "record partial segment deletion audit") || !strings.Contains(got.err.Error(), "fixture segment audit refusal")) {
					t.Errorf("partial audit failure hidden: %v", got.err)
				}
			}
			paths, err := filepath.Glob(filepath.Join(cache, "aeronautes", "deletion-attempts", "*.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if incomplete {
				if len(paths) != 1 || !strings.Contains(got.err.Error(), paths[0]) {
					t.Fatalf("DLO admitted-attempt evidence missing: %v %v", paths, got.err)
				}
				attempts := readDeletionAttempts(t, paths[0])
				if len(attempts) != 2 || attempts[0].Name != "scope/a" || attempts[1].Name != "scope/b" || crafted.calls != 1 {
					t.Errorf("DLO issued attempt set: %+v calls=%d", attempts, crafted.calls)
				}
			} else if len(paths) != 0 {
				t.Errorf("invented DLO unindexed attempts: %v", paths)
			}
			events, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "segments.delete", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if fault == "index_and_audit" {
				if len(events) != 0 {
					t.Errorf("rejected audit became visible: %+v", events)
				}
			} else {
				want := "verify_mismatch"
				if fault == "control" {
					want = "applied"
				}
				if len(events) != 1 || events[0].Result != want || events[0].Resource != "manifests/selected" {
					t.Errorf("DLO audit: %+v want=%s", events, want)
				}
				if incomplete && len(events) == 1 && events[0].Summary != "partial: manifest deleted; deletion accounting incomplete" {
					t.Errorf("DLO audit lost controlled qualifier: %+v", events[0])
				}
			}
			session, err := h.transfer.connections.SessionFor(t.Context(), bookmark)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.ObjectDetail(t.Context(), "manifests", "selected"); !swiftcontrol.IsNotFound(err) {
				t.Errorf("manifest effect not witnessed: %v", err)
			}
			for _, name := range []string{"scope/a", "scope/b"} {
				_, err := session.ObjectDetail(t.Context(), "segments", name)
				if (fault == "control" && !swiftcontrol.IsNotFound(err)) || (fault != "control" && err != nil) {
					t.Errorf("segment effect differs for %s: %v", name, err)
				}
			}
		})
	}
}
