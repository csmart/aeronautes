package deletion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// craftedBulkServer answers exactly the bulk-delete POST from a queue of
// canned replies, leaving authentication, capabilities, containers and
// listing to the real mock. When forward is set the request is first
// replayed into the mock, so the selected objects are genuinely
// deleted before the canned reply is returned: that is what lets one
// reply describe two opposite effects.
type craftedBulkServer struct {
	inner   http.Handler
	mu      sync.Mutex
	replies []map[string]any
	forward bool
	bodies  []string
	sent    [][]byte
	calls   int
}

func (c *craftedBulkServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete") {
		body, _ := io.ReadAll(r.Body)
		if c.forward {
			replay := httptest.NewRequest(http.MethodPost, r.URL.String(), bytes.NewReader(body))
			replay.Header = r.Header.Clone()
			c.inner.ServeHTTP(httptest.NewRecorder(), replay)
		}
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		reply := c.replies[min(c.calls, len(c.replies)-1)]
		c.calls++
		c.mu.Unlock()
		payload, _ := json.Marshal(reply)
		c.mu.Lock()
		c.sent = append(c.sent, payload)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}
	c.inner.ServeHTTP(w, r)
}

func craftedCluster(t *testing.T, replies ...map[string]any) (*swiftcontrol.Session, *swiftcontrol.Capabilities, *swifttest.Server, *craftedBulkServer) {
	t.Helper()
	return craftedClusterForwarding(t, false, replies...)
}

func craftedClusterForwarding(t *testing.T, forward bool, replies ...map[string]any) (*swiftcontrol.Session, *swiftcontrol.Capabilities, *swifttest.Server, *craftedBulkServer) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	crafted := &craftedBulkServer{inner: mock, replies: replies, forward: forward}
	srv := httptest.NewServer(crafted)
	t.Cleanup(srv.Close)
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mock.AddContainer("media", "standard")
	return session, capabilities, mock, crafted
}

func namesN(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("n%04d", i)
	}
	return out
}

func reply(deleted, notFound int, errors ...[2]string) map[string]any {
	rows := [][2]string{}
	rows = append(rows, errors...)
	return map[string]any{
		"Number Deleted": deleted, "Number Not Found": notFound,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": rows,
	}
}

