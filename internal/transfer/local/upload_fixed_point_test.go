package local

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// ADR 0085's fixed-point comparison: a fresh observation taken after all
// preparation and immediately before the publishing request, compared with what
// the operator was shown when they accepted replacing the name.
//
// It is a fresh read and not either stored record, because observePredecessor
// runs BEFORE the preparation that decision refuses to leave inside the window —
// the native route hashes the whole local file after it — so the acknowledged
// baseline and that record can agree while the replacement lands between them.
// versioningHeld answers ADR 0102 decision 2's question for cases whose
// subject is not the observation: the container still reports what the
// question was composed from.
func versioningHeld(context.Context, UploadItem) (string, error) { return "", nil }

// liveContextKey marks the context a case hands the route, so a hook can
// prove the route forwarded that context to it. It proves forwarding and no
// more: context.WithoutCancel keeps values, so a marker cannot show that a
// cancellation reaches the read; the app's held-response checks establish
// that.
type liveContextKey struct{}

func TestFixedPointComparisonRefusesWhatWasNotAcknowledged(t *testing.T) {
	const prior = "the bytes already there"
	for _, c := range []struct {
		name string
		// arrange runs after the baseline is captured and before the
		// publication, so it is the interleaving under test.
		arrange func(t *testing.T, s *swiftcontrol.Session, mock *swifttest.Server)
		hook    func(t *testing.T, s *swiftcontrol.Session, item UploadItem) func(UploadItem) (ShownObject, bool, error)
		// versioning answers ADR 0102 decision 2's question in place of the
		// default that says the statement still holds; noVersioningHook
		// leaves the question with nobody to answer it.
		versioning       func(context.Context, UploadItem) (string, error)
		noVersioningHook bool
		// stopped says the run must stop as well as refuse: the refusal
		// could not be recorded.
		stopped   bool
		published bool
		// says is what the operator must be told, where the reason matters as
		// much as the refusal.
		says string
	}{
		{
			name:      "the name still holds what was acknowledged",
			published: true,
		},
		{
			name: "another object of the same length is there now",
			arrange: func(t *testing.T, _ *swiftcontrol.Session, mock *swifttest.Server) {
				seed(mock, "media", "docs/object", strings.Repeat("x", len(prior)))
			},
		},
		{
			name: "the acknowledged object has gone",
			arrange: func(t *testing.T, s *swiftcontrol.Session, _ *swifttest.Server) {
				if err := s.DeleteObject(t.Context(), "media", "docs/object"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// The REASON is the assertion here, not just the refusal. Ignoring
			// the unidentified result still refuses, because the fresh read is
			// held to the completeness rule and so can never equal an empty
			// baseline — but it refuses by handing the operator a comparison
			// against nothing, which says the wrong thing about why.
			name: "the acknowledgement captured no identity",
			hook: func(*testing.T, *swiftcontrol.Session, UploadItem) func(UploadItem) (ShownObject, bool, error) {
				return func(UploadItem) (ShownObject, bool, error) { return ShownObject{}, false, nil }
			},
			says: "nothing to compare against",
		},
		{
			name: "nothing can be asked what was acknowledged",
			hook: func(*testing.T, *swiftcontrol.Session, UploadItem) func(UploadItem) (ShownObject, bool, error) {
				return nil
			},
		},
		{
			// ADR 0102 decision 2: the container no longer reports what the
			// question was composed from, so the replacement is refused before
			// the object is compared, and the operator is told what changed.
			// The hook sees the context the route was given: forwarding is
			// what the marker proves, not that a cancellation reaches the read.
			name: "the container no longer reports the versioning the question was composed from",
			versioning: func(ctx context.Context, _ UploadItem) (string, error) {
				if ctx.Value(liveContextKey{}) == nil {
					return "", errors.New("the hook was asked with a context that is not the route's")
				}
				return "the container now reports versions_location; the question said none", nil
			},
			says: "the question said none",
		},
		{
			name:             "nothing can ask whether the versioning still holds",
			noVersioningHook: true,
			says:             "versioning read again",
		},
		{
			// A refusal that could not be recorded still refuses, and the run
			// stops, as it does when an identity refusal has nowhere to go.
			name: "the refusal on versioning could not be recorded",
			versioning: func(context.Context, UploadItem) (string, error) {
				return "", errors.New("the audit store is unavailable")
			},
			says:    "could not be recorded",
			stopped: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			seed(mock, "media", "docs/object", prior)
			server := httptest.NewServer(mock)
			t.Cleanup(server.Close)
			s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
			item := replacingItem(t, s, path)

			hook := acknowledgedNow(t, s, "media", item)
			if c.hook != nil {
				hook = c.hook(t, s, item)
			}
			// Capture before the interleaving, which is what an acknowledgement
			// taken before it would have held.
			if hook != nil {
				if _, _, err := hook(item); err != nil {
					t.Fatal(err)
				}
			}
			if c.arrange != nil {
				c.arrange(t, s, mock)
			}
			recorder := &UploadRecorder{Acknowledged: hook, VersioningUnchanged: versioningHeld}
			if c.versioning != nil {
				recorder.VersioningUnchanged = c.versioning
			}
			if c.noVersioningHook {
				recorder.VersioningUnchanged = nil
			}
			run := &uploadRun{report: &Report{}, recorder: recorder}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			live := context.WithValue(t.Context(), liveContextKey{}, true)
			result := nativeUpload(live, s, "media", item, file, true, run.confirmUnchanged())

			if c.published {
				if result.Result != transfercopy.ResultCopied {
					t.Fatalf("an unchanged name was refused: %+v %v", result.Observation, result.failure)
				}
				return
			}
			if (run.stopped != nil) != c.stopped {
				t.Errorf("run stopped = %v, want %v", run.stopped, c.stopped)
			}
			// A conflict, because a concurrent change was detected; and
			// nothing attempted, because the request was never sent. Those
			// are separate fields and both matter to what an operator is
			// told afterwards.
			if result.Result != transfercopy.ResultConflict {
				t.Fatalf("the publication was not refused as a conflict at the fixed point: %+v", result.Observation)
			}
			if result.Effect != transfercopy.EffectNotAttempted {
				t.Errorf("a refusal before the request claimed an effect: %+v", result.Observation)
			}
			if result.Diagnostic.Code != transfercopy.CodeConflict {
				t.Errorf("code = %v, want the conflict verdict", result.Diagnostic.Code)
			}
			if c.says != "" {
				if result.failure == nil || !strings.Contains(result.failure.Error(), c.says) {
					t.Errorf("the refusal does not say why: %v, want it to mention %q", result.failure, c.says)
				}
			}
			if result.label != "native-reobserve" {
				t.Errorf("the refusal is not attributed to the comparison: %q", result.label)
			}
		})
	}
}

// A cancel that lands in the versioning read is the job stopping, not a finding
// about the container: the item is coded cancelled, as a cancel during the
// object read beside it always was, and the run is not stopped by it.
func TestFixedPointVersioningReadCancelledIsNotAConflict(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "the bytes already there")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
	item := replacingItem(t, s, path)
	hook := acknowledgedNow(t, s, "media", item)
	if _, _, err := hook(item); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{Acknowledged: hook,
		VersioningUnchanged: func(context.Context, UploadItem) (string, error) {
			cancel()
			return "the job was cancelled before the container's versioning was read again", nil
		}}}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(ctx, s, "media", item, file, true, run.confirmUnchanged())
	if result.Diagnostic.Code != transfercopy.CodeCancelled || result.Effect != transfercopy.EffectNotAttempted {
		t.Fatalf("a cancelled read was not coded as a cancellation before any request: %+v %v", result.Observation, result.failure)
	}
	if run.stopped != nil {
		t.Errorf("a cancellation stopped the run as an unrecorded refusal would: %v", run.stopped)
	}

	// A refusal that could not be recorded is lost whether or not the job
	// was cancelled at the same moment, so it still stops the run.
	both, cancelBoth := context.WithCancel(t.Context())
	defer cancelBoth()
	lost := &uploadRun{report: &Report{}, recorder: &UploadRecorder{Acknowledged: hook,
		VersioningUnchanged: func(context.Context, UploadItem) (string, error) {
			cancelBoth()
			return "", errors.New("the audit store is unavailable")
		}}}
	again, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	nativeUpload(both, s, "media", item, again, true, lost.confirmUnchanged())
	if lost.stopped == nil || !strings.Contains(lost.stopped.Error(), "could not be recorded") {
		t.Errorf("a cancel hid a refusal that could not be recorded: stopped = %v", lost.stopped)
	}
}

