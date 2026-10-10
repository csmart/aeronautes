package local

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

func TestStagedUploadRetainsCloseAndLaterProblems(t *testing.T) {
	for _, mode := range []string{"control", "close", "close_cleanup", "close_verification"} {
		t.Run(mode, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			stage := ".aeronautes/stage/close-fault/batch/source"
			seed(mock, "media", stage, "bytes")
			seed(mock, "media", "unselected", "remote sentinel")
			seed(mock, "media", ".aeronautes/stage/other-job/keep", "other job")
			seed(mock, "outside", "keep", "outside sentinel")
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)
			var heads, lengthFaults, deletes atomic.Int32
			var liveWritten atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/AUTH_test/media/"+stage {
					switch r.Method {
					case http.MethodHead:
						if heads.Add(1) == 2 && mode != "control" {
							// An uncached length probe disagrees with the full GET;
							// the wire reader reports this only when it is closed.
							lengthFaults.Add(1)
							w.Header().Set("Content-Length", "6")
							w.WriteHeader(http.StatusOK)
							return
						}
					case http.MethodGet:
						if mode != "control" {
							w.Header().Set("Content-Type", "application/octet-stream")
							w.Header().Set("Transfer-Encoding", "chunked")
							w.WriteHeader(http.StatusOK)
							w.(http.Flusher).Flush()
							if _, err := io.WriteString(w, "bytes"); err != nil {
								t.Errorf("staged fixture body: %v", err)
							}
							return
						}
					case http.MethodDelete:
						deletes.Add(1)
						if mode == "close_cleanup" {
							http.Error(w, "fixture cleanup refusal", http.StatusForbidden)
							return
						}
					}
				}
				if r.URL.Path == "/v1/AUTH_test/media/live/source" {
					if r.Method == http.MethodHead && liveWritten.Load() && mode == "close_verification" {
						w.Header().Set("Content-Length", "1")
						w.WriteHeader(http.StatusOK)
						return
					}
					if r.Method == http.MethodPut {
						defer liveWritten.Store(true)
					}
				}
				mock.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			item := UploadItem{Path: writeFile(t, t.TempDir(), "source", "bytes"), Name: "live/source", Size: 5}
			result := commitStagedUpload(t.Context(), session, "media", stage, item, item.Replaces, nil)
			want := transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5}
			issues, wantDeletes := int64(0), int32(1)
			if mode != "control" {
				issues = 1
				want.Effect = transfercopy.EffectUnresolved
				want.Diagnostic = transfercopy.Diagnostic{Stage: transfercopy.StageStaging, Code: transfercopy.CodeIO}
				if lengthFaults.Load() != 1 {
					t.Fatal("fixture did not reach the deferred wire length check")
				}
			}
			if mode == "close_cleanup" {
				issues = 2
			}
			if mode == "close_verification" {
				wantDeletes = 0
				want = transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved,
					Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageVerification, Code: transfercopy.CodeUnverified}}
			}
			if result.Observation != want || int64(len(result.issues)) != issues || deletes.Load() != wantDeletes || result.verification == nil {
				t.Fatalf("closure or later issue erased the final observation: %+v want=%+v issues=%d deletes=%d", result, want, issues, deletes.Load())
			}
			if mode != "control" && result.issues[0].label != "staged-close" || mode == "close_cleanup" && result.issues[1].label != "staging-cleanup" {
				t.Fatalf("simultaneous upload problems were collapsed: %+v", result.issues)
			}
			run := &uploadRun{report: &Report{}}
			if err := run.observe(item, result); err != nil {
				t.Fatal(err)
			}
			if run.report.PostCommitIssues != issues || len(run.report.PostCommitIssueSamples) != int(issues) {
				t.Fatalf("closure issues lost exact accounting: %+v", run.report)
			}
			if mode == "close_verification" {
				if run.report.Done != 0 || run.report.Bytes != 0 || len(run.report.Failures) != 1 || result.verification.Result != transfercopy.ResultFailed {
					t.Fatalf("verification and closure failure counted one name twice: %+v", run.report)
				}
			} else if run.report.Done != 1 || run.report.Bytes != 5 || len(run.report.Failures) != 0 || result.verification.Result != transfercopy.ResultVerified {
				t.Fatalf("post-copy problems erased witnessed bytes: %+v", run.report)
			}
			after := uploadSnapshot(t, witness)
			object := after.Containers["media"].Objects[item.Name]
			if object.Size != 5 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("bytes"))) || object.Class != "plain" {
				t.Fatalf("close fixture lacks independently readable copied bytes: %+v", object)
			}
			delete(after.Containers["media"].Objects, item.Name)
			if mode == "control" || mode == "close" {
				if _, exists := after.Containers["media"].Objects[stage]; exists {
					t.Fatal("successful cleanup left the selected staging object")
				}
				delete(before.Containers["media"].Objects, stage)
			}
			if delta := oracle.Diff(before, after); !delta.Empty() {
				t.Fatalf("close fixture changed unpermitted owned data: %s", delta)
			}
		})
	}
}