// wire renders the request line the wire layer builds for a name, using
// the same standard-library call so the helper cannot drift from it.
func wire(container, name string) string {
	segments := strings.Split(name, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return "/" + container + "/" + strings.Join(segments, "/")
}

type ledger struct {
	failed     []string
	unresolved []string
	details    map[string]string
	result     *Result
	err        error
}

func runNames(t *testing.T, session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities,
	names []string, opts Options) ledger {
	t.Helper()
	var mu sync.Mutex
	l := ledger{details: map[string]string{}}
	opts.OnFailure = func(name, detail string) {
		mu.Lock()
		l.failed = append(l.failed, name)
		l.details[name] = detail
		mu.Unlock()
	}
	opts.OnUnresolved = func(name, detail string) {
		mu.Lock()
		l.unresolved = append(l.unresolved, name)
		mu.Unlock()
	}
	l.result, l.err = DeleteNames(context.Background(), session, capabilities, "media", names, opts)
	sort.Strings(l.failed)
	sort.Strings(l.unresolved)
	// The uncapped streams and the exact counts must always agree, and
	// the bounded samples must stop exactly at the display cap.
	if int64(len(l.failed)) != l.result.Failed {
		t.Errorf("failure stream %d disagrees with the exact count %d", len(l.failed), l.result.Failed)
	}
	if int64(len(l.unresolved)) != l.result.Unresolved {
		t.Errorf("unresolved stream %d disagrees with the exact count %d", len(l.unresolved), l.result.Unresolved)
	}
	if len(l.failed) <= maxFailures && len(l.result.Failures) != len(l.failed) {
		t.Errorf("failure sample %d disagrees with the stream %d below the cap", len(l.result.Failures), len(l.failed))
	}
	if len(l.failed) > maxFailures && len(l.result.Failures) != maxFailures {
		t.Errorf("failure sample %d is not capped at %d", len(l.result.Failures), maxFailures)
	}
	for _, name := range append(append([]string{}, l.failed...), l.unresolved...) {
		if strings.HasPrefix(name, "/") {
			t.Errorf("a wire path entered the ledger as %q", name)
		}
	}
	return l
}

func equal(got, want []string) bool {
	w := append([]string(nil), want...)
	sort.Strings(w)
	return strings.Join(got, "\x00") == strings.Join(w, "\x00")
}

// TestBulkUntrustedReplyLeavesEveryNameUnresolved is the maintained
// regression for the bulk-delete half of DS-08. A reply that fails
// validation says nothing dependable about which names it handled, so
// every requested name's effect is unresolved, the names it validly
// blamed are also reported failures, and its counts are credited to
// nothing.
func TestBulkUntrustedReplyLeavesEveryNameUnresolved(t *testing.T) {
	cases := []struct {
		label      string
		requested  []string
		reply      map[string]any
		wantFailed []string
	}{
		{"one row naming the last name, nothing else accounted", namesN(5),
			reply(0, 0, [2]string{wire("media", "n0004"), "500 Internal Server Error"}),
			[]string{"n0004"}},
		{"rows out of request order", namesN(5),
			reply(0, 0, [2]string{wire("media", "n0003"), "500 Internal Server Error"},
				[2]string{wire("media", "n0000"), "503 Service Unavailable"}),
			[]string{"n0000", "n0003"}},
		{"the same row twice", namesN(5),
			reply(0, 0, [2]string{wire("media", "n0003"), "500 Internal Server Error"},
				[2]string{wire("media", "n0003"), "500 Internal Server Error"}),
			[]string{"n0003"}},
		{"two rows disagreeing about one name", namesN(2),
			reply(0, 0, [2]string{wire("media", "n0000"), "500 Internal Server Error"},
				[2]string{wire("media", "n0000"), "409 Conflict"}),
			[]string{"n0000"}},
		{"a row naming a path never requested", namesN(4),
			reply(0, 0, [2]string{wire("media", "never-requested"), "500 Internal Server Error"}),
			nil},
		{"counts claim successes and one name is unaccounted", namesN(5),
			reply(3, 0, [2]string{wire("media", "n0000"), "500 Internal Server Error"}),
			[]string{"n0000"}},
		{"a row whose status is empty", namesN(3),
			reply(0, 0, [2]string{wire("media", "n0000"), ""}),
			nil},
		{"a row whose status is itself a success", namesN(3),
			reply(0, 0, [2]string{wire("media", "n0000"), "200 OK"}),
			nil},
		{"a row whose status cannot be parsed", namesN(3),
			reply(0, 0, [2]string{wire("media", "n0000"), "broken"}),
			nil},
		{"a single name and nothing accounted", namesN(1), reply(0, 0), nil},
		{"names whose spelling needs escaping", []string{"dir/a b", "dir/c%d", "héllo", "line\nbreak", "plain"},
			reply(0, 0, [2]string{wire("media", "dir/a b"), "500 Internal Server Error"}),
			[]string{"dir/a b"}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			session, capabilities, _, _ := craftedCluster(t, tc.reply)
			l := runNames(t, session, capabilities, tc.requested, Options{})
			if l.err == nil {
				t.Fatal("an untrusted reply must surface an error")
			}
			if !equal(l.unresolved, tc.requested) {
				t.Errorf("unresolved\n got %v\nwant every requested name %v", l.unresolved, tc.requested)
			}
			if !equal(l.failed, tc.wantFailed) {
				t.Errorf("reported failures\n got %v\nwant %v", l.failed, tc.wantFailed)
			}
			if l.result.Deleted != 0 || l.result.NotFound != 0 {
				t.Errorf("an untrusted reply's counts were credited: %+v", l.result)
			}
			if l.result.UntrustedReplies != 1 {
				t.Errorf("untrusted replies = %d, want 1", l.result.UntrustedReplies)
			}
			for _, name := range tc.wantFailed {
				if !strings.Contains(l.details[name], "5") && !strings.Contains(l.details[name], "4") {
					t.Errorf("%q lost the status the reply reported: %q", name, l.details[name])
				}
			}
		})
	}
}