// A replacement handed NO comparison at all is refused, which is a different
// guard from a comparison that can find no acknowledgement: this one protects
// against a caller that never wired the comparison in. The run always wires it,
// so nothing in production reaches here — and that is exactly why it needs a
// case of its own, since a guard no test exercises is a guard nobody knows is
// working.
func TestPublicationWithNoComparisonRefusesAReplacement(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "the bytes already there")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
	item := replacingItem(t, s, path)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	result := nativeUpload(t.Context(), s, "media", item, file, true, nil)
	if result.Result != transfercopy.ResultConflict || result.Effect != transfercopy.EffectNotAttempted {
		t.Fatalf("a replacement with no comparison was not refused: %+v", result.Observation)
	}
	// And the prior object is untouched, which is the point of refusing.
	detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil || detail.ContentLength != int64(len("the bytes already there")) {
		t.Errorf("the refused publication disturbed the prior object: %+v %v", detail, err)
	}
}

// The control the cases above need: with nothing acknowledged and nothing being
// replaced, no comparison is made and no hook is required, because the create's
// own conditional is what protects it.
func TestFixedPointComparisonIsNotMadeForACreate(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
	preflight, err := PreflightUpload(t.Context(), s, "media", "docs/", []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Replaces {
		t.Fatalf("the fixture did not admit exactly one create: %+v %v", preflight, err)
	}
	item := preflight.Items[0]
	item.Route = "native"
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// No hook at all, which would refuse a replacement.
	run := &uploadRun{report: &Report{}}
	if result := nativeUpload(t.Context(), s, "media", item, file, false, run.confirmUnchanged()); result.Result != transfercopy.ResultCopied {
		t.Fatalf("a create was refused by a comparison it does not need: %+v", result.Observation)
	}
}

// When observePredecessor finds the name already absent, the
// publication is a CREATE — publishHeaders restores If-None-Match: * and
// ADR 0085 keeps that transition expressly. The comparison must not refuse it
// on the strength of the preflight's historical flag, which still says the name
// was live when the operator was asked.
func TestVanishedPredecessorPublishesAsACreateRatherThanBeingCompared(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "the bytes already there")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
	item := replacingItem(t, s, path)

	// A baseline WAS captured, as it would have been: the name was live when
	// the operator answered. Then it goes away before the run looks.
	hook := acknowledgedNow(t, s, "media", item)
	if _, _, err := hook(item); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObject(t.Context(), "media", "docs/object"); err != nil {
		t.Fatal(err)
	}
	run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{
		Acknowledged: hook,
		Replacing:    func(UploadItem, ObservedPredecessor) error { return nil },
	}}
	present, err := run.observePredecessor(t.Context(), s, "media", item)
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("the fixture did not remove the predecessor before the run looked")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(t.Context(), s, "media", item, file, present, run.confirmUnchanged())
	if result.Result != transfercopy.ResultCopied {
		t.Fatalf("a publication ADR 0085 keeps as a create was refused: %+v %v", result.Observation, result.failure)
	}
}

