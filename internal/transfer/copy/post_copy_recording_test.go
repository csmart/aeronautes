package copy

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

type copyRecordingTransport func(*http.Request) (*http.Response, error)

func (f copyRecordingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type copyRecordingBody struct {
	io.ReadCloser
	closed *bool
	fault  error
}

func (b copyRecordingBody) Close() error {
	*b.closed = true
	return errors.Join(b.ReadCloser.Close(), b.fault)
}

func TestCopyRecordsAfterPostCopyActions(t *testing.T) {
	for _, mode := range []string{"native", "native_close", "staged", "staged_close", "staged_cleanup", "staged_both", "native_symlink", "native_symlink_close", "staged_symlink", "staged_symlink_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			native, symlink := strings.HasPrefix(mode, "native"), strings.Contains(mode, "symlink")
			closeFailure := strings.HasSuffix(mode, "close") || mode == "staged_both"
			cleanupFailure := strings.HasSuffix(mode, "cleanup") || mode == "staged_both"
			closeFault := errors.New("fixture close failure https://user:password.invalid/?token=private")
			closed, cleaned := false, false
			original := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = original })
			http.DefaultTransport = copyRecordingTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodDelete && r.URL.Path == "/v1/AUTH_test/backup/.aeronautes/stage/recorded/batch/object" {
					cleaned = true
					if cleanupFailure {
						return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture cleanup token=private")), Request: r}, nil
					}
				}
				response, err := original.RoundTrip(r)
				bodyPath := "/v1/AUTH_test/backup/.aeronautes/stage/recorded/batch/object"
				if native {
					bodyPath = "/v1/AUTH_test/media/object"
				}
				if err == nil && r.Method == http.MethodGet && r.URL.Path == bodyPath && r.URL.RawQuery == "" {
					var fault error
					if closeFailure {
						fault = closeFault
					}
					response.Body = copyRecordingBody{ReadCloser: response.Body, closed: &closed, fault: fault}
				}
				return response, err
			})
			w := newWorld(t)
			seed(w.srcMock, "media", "object", []byte("copy bytes"), nil)
			seed(w.srcMock, "media", "target", []byte("copy bytes"), nil)
			if symlink {
				w.srcMock.PutObject("media", "object", swifttest.Object{SymlinkTarget: "media/target"})
			}
			var observed []Observation
			e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, &Recorder{
				Admit: func(Admission) error { return nil },
				Execute: func(_ string, observation Observation) error {
					if (native || !symlink) && !closed {
						t.Error("final recording preceded stream close")
					}
					if !native && !cleaned {
						t.Error("final recording preceded staging cleanup")
					}
					if symlink {
						link, err := w.dst.SymlinkGet(t.Context(), "backup", "object")
						if err != nil || link.SymlinkTarget != "media/target" {
							t.Fatalf("copy observation has no independently readable link: %+v %v", link, err)
						}
					} else if headDst(t, w, "object").ContentLength != 10 {
						t.Fatal("copy observation has no independently readable bytes")
					}
					observed = append(observed, observation)
					return nil
				}, Verify: func(string, Observation) error { return nil }})
			route := plan.RouteRclone
			if native {
				route = plan.RouteNative
			}
			report, err := e.Run(t.Context(), []plan.Item{{Name: "object", Verdict: plan.VerdictNew, Route: route, SourceBytes: 10}})
			want := Observation{Result: ResultCopied, Effect: EffectReportedApplied, CopiedBytes: 10}
			if symlink {
				want.CopiedBytes = 0
			}
			if closeFailure || cleanupFailure {
				want.Effect = EffectUnresolved
				if cleanupFailure {
					want.Diagnostic = Diagnostic{Stage: StageCleanup, Code: CodeForbidden, HTTPStatus: 403}
				} else {
					stage := StageStaging
					if native {
						stage = StageSourceRead
					}
					want.Diagnostic = Diagnostic{Stage: stage, Code: CodeUnknown}
				}
			}
			if (err != nil) != (closeFailure || cleanupFailure) || (closeFailure && !errors.Is(err, closeFault)) ||
				len(observed) != 1 || observed[0] != want || report.Copied != 1 || report.BytesCopied != want.CopiedBytes || len(report.Failures) != 0 {
				t.Fatalf("post-copy actions lost bytes, problem or final ordering: report=%+v observed=%+v want=%+v err=%v", report, observed, want, err)
			}
			if cleanupFailure {
				var swiftErr *swiftcontrol.SwiftError
				if !errors.As(err, &swiftErr) || swiftErr.HTTPStatus != 403 || len(report.StagedResidue) != 1 {
					t.Fatalf("cleanup problem vanished or residue was cleared: %+v %v", report, err)
				}
			}
		})
	}
}