// TestBulkValidatedReplyCreditsAggregateAndNamesFailures keeps the
// valid partial reply working: one deletion claimed and one genuine row
// is dependable, so its count is credited and exactly one name is a
// reported failure. That name's effect is still unresolved, because
// nothing here observes the store.
func TestBulkValidatedReplyCreditsAggregateAndNamesFailures(t *testing.T) {
	session, capabilities, _, _ := craftedCluster(t,
		reply(1, 0, [2]string{wire("media", "b"), "500 Internal Server Error"}))
	l := runNames(t, session, capabilities, []string{"a", "b"}, Options{})
	if l.err != nil {
		t.Fatalf("a validated reply must not surface an error: %v", l.err)
	}
	if l.result.Deleted != 1 || l.result.UntrustedReplies != 0 {
		t.Errorf("result = %+v", l.result)
	}
	if !equal(l.failed, []string{"b"}) {
		t.Errorf("reported failures = %v, want b alone", l.failed)
	}
	if !equal(l.unresolved, []string{"b"}) {
		t.Errorf("unresolved = %v, want b alone: a is covered by the credited aggregate", l.unresolved)
	}
}

// TestBulkValidatedAllSuccessRecordsNothing is the passing control.
func TestBulkValidatedAllSuccessRecordsNothing(t *testing.T) {
	session, capabilities, _, _ := craftedCluster(t, reply(4, 1))
	l := runNames(t, session, capabilities, namesN(5), Options{})
	if l.err != nil {
		t.Fatalf("a validated reply must not surface an error: %v", l.err)
	}
	if l.result.Deleted != 4 || l.result.NotFound != 1 || l.result.Failed != 0 || l.result.Unresolved != 0 {
		t.Fatalf("result = %+v", l.result)
	}
}

// TestBulkReportedErrorCarriesNoEffectKnowledge is the control pair,
// and the reply it uses is a perfectly valid one: a single requested
// name, no counts, and one well formed row blaming that name. It
// reconciles, so nothing is wrong with it. The same reply nevertheless
// accompanies an object that survived and one that was destroyed, so
// even a dependable report establishes no effect: the run must
// classify both runs identically and assert neither outcome.
func TestBulkReportedErrorCarriesNoEffectKnowledge(t *testing.T) {
	var seen [][]byte
	var outcomes []string
	for _, forward := range []bool{false, true} {
		session, capabilities, mock, crafted := craftedClusterForwarding(t, forward,
			reply(0, 0, [2]string{wire("media", "target"), "500 Internal Server Error"}))
		mock.PutObject("media", "target", swifttest.Object{Bytes: 4, ETag: "aa", Data: []byte("data")})
		mock.PutObject("media", "collateral", swifttest.Object{Bytes: 4, ETag: "bb", Data: []byte("keep")})
		l := runNames(t, session, capabilities, []string{"target"}, Options{})
		if l.err != nil {
			t.Fatalf("this reply is valid and must not surface an error: %v", l.err)
		}
		if l.result.UntrustedReplies != 0 {
			t.Errorf("a valid reply was treated as untrusted: %+v", l.result)
		}
		if !equal(l.failed, []string{"target"}) || !equal(l.unresolved, []string{"target"}) {
			t.Errorf("forward=%v failed=%v unresolved=%v", forward, l.failed, l.unresolved)
		}
		_, headErr := session.ObjectDetail(context.Background(), "media", "target")
		if forward && headErr == nil {
			t.Error("the forwarded run should have destroyed the object")
		}
		if !forward && headErr != nil {
			t.Errorf("the unforwarded run should have left the object: %v", headErr)
		}
		if _, err := session.ObjectDetail(context.Background(), "media", "collateral"); err != nil {
			t.Errorf("collateral was harmed: %v", err)
		}
		seen = append(seen, crafted.sent[0])
		outcomes = append(outcomes, fmt.Sprintf("failed=%v unresolved=%v deleted=%d",
			l.failed, l.unresolved, l.result.Deleted))
	}
	if !bytes.Equal(seen[0], seen[1]) {
		t.Fatal("the two runs must be driven by identical reply bytes")
	}
	if outcomes[0] != outcomes[1] {
		t.Errorf("the same reply produced different outcomes: %q and %q", outcomes[0], outcomes[1])
	}
}