// The fresh observation is held to the same completeness rule as the
// capture. Without it a HEAD stating no length arrives as a zero that compares
// equal to a genuine empty object's baseline, and the replacement proceeds
// against an identity nobody established.
func TestFixedPointRefusesAFreshReadItCannotIdentify(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "")
	server := dropHeadHeader(t, mock, "/v1/AUTH_test/media/docs/object", "Content-Length")
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
	item := UploadItem{Path: path, Name: "docs/object", Size: 29, Route: "native", Replaces: true}
	// The acknowledgement captured a COMPLETE identity for a genuinely empty
	// object, which is valid. The fresh read states no length, so its zero must
	// not be allowed to match it.
	empty := ShownObject{Class: "plain", Length: 0, ETag: md5hex("")}
	run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{
		Acknowledged:        func(UploadItem) (ShownObject, bool, error) { return empty, true, nil },
		VersioningUnchanged: versioningHeld}}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(t.Context(), s, "media", item, file, true, run.confirmUnchanged())
	if result.Result != transfercopy.ResultConflict || result.Effect != transfercopy.EffectNotAttempted {
		t.Fatalf("a fresh read that identified nothing was accepted as a match: %+v", result.Observation)
	}
}

// The staged plain publication OPENS the staged object before it
// publishes, and that open is a request of its own. A writer arriving during it
// must still be caught, which is only true if the comparison follows the open.
func TestStagedCommitComparesAfterOpeningThePublishingReader(t *testing.T) {
	const intruder = "another writer's bytes, arriving while the stage was being opened"
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "the bytes already there")
	stage := ".aeronautes/stage/fixed-point/batch/object"
	seed(mock, "media", stage, "the bytes this job would send")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job would send")
	item := replacingItem(t, s, path)
	item.Route = "engine"
	hook := acknowledgedNow(t, s, "media", item)
	if _, _, err := hook(item); err != nil {
		t.Fatal(err)
	}
	// The writer lands exactly when the staged object's body has been served,
	// which is after a comparison placed before that open and before one placed
	// after it.
	var once bool
	mock.AfterRequest = func(method, path string) {
		if once || method != "GET" || !strings.HasSuffix(path, stage) {
			return
		}
		once = true
		seed(mock, "media", "docs/object", intruder)
	}
	run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{Acknowledged: hook, VersioningUnchanged: versioningHeld}}
	result := commitStagedUpload(t.Context(), s, "media", stage, item, true, run.confirmUnchanged(), nil)
	if result.Result != transfercopy.ResultConflict || result.Effect != transfercopy.EffectNotAttempted {
		t.Fatalf("a writer arriving while the stage was opened was published over: %+v", result.Observation)
	}
	detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil || detail.ContentLength != int64(len(intruder)) {
		t.Errorf("the writer's bytes did not survive: %+v %v", detail, err)
	}
}
