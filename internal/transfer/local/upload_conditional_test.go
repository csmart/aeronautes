package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func TestStagedUploadPreservesFinalHeadRacer(t *testing.T) {
	for _, kind := range []string{"plain", "dlo"} {
		t.Run(kind, func(t *testing.T) {
			// The SECOND HEAD of this name is the commit's own final read
			// before its conditional PUT, which is the boundary under test.
			// The first is the preflight's: this selection is one file, which
			// the destination resolution settles by name rather than by
			// listing. That dependence is stated because an occurrence which
			// no longer matches leaves this barrier holding the wrong
			// response until the deadline, rather than failing loudly.
			selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodHead, EscapedPath: "/v1/AUTH_test/media/docs/object", Occurrence: 2}
			barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			mock := swifttest.New("test:tester", "testing")
			server := httptest.NewServer(barrier.Wrap(mock))
			t.Cleanup(server.Close)
			t.Cleanup(barrier.Close)
			s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			mock.AddContainer("media", "standard")
			path := writeFile(t, t.TempDir(), "object", "ours")
			preflight, err := PreflightUpload(t.Context(), s, "media", "docs/", []string{path})
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 {
				t.Fatalf("new staged upload was not admitted: %+v, %v", preflight, err)
			}
			stage := ".aeronautes/stage/conditional-upload/batch/"
			seed(mock, "media", stage+"object", "ours")
			if kind == "dlo" {
				seed(mock, "media_segments", "upload/leaf", "ours")
				mock.PutObject("media", stage+"object", swifttest.Object{Manifest: "media_segments/upload/"})
			}
			seed(mock, "outside", "keep", "outside content")
			witness := uploadWitness(t, mock)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			done := make(chan *Report, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				barrier.Close()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("upload commit did not stop after fixture cancellation")
				}
			})
			go func() {
				defer close(finished)
				report := &Report{}
				u := &uploadRun{report: report}
				result := commitStagedUpload(ctx, s, "media", stage+"object", preflight.Items[0], preflight.Items[0].Replaces, nil, nil)
				if err := u.observe(preflight.Items[0], result); err != nil {
					t.Error(err)
				}
				done <- report
			}()
			event, err := barrier.Wait(ctx)
			if err != nil || event.HTTPResponseSelector != selector || event.StatusCode != http.StatusNotFound {
				t.Fatalf("final upload HEAD boundary: %+v, %v", event, err)
			}
			seed(mock, "media", "docs/object", "concurrent writer")
			before := uploadSnapshot(t, witness)
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			select {
			case report := <-done:
				if report.Done != 0 || len(report.Failures) != 1 || report.Failures[0].Name != "docs/object" || report.Failures[0].Stage != "commit-conflict" {
					t.Errorf("staged conditional conflict was not reported: %+v", report)
				}
			case <-ctx.Done():
				t.Fatal("upload did not finish after releasing its write boundary")
			}
			if err := barrier.Err(); err != nil {
				t.Fatal(err)
			}
			if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
				t.Errorf("upload commit harmed a concurrent writer or backing data: %s", diff)
			}
		})
	}
}
