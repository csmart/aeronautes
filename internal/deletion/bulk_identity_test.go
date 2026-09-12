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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
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
	// The canned reply credits one deletion without applying any, so the
	// second pass lists both names again; the job had already requested
	// both and sends nothing more.
	if crafted.calls != 1 {
		t.Fatalf("the sweep sent %d bulk requests, want exactly one", crafted.calls)
	}
	if result.Passes != 2 || result.NotRequestedAgain != 2 {
		t.Errorf("passes=%d notRequestedAgain=%d, want 2 and 2", result.Passes, result.NotRequestedAgain)
	}
	if result.Failed != 1 || len(failed) != 1 || result.Unresolved != 1 || len(unresolved) != 1 {
		t.Errorf("distinct identities: Failed=%d/%d Unresolved=%d/%d, want one each", result.Failed, len(failed), result.Unresolved, len(unresolved))
	}
	if len(result.Failures) != 1 || len(result.Unresolveds) != 1 {
		t.Errorf("retained sets hold %d failures and %d unresolved, want one each", len(result.Failures), len(result.Unresolveds))
	}
	if result.Remaining != 1 {
		t.Errorf("remaining=%d, want 1: names still listed after the only request keep the run partial", result.Remaining)
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
// middleware as the object it names, that exactly those objects go, and
// that a neighbouring namespace is untouched. The mock echoes what it
// was sent, so only the real service can settle the request encoding.
//
// Absence is established by an authoritative object read expecting a
// not-found, never by a listing, which is advisory.
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
	stamp := time.Now().UnixNano()
	container := fmt.Sprintf("aeronautes-bulkid-%d", stamp)
	neighbour := fmt.Sprintf("aeronautes-bulkid-%d-neighbour", stamp)
	for _, name := range []string{container, neighbour} {
		if _, err := session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}

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
	inNeighbour := "other-namespace.bin"
	seeded := append(append([]string{}, doomed...), kept)

	// Every object this test creates is removed again, in both
	// namespaces, whatever the test does in between.
	t.Cleanup(func() {
		background := context.Background()
		for _, pair := range [][2]string{{container, kept}, {neighbour, inNeighbour}} {
			for _, name := range append([]string{pair[1]}, doomed...) {
				_ = session.DeleteObject(background, pair[0], name)
			}
		}
		for _, name := range []string{container, neighbour} {
			if err := session.DeleteContainer(background, name); err != nil {
				t.Errorf("cleanup left %q behind: %v", name, err)
			}
		}
	})

	for _, name := range seeded {
		if _, err := session.PutObject(ctx, container, name, strings.NewReader("payload"), "application/octet-stream", nil); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	if _, err := session.PutObject(ctx, neighbour, inNeighbour, strings.NewReader("payload"), "application/octet-stream", nil); err != nil {
		t.Fatal(err)
	}
	// Each seeded name must be authoritatively readable under its exact
	// spelling first, or the delete would prove nothing.
	for _, name := range seeded {
		if _, err := session.ObjectDetail(ctx, container, name); err != nil {
			t.Fatalf("the fixture did not store %q under that exact spelling: %v", name, err)
		}
	}

	var mu sync.Mutex
	var failed, unresolved []string
	result, err := DeleteNames(ctx, session, capabilities, container, doomed, Options{
		// Small enough to cross a chunk boundary and complete, rather
		// than stopping at the first request.
		BulkSize:     3,
		OnFailure:    func(name, detail string) { mu.Lock(); failed = append(failed, name); mu.Unlock() },
		OnUnresolved: func(name, detail string) { mu.Lock(); unresolved = append(unresolved, name); mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("the owned service refused a well formed request: %v", err)
	}
	if result.Deleted != int64(len(doomed)) || result.Failed != 0 || result.Unresolved != 0 {
		t.Fatalf("result = %+v (failed %v, unresolved %v)", result, failed, unresolved)
	}
	if result.UntrustedReplies != 0 {
		t.Errorf("the owned service produced %d untrusted replies", result.UntrustedReplies)
	}

	// Authoritative witnesses: each named object is gone, the unnamed
	// neighbour in the same container survives, and the second namespace
	// is untouched.
	for _, name := range doomed {
		if _, err := session.ObjectDetail(ctx, container, name); err == nil {
			t.Errorf("%q survived, so its request encoding did not name it", name)
		} else if !swiftcontrol.IsNotFound(err) {
			t.Errorf("%q could not be witnessed as absent: %v", name, err)
		}
	}
	if _, err := session.ObjectDetail(ctx, container, kept); err != nil {
		t.Errorf("the neighbour %q was destroyed by a request that never named it: %v", kept, err)
	}
	if _, err := session.ObjectDetail(ctx, neighbour, inNeighbour); err != nil {
		t.Errorf("the second namespace was disturbed: %v", err)
	}
}

// TestBulkRejectsUndocumentedOutcomes covers replies whose counts and
// rows reconcile but whose overall outcome is not a bulk-delete result,
// and replies that omit a required count altogether. A missing count is
// not an observed zero.
func TestBulkRejectsUndocumentedOutcomes(t *testing.T) {
	row := [2]string{wire("media", "b"), "500 Internal Server Error"}
	cases := []struct {
		label string
		reply map[string]any
	}{
		{"an informational overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "100 Continue",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a created overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "201 Created",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a no-content overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "204 No Content",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a redirect overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "302 Found",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"no deleted count at all", map[string]any{
			"Number Not Found": 1, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a null deleted count", map[string]any{
			"Number Deleted": nil, "Number Not Found": 1, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"no not-found count at all", map[string]any{
			"Number Deleted": 1, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a null not-found count", map[string]any{
			"Number Deleted": 1, "Number Not Found": nil, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			session, capabilities, _, _ := craftedCluster(t, tc.reply)
			l := runNames(t, session, capabilities, []string{"a", "b"}, Options{})
			if l.err == nil {
				t.Fatal("an undocumented outcome must not validate")
			}
			if l.result.Deleted != 0 || l.result.NotFound != 0 {
				t.Errorf("an untrusted reply's counts were credited: %+v", l.result)
			}
			if !equal(l.unresolved, []string{"a", "b"}) {
				t.Errorf("unresolved = %v, want every requested name", l.unresolved)
			}
			if len(l.result.UntrustedAggregates) != 1 {
				t.Fatalf("the diagnostic aggregate did not survive: %+v", l.result.UntrustedAggregates)
			}
			if l.result.UntrustedAggregates[0].Requested != 2 {
				t.Errorf("the diagnostic lost the request size: %+v", l.result.UntrustedAggregates[0])
			}
			if len(l.result.UntrustedAggregates[0].Anomalies) == 0 {
				t.Error("the diagnostic carries no reason")
			}
		})
	}
}

// TestBulkRetainsMalformedRowsWhole keeps the evidence of a bad reply
// intact rather than truncating it to the documented shape.
func TestBulkRetainsMalformedRowsWhole(t *testing.T) {
	session, capabilities, _, _ := craftedCluster(t, replyShaped("200 OK", 1, 0,
		[]string{wire("media", "a"), "500 Internal Server Error", "third", "fourth"}))
	l := runNames(t, session, capabilities, []string{"a"}, Options{})
	if l.err == nil {
		t.Fatal("a malformed row must not validate")
	}
	if len(l.result.UntrustedAggregates) != 1 {
		t.Fatalf("no diagnostic retained: %+v", l.result)
	}
	rows := l.result.UntrustedAggregates[0].MalformedRows
	if len(rows) != 1 || len(rows[0]) != 4 {
		t.Fatalf("the malformed row was not retained whole: %v", rows)
	}
	if rows[0][2] != "third" || rows[0][3] != "fourth" {
		t.Errorf("the retained row lost elements: %v", rows[0])
	}
}

// swiftQuote reproduces the encoder Swift uses for the paths it returns
// in error rows: everything but the unreserved set and the separator is
// percent-encoded. It is deliberately not the production request
// encoder, because a helper that repeats the code under test proves
// nothing about the service.
func swiftQuote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		unreserved := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '/'
		if unreserved {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// replyPath renders a row path the way the service returns it.
func replyPath(container, name string) string {
	return "/" + swiftQuote(container) + "/" + swiftQuote(name)
}

// TestBulkResolvesTheServiceReplyEncoding pins the identity rule to the
// bytes a row names rather than to its spelling. The service decodes
// the line it was sent and quotes the name again more strictly, so a
// name carrying punctuation comes back spelled differently from the way
// it went out, and both spellings name the same object.
func TestBulkResolvesTheServiceReplyEncoding(t *testing.T) {
	names := []string{
		"plus+and&amp.bin",
		"colon:name.bin",
		"equals=name.bin",
		"at@name.bin",
		"dollar$name.bin",
		"comma,semi;name.bin",
		"literal%percent.bin",
		"dir/with a space.bin",
		"héllo-ünicode.bin",
	}
	for _, name := range names {
		for _, spelling := range []struct {
			label string
			path  func(string, string) string
		}{
			{"as the service returns it", replyPath},
			{"as the request sent it", func(c, n string) string { return wire(c, n) }},
		} {
			t.Run(name+" "+spelling.label, func(t *testing.T) {
				// One requested name and one valid row naming it is a
				// legitimate, reconciling reply. The point of the case is
				// that the row resolves to the requested identity whichever
				// spelling it arrives in.
				row := [2]string{spelling.path("media", name), "500 Internal Server Error"}
				session, capabilities, _, _ := craftedCluster(t, reply(0, 0, row))
				l := runNames(t, session, capabilities, []string{name}, Options{})
				if l.err != nil {
					t.Fatalf("a legitimate row was rejected: %v", l.err)
				}
				if l.result.UntrustedReplies != 0 {
					t.Errorf("a legitimate reply was treated as untrusted: %+v", l.result)
				}
				if !equal(l.failed, []string{name}) {
					t.Errorf("the row did not resolve to the requested identity: failed=%v", l.failed)
				}
				if !equal(l.unresolved, []string{name}) {
					t.Errorf("unresolved = %v, want the requested name", l.unresolved)
				}
				if !strings.Contains(l.details[name], "500") {
					t.Errorf("the reported status was lost: %q", l.details[name])
				}
			})
		}
	}
}

// TestBulkValidPartialSurvivesTheReplyEncoding proves the encoding fix
// where it matters most: a legitimate partial reply must still be
// validated and credited when its row uses the service's own spelling.
func TestBulkValidPartialSurvivesTheReplyEncoding(t *testing.T) {
	session, capabilities, _, _ := craftedCluster(t,
		reply(1, 0, [2]string{replyPath("media", "b+plus.bin"), "500 Internal Server Error"}))
	l := runNames(t, session, capabilities, []string{"a.bin", "b+plus.bin"}, Options{})
	if l.err != nil {
		t.Fatalf("a valid partial reply was rejected: %v", l.err)
	}
	if l.result.Deleted != 1 || l.result.UntrustedReplies != 0 {
		t.Errorf("the aggregate was not credited: %+v", l.result)
	}
	if !equal(l.failed, []string{"b+plus.bin"}) || !equal(l.unresolved, []string{"b+plus.bin"}) {
		t.Errorf("failed=%v unresolved=%v", l.failed, l.unresolved)
	}
}

// TestBulkRefusesRowsThatAreNotOurs keeps the resolver strict in the
// other direction: decoding is one layer, and nothing beyond a
// different spelling of a requested identity is admitted.
func TestBulkRefusesRowsThatAreNotOurs(t *testing.T) {
	cases := []struct{ label, path string }{
		{"another container", replyPath("other-container", "a b.bin")},
		{"a name never requested", replyPath("media", "never.bin")},
		{"whitespace added around the path", " " + replyPath("media", "a b.bin") + " "},
		{"malformed percent encoding", "/media/a%zz.bin"},
		{"no object part at all", "/media"},
		{"an empty object part", "/media/"},
		{"a double-encoded name", replyPath("media", swiftQuote("a b.bin"))},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			session, capabilities, _, _ := craftedCluster(t,
				reply(0, 0, [2]string{tc.path, "500 Internal Server Error"}))
			l := runNames(t, session, capabilities, []string{"a b.bin"}, Options{})
			if l.err == nil {
				t.Fatal("a row that is not ours must not validate")
			}
			if l.result.Failed != 0 {
				t.Errorf("a foreign row became a reported failure: %+v", l.result.Failures)
			}
			if !equal(l.unresolved, []string{"a b.bin"}) {
				t.Errorf("unresolved = %v, want the requested name", l.unresolved)
			}
		})
	}
}

// swiftProxy stands in front of the owned Swift fixture so one bulk
// reply can be substituted while the request is optionally forwarded
// for real. Everything else passes through untouched, and the auth
// response's storage URL is rewritten so the session keeps talking to
// the proxy rather than escaping to the fixture directly.
type swiftProxy struct {
	target  string
	self    string
	forward bool
	reply   map[string]any
	client  *http.Client
	calls   int
	sent    []byte
}

func (p *swiftProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	bulk := r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete")
	if !bulk || p.forward {
		outbound, err := http.NewRequest(r.Method, p.target+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		outbound.Header = r.Header.Clone()
		resp, err := p.client.Do(outbound)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		if !bulk {
			for key, values := range resp.Header {
				for _, v := range values {
					if strings.EqualFold(key, "X-Storage-Url") {
						v = strings.Replace(v, p.target, p.self, 1)
					}
					w.Header().Add(key, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(payload)
			return
		}
	}
	// The substituted reply, identical whether or not the request was
	// forwarded.
	p.calls++
	payload, _ := json.Marshal(p.reply)
	p.sent = payload
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

// proxiedSessionOver puts the substituting proxy in front of any
// provider, so the mock and the owned service are driven through one
// code path with byte-identical injected replies.
func proxiedSessionOver(t *testing.T, base string, forward bool, reply map[string]any) (*swiftcontrol.Session, *swiftcontrol.Capabilities, *swiftProxy) {
	t.Helper()
	proxy := &swiftProxy{target: base, forward: forward, reply: reply, client: &http.Client{}}
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)
	proxy.self = srv.URL
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	capabilities, err := session.FetchCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return session, capabilities, proxy
}

// TestBulkEffectSubstitutionMock runs the fault boundary against the
// in-process provider: the same substituted reply, once with the
// deletions really applied and once suppressed.
func TestBulkEffectSubstitutionMock(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	observer := effectObserver(t, srv.URL+"/auth/v1.0")
	runDeleteNamesEffectPair(t, srv.URL, observer)
}

// TestBulkEffectSubstitutionRealSwift runs the same boundary against
// the owned service, so the pair is not an artefact of the mock.
func TestBulkEffectSubstitutionRealSwift(t *testing.T) {
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		t.Skip("the owned Swift fixture is not configured")
	}
	observer := effectObserver(t, authURL)
	runDeleteNamesEffectPair(t, authURL[:strings.Index(authURL, "/auth/")], observer)
}

func effectObserver(t *testing.T, authURL string) *swiftcontrol.Session {
	t.Helper()
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

// witnessGone requires an authoritative not-found. A transport,
// authentication or server failure is a broken observation, not a
// witnessed deletion.
func witnessGone(t *testing.T, observer *swiftcontrol.Session, container, name string) {
	t.Helper()
	_, err := observer.ObjectDetail(context.Background(), container, name)
	switch {
	case err == nil:
		t.Errorf("%s/%s is still present, so nothing was witnessed as deleted", container, name)
	case !swiftcontrol.IsNotFound(err):
		t.Fatalf("%s/%s could not be observed, so absence is not established: %v", container, name, err)
	}
}

// witnessBytes requires the object to be present with exactly the bytes
// it was given, so a survivor is shown to have survived intact.
func witnessBytes(t *testing.T, observer *swiftcontrol.Session, container, name, want string) {
	t.Helper()
	body, _, err := observer.OpenObject(context.Background(), container, name)
	if err != nil {
		t.Fatalf("%s/%s is not present: %v", container, name, err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("%s/%s could not be read: %v", container, name, err)
	}
	if string(got) != want {
		t.Errorf("%s/%s content = %q, want %q", container, name, got, want)
	}
}

// runDeleteNamesEffectPair drives DeleteNames twice with byte-identical
// substituted replies, once with the deletions applied and once
// suppressed. The ledger must read identically in both, while an
// observer that never passes through the proxy shows the stores really
// did end in opposite states, with surviving content and the
// neighbouring namespace intact.
func runDeleteNamesEffectPair(t *testing.T, base string, observer *swiftcontrol.Session) {
	t.Helper()
	ctx := context.Background()
	container := fmt.Sprintf("aeronautes-bulksub-%d", time.Now().UnixNano())
	if _, err := observer.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: container}); err != nil {
		t.Fatal(err)
	}
	targets := []string{"substitution-target.bin", "substitution-second.bin"}
	neighbour := "substitution-neighbour.bin"
	contents := map[string]string{
		targets[0]: "target-one-payload",
		targets[1]: "target-two-payload",
		neighbour:  "neighbour-payload",
	}
	t.Cleanup(func() {
		background := context.Background()
		for name := range contents {
			_ = observer.DeleteObject(background, container, name)
		}
		if err := observer.DeleteContainer(background, container); err != nil {
			t.Errorf("cleanup left %q behind: %v", container, err)
		}
	})

	// One target is blamed by name, the other gets no disposition at all,
	// so both shapes of unresolved identity are exercised.
	reply := map[string]any{
		"Number Deleted": 0, "Number Not Found": 0,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": [][2]string{{"/" + container + "/" + targets[0], "500 Internal Server Error"}},
	}
	var ledgers []string
	var payloads [][]byte

	for _, forward := range []bool{false, true} {
		for name, body := range contents {
			if _, err := observer.PutObject(ctx, container, name, strings.NewReader(body), "application/octet-stream", nil); err != nil {
				t.Fatal(err)
			}
		}
		session, capabilities, proxy := proxiedSessionOver(t, base, forward, reply)
		var mu sync.Mutex
		var failed, unresolved []string
		result, err := DeleteNames(ctx, session, capabilities, container, targets, Options{
			OnFailure:    func(n, d string) { mu.Lock(); failed = append(failed, n); mu.Unlock() },
			OnUnresolved: func(n, d string) { mu.Lock(); unresolved = append(unresolved, n); mu.Unlock() },
		})
		sort.Strings(failed)
		sort.Strings(unresolved)
		ledgers = append(ledgers, fmt.Sprintf("err=%v deleted=%d notfound=%d failed=%v unresolved=%v untrusted=%d",
			err != nil, result.Deleted, result.NotFound, failed, unresolved, result.UntrustedReplies))
		payloads = append(payloads, proxy.sent)

		// Witnessed by a session that never passed through the proxy.
		for _, name := range targets {
			if forward {
				witnessGone(t, observer, container, name)
			} else {
				witnessBytes(t, observer, container, name, contents[name])
			}
		}
		// Collateral: the neighbour keeps its bytes in both worlds.
		witnessBytes(t, observer, container, neighbour, contents[neighbour])
	}

	if !bytes.Equal(payloads[0], payloads[1]) {
		t.Fatal("the two runs must be driven by identical reply bytes")
	}
	if ledgers[0] != ledgers[1] {
		t.Errorf("the same reply produced different ledgers:\n suppressed %s\n applied    %s", ledgers[0], ledgers[1])
	}
	if !strings.Contains(ledgers[0], "unresolved=["+targets[1]+" "+targets[0]+"]") &&
		!strings.Contains(ledgers[0], "unresolved=["+targets[0]+" "+targets[1]+"]") {
		t.Errorf("both requested identities must be unresolved: %s", ledgers[0])
	}
}