// TestBulkUnrequestedPathIsSurfacedNotSilent keeps a foreign row out of
// the ledger without dropping it in silence.
func TestBulkUnrequestedPathIsSurfacedNotSilent(t *testing.T) {
	session, capabilities, _, _ := craftedCluster(t,
		reply(0, 0, [2]string{wire("media", "not-ours"), "500 Internal Server Error"}))
	l := runNames(t, session, capabilities, namesN(3), Options{})
	if l.err == nil || !strings.Contains(l.err.Error(), "did not send") {
		t.Fatalf("the foreign row was not surfaced: %v", l.err)
	}
	if l.result.Failed != 0 {
		t.Errorf("a foreign row became a reported failure: %+v", l.result.Failures)
	}
	if !equal(l.unresolved, namesN(3)) {
		t.Errorf("unresolved = %v, want every requested name", l.unresolved)
	}
}

// TestBulkLedgerCrossesChunks exercises the chunk arithmetic and the
// display cap boundary rather than assuming them.
func TestBulkLedgerCrossesChunks(t *testing.T) {
	for _, c := range []struct{ total, size int }{{1, 1}, {201, 100}, {500, 201}} {
		t.Run(fmt.Sprintf("total_%d_size_%d", c.total, c.size), func(t *testing.T) {
			requested := namesN(c.total)
			first := requested[:min(c.size, c.total)]
			session, capabilities, _, crafted := craftedCluster(t,
				reply(3, 0, [2]string{wire("media", first[0]), "500 Internal Server Error"}))
			l := runNames(t, session, capabilities, requested, Options{BulkSize: c.size})
			if l.err == nil {
				t.Fatal("an untrusted reply must surface an error")
			}
			if !equal(l.unresolved, first) {
				t.Errorf("unresolved %d identities, want the %d of the failing chunk", len(l.unresolved), len(first))
			}
			if len(crafted.bodies) != 1 {
				t.Errorf("sent %d requests, want the run to stop at the first failure", len(crafted.bodies))
			}
		})
	}
}

// TestBulkLedgerInStreamingPass covers the second call site, reached
// through DeletePrefix, which has the same defect and its own recording.
func TestBulkLedgerInStreamingPass(t *testing.T) {
	session, capabilities, mock, _ := craftedCluster(t,
		reply(0, 0, [2]string{wire("media", "doomed/obj-0003"), "500 Internal Server Error"}))
	for i := 0; i < 5; i++ {
		mock.PutObject("media", fmt.Sprintf("doomed/obj-%04d", i), swifttest.Object{
			Bytes: 3, ETag: "aa", Data: []byte("abc"),
		})
	}
	var mu sync.Mutex
	var failed, unresolved []string
	_, err := DeletePrefix(context.Background(), session, capabilities, "media", "doomed/", Options{
		BulkSize:     10,
		OnFailure:    func(name, detail string) { mu.Lock(); failed = append(failed, name); mu.Unlock() },
		OnUnresolved: func(name, detail string) { mu.Lock(); unresolved = append(unresolved, name); mu.Unlock() },
	})
	if err == nil {
		t.Fatal("an untrusted reply must surface an error")
	}
	sort.Strings(failed)
	sort.Strings(unresolved)
	want := []string{}
	for i := 0; i < 5; i++ {
		want = append(want, fmt.Sprintf("doomed/obj-%04d", i))
	}
	if !equal(unresolved, want) {
		t.Errorf("streaming pass unresolved\n got %v\nwant %v", unresolved, want)
	}
	if !equal(failed, []string{"doomed/obj-0003"}) {
		t.Errorf("streaming pass failures = %v", failed)
	}
}

// replyShaped builds a reply with an arbitrary overall status and rows
// of arbitrary shape, so a malformed reply can be driven without the
// helper quietly correcting it.
func replyShaped(status string, deleted, notFound int, rows ...[]string) map[string]any {
	if rows == nil {
		rows = [][]string{}
	}
	return map[string]any{
		"Number Deleted": deleted, "Number Not Found": notFound,
		"Response Status": status, "Response Body": "",
		"Errors": rows,
	}
}

// TestBulkRejectsMalformedRepliesThatReconcile covers the shapes whose
// counts add up, so the malformation is the only thing wrong with them.
// Each must be untrusted, credit nothing, and leave the requested name
// unresolved rather than becoming an authoritative disposition.
func TestBulkRejectsMalformedRepliesThatReconcile(t *testing.T) {
	cases := []struct {
		label string
		reply map[string]any
	}{
		{"an overall status that is not a status",
			replyShaped("200not-a-status", 1, 0)},
		{"an overall status that is empty",
			replyShaped("", 1, 0)},
		{"a row code carrying a sign",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "+500 Internal Server Error"})},
		{"a row code with a leading zero",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "0500 Internal Server Error"})},
		{"a row code with no separator",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "500Internal"})},
		{"a row carrying a third element",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "500 Internal Server Error", "extra"})},
		{"a row path padded with spaces",
			replyShaped("200 OK", 1, 0, []string{" " + wire("media", "a") + " ", "500 Internal Server Error"})},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			session, capabilities, _, _ := craftedCluster(t, tc.reply)
			l := runNames(t, session, capabilities, []string{"a"}, Options{})
			if l.err == nil {
				t.Fatal("a malformed reply must not validate merely because its counts add up")
			}
			if l.result.Deleted != 0 || l.result.NotFound != 0 {
				t.Errorf("a malformed reply's counts were credited: %+v", l.result)
			}
			if l.result.Failed != 0 {
				t.Errorf("a malformed row became an authoritative failure: %+v", l.result.Failures)
			}
			if !equal(l.unresolved, []string{"a"}) {
				t.Errorf("unresolved = %v, want the requested name", l.unresolved)
			}
		})
	}
}

// TestBulkLedgerCountsDistinctIdentitiesAcrossPasses pins the counters
// to what they claim to be. A sweep re-lists whatever is still there,
// so a name the service keeps blaming is observed on every pass, and
// one identity must remain one record, one stream event and one count.
func TestBulkLedgerCountsDistinctIdentitiesAcrossPasses(t *testing.T) {
	session, capabilities, mock, crafted := craftedCluster(t,
		reply(1, 0, [2]string{wire("media", "doomed/b"), "500 Internal Server Error"}))
	mock.PutObject("media", "doomed/a", swifttest.Object{Bytes: 1, ETag: "aa", Data: []byte("a")})
	mock.PutObject("media", "doomed/b", swifttest.Object{Bytes: 1, ETag: "bb", Data: []byte("b")})

	var mu sync.Mutex
	var failed, unresolved []string
	result, err := DeletePrefix(context.Background(), session, capabilities, "media", "doomed/", Options{
		BulkSize:     10,
		Passes:       3,
		OnFailure:    func(name, detail string) { mu.Lock(); failed = append(failed, name); mu.Unlock() },
		OnUnresolved: func(name, detail string) { mu.Lock(); unresolved = append(unresolved, name); mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("a valid partial reply must not surface an error: %v", err)
	}
	if crafted.calls < 2 {
		t.Skipf("the sweep did not repeat (%d request), so this case proves nothing", crafted.calls)
	}
	if result.Failed != 1 || len(failed) != 1 {
		t.Errorf("Failed = %d over %d stream events, want one distinct identity across %d requests",
			result.Failed, len(failed), crafted.calls)
	}
	if result.Unresolved != 1 || len(unresolved) != 1 {
		t.Errorf("Unresolved = %d over %d stream events, want one distinct identity",
			result.Unresolved, len(unresolved))
	}
	if len(result.Failures) != 1 || len(result.Unresolveds) != 1 {
		t.Errorf("retained sets hold %d failures and %d unresolved, want one each",
			len(result.Failures), len(result.Unresolveds))
	}
}

// realSwiftSession authenticates against the owned Swift fixture, or
// skips when it is not configured. Package-level tests are otherwise
// mock-backed, and the mock cannot prove how the real middleware treats
// an encoded name.
func realSwiftSession(t *testing.T) *swiftcontrol.Session {
	t.Helper()
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		t.Skip("the owned Swift fixture is not configured")
	}
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: authURL, Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

// TestBulkDeleteEncodingRoundTripsRealSwift proves on the owned service
// that every name shape this slice claims to carry actually reaches the
// middleware as the object it names, and that exactly those objects go.
// The mock echoes what it was sent, so only the real service can settle
// the request encoding.
func TestBulkDeleteEncodingRoundTripsRealSwift(t *testing.T) {
	session := realSwiftSession(t)
	ctx := context.Background()
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.BulkDeleteMaxDeletes() <= 0 {
		t.Skip("the owned fixture does not advertise bulk delete")
	}
	container := fmt.Sprintf("aeronautes-bulkid-%d", time.Now().UnixNano())
	if _, err := session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: container}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.DeleteContainer(context.Background(), container) })

	doomed := []string{
		"plain.bin",
		"dir/with a space.bin",
		"literal%percent.bin",
		"percent-like%41.bin",
		"héllo-ünicode.bin",
		"line\nbreak.bin",
		"deep/nested/child.bin",
		"trailing-plus+and&amp.bin",
	}
	kept := "kept-neighbour.bin"
	for _, name := range append(append([]string{}, doomed...), kept) {
		if _, err := session.PutObject(ctx, container, name, strings.NewReader("payload"), "application/octet-stream", nil); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	// Every seeded name must exist under the exact spelling first,
	// otherwise the delete would be proving nothing.
	present := map[string]bool{}
	for entry, err := range session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := entry.(swiftcontrol.ObjectEntry); ok {
			present[o.Name] = true
		}
	}
	for _, name := range append(append([]string{}, doomed...), kept) {
		if !present[name] {
			t.Fatalf("the fixture did not store %q under that exact spelling", name)
		}
	}

	var mu sync.Mutex
	var failed, unresolved []string
	result, err := DeleteNames(ctx, session, capabilities, container, doomed, Options{
		OnFailure:    func(name, detail string) { mu.Lock(); failed = append(failed, name); mu.Unlock() },
		OnUnresolved: func(name, detail string) { mu.Lock(); unresolved = append(unresolved, name); mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("the owned service refused a well formed request: %v", err)
	}
	if result.Deleted != int64(len(doomed)) || result.Failed != 0 || result.Unresolved != 0 {
		t.Fatalf("result = %+v (failed %v, unresolved %v)", result, failed, unresolved)
	}

	after := map[string]bool{}
	for entry, err := range session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := entry.(swiftcontrol.ObjectEntry); ok {
			after[o.Name] = true
		}
	}
	for _, name := range doomed {
		if after[name] {
			t.Errorf("%q survived, so its request encoding did not name it", name)
		}
	}
	if !after[kept] {
		t.Errorf("the neighbour %q was destroyed by a request that never named it", kept)
	}
	if len(after) != 1 {
		t.Errorf("the container holds %d objects after the run, want only the neighbour", len(after))
	}
}
