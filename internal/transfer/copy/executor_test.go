package copy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// world is a two-account mock cluster with adapter sessions.
type world struct {
	srcMock, dstMock *swifttest.Server
	src, dst         *swiftcontrol.Session
}

func newWorld(t *testing.T) *world {
	return newWorldWrappingDest(t, func(h http.Handler) http.Handler { return h })
}

// newWorldWrappingDest puts a handler of the caller's choosing in front of the
// DESTINATION mock, so a case can see or change the requests this executor
// makes without reaching inside it.
func newWorldWrappingDest(t *testing.T, wrap func(http.Handler) http.Handler) *world {
	t.Helper()
	w := &world{}
	w.srcMock = swifttest.New("test:tester", "testing")
	w.dstMock = swifttest.New("test:tester", "testing")
	srcSrv := httptest.NewServer(w.srcMock)
	t.Cleanup(srcSrv.Close)
	dstSrv := httptest.NewServer(wrap(w.dstMock))
	t.Cleanup(dstSrv.Close)
	w.src = authenticateMock(t, srcSrv)
	w.dst = authenticateMock(t, dstSrv)
	w.srcMock.AddContainer("media", "standard")
	w.dstMock.AddContainer("backup", "standard")
	return w
}

// authenticateMock opens an adapter session against one mock server.
func authenticateMock(t *testing.T, srv *httptest.Server) *swiftcontrol.Session {
	t.Helper()
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func seed(mock *swifttest.Server, container, name string, data []byte, meta map[string]string) {
	mock.PutObject(container, name, swifttest.Object{
		Bytes: int64(len(data)), ETag: md5hex(string(data)),
		ContentType: "text/plain", Meta: meta, Data: data,
	})
}

// lineEngine stands in for the pinned engine as it was measured copying
// store to store (docs/rclone.md). It writes each listed name under the
// destination path it is given, with the content type and a modification
// time and no other header; it writes an object larger than chunk in
// segments behind a dynamic manifest and says where; it prints one line for
// each object it wrote; and a listed name that is not at the source gets no
// line and no error. It writes into the destination mock directly, so that
// mock's request log holds only what the application itself sent.
type lineEngine struct {
	w     *world
	chunk int64
	// refuse makes the engine report this error for a name and write nothing.
	refuse map[string]string
	// exit is the child's exit code.
	exit int
	// quiet stops it reporting where it puts segments.
	quiet bool
	// gone are listed names that are no longer at the source when it runs.
	gone map[string]bool
	// during runs once the batch is written, before the child returns.
	during func()
	// calls are the source and destination paths and the names of each run.
	calls []string
}

func parseSpec(spec string) (container, prefix string) {
	path := spec[strings.Index(spec, ":")+1:]
	if slash := strings.Index(path, "/"); slash >= 0 {
		return path[:slash], path[slash+1:] + "/"
	}
	return path, ""
}

func (f *lineEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	f.calls = append(f.calls, src+" -> "+dst+" "+strings.Join(names, ","))
	srcC, srcP := parseSpec(src)
	dstC, dstP := parseSpec(dst)
	result := &rclone.Result{ExitCode: f.exit}
	for _, name := range names {
		if msg := f.refuse[name]; msg != "" {
			onEvent(rclone.Event{Level: "error", Object: name, Msg: msg})
			result.Errors = append(result.Errors, msg)
			continue
		}
		if f.gone[name] {
			continue // measured: no line, and the child still exits 0
		}
		body, source, err := f.w.src.OpenObject(ctx, srcC, srcP+name)
		if err != nil {
			return result, err
		}
		data, err := io.ReadAll(body)
		if err := errors.Join(err, body.Close()); err != nil {
			return result, err
		}
		// The source's own modification time when it states one, else the
		// time the source was last changed.
		mtime := map[string]string{"Mtime": source.Meta["Mtime"]}
		if mtime["Mtime"] == "" {
			mtime["Mtime"] = "1756400000"
		}
		object := swifttest.Object{Bytes: int64(len(data)), ETag: md5hex(string(data)), ContentType: source.ContentType,
			Meta: mtime, Data: data}
		if f.chunk > 0 && int64(len(data)) > f.chunk {
			prefix := dstP + name + "/2026-11-16-000000-run/"
			for i := 0; int64(i)*f.chunk < int64(len(data)); i++ {
				segment := data[int64(i)*f.chunk : min(int64(i+1)*f.chunk, int64(len(data)))]
				segmentName := fmt.Sprintf("%s%08d", prefix, i)
				f.w.dstMock.PutObject(dstC+"_segments", segmentName, swifttest.Object{
					Bytes: int64(len(segment)), ETag: md5hex(string(segment)), Data: segment})
				if !f.quiet {
					onEvent(rclone.Event{Level: "debug", Object: name,
						Msg: fmt.Sprintf("Uploading segment file %q into %q", segmentName, dstC+"_segments")})
				}
			}
			object = swifttest.Object{ContentType: source.ContentType, ETag: md5hex(""),
				Manifest: dstC + "_segments/" + strings.TrimSuffix(prefix, "/"), Meta: mtime}
		}
		f.w.dstMock.PutObject(dstC, dstP+name, object)
		onEvent(rclone.Event{Level: "info", Object: name, Msg: "Copied (new)"})
	}
	if f.during != nil {
		f.during()
	}
	return result, nil
}

// records collects what the executor recorded for each name.
type records struct {
	executed map[string]Observation
	verified map[string]bool
	admitted []string
}

func (r *records) recorder() *Recorder {
	r.executed, r.verified = map[string]Observation{}, map[string]bool{}
	return &Recorder{
		Admit:   func(a Admission) error { r.admitted = append(r.admitted, a.Item.Name); return nil },
		Execute: func(name string, o Observation) error { r.executed[name] = o; return nil },
		Verify:  func(name string, o Observation) error { r.verified[name] = o.Result == ResultVerified; return nil },
	}
}

func newExecutor(t *testing.T, w *world, engine Engine, change ...func(*Config)) *Executor {
	t.Helper()
	cfg := Config{
		Source: w.src, Dest: w.dst, Engine: engine,
		SrcSpec: "aeronautes_src:media", DstSpec: "aeronautes_dst:backup",
		SrcContainer: "media", DstContainer: "backup", ChunkBytes: 8,
	}
	for _, apply := range change {
		apply(&cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func headDst(t *testing.T, w *world, name string) *swiftcontrol.ObjectDetail {
	t.Helper()
	d, err := w.dst.ObjectDetail(context.Background(), "backup", name)
	if err != nil {
		t.Fatalf("HEAD backup/%s: %v", name, err)
	}
	return d
}

func readDst(t *testing.T, w *world, name string) string {
	t.Helper()
	body, _, err := w.dst.OpenObject(context.Background(), "backup", name)
	if err != nil {
		t.Fatalf("GET backup/%s: %v", name, err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("GET backup/%s: %v", name, err)
	}
	return string(data)
}

func newItem(name string) plan.Item {
	return plan.Item{Name: name, Verdict: plan.VerdictNew, Route: plan.RouteRclone}
}

// ADR 0122 decision 4 and its required proof: a copy writes each object at
// its name. The engine is given the destination folder itself, the
// application asks the destination nothing about a name before its write or
// after it, and what the engine said is each object's outcome and its record.
func TestCopyWritesEachObjectAtItsNameAndAsksTheDestinationNothing(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "from/a.txt", []byte("alpha"), nil)
	seed(w.srcMock, "media", "from/sub/b.txt", []byte("beta"), map[string]string{"Mtime": "1700000000.5"})
	engine := &lineEngine{w: w}
	var kept records
	e := newExecutor(t, w, engine, func(c *Config) {
		c.SrcPrefix, c.DstPrefix, c.Recorder = "from/", "to/", kept.recorder()
	})
	mark := len(w.dstMock.RequestLog())
	report, err := e.Run(t.Context(), []plan.Item{newItem("a.txt"), newItem("sub/b.txt")})
	sent := w.dstMock.RequestLog()[mark:]
	if err != nil || report.Copied != 2 || report.Verified != 2 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if want := []string{"aeronautes_src:media/from -> aeronautes_dst:backup/to a.txt,sub/b.txt"}; !reflect.DeepEqual(engine.calls, want) {
		t.Fatalf("engine runs = %q, want %q", engine.calls, want)
	}
	if len(sent) != 0 {
		t.Fatalf("the application sent the destination %q, want nothing: the engine writes, and no name is read around its write", sent)
	}
	if readDst(t, w, "to/a.txt") != "alpha" || readDst(t, w, "to/sub/b.txt") != "beta" {
		t.Fatal("the objects are not at their names")
	}
	for _, name := range []string{"a.txt", "sub/b.txt"} {
		if got := kept.executed[name]; got.Result != ResultCopied || got.Effect != EffectReportedApplied || !kept.verified[name] {
			t.Fatalf("%s recorded %+v, second record %v", name, got, kept.verified[name])
		}
	}
}

// A name the two listings show as the same is skipped with no request to
// either store; no stamp is written, and none is read.
func TestANameTheListingsShowAsTheSameIsSkippedWithNoRequest(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "a.txt", []byte("alpha"), nil)
	seed(w.dstMock, "backup", "a.txt", []byte("alpha"), map[string]string{"Kept": "yes"})
	engine := &lineEngine{w: w}
	var kept records
	e := newExecutor(t, w, engine, func(c *Config) { c.Recorder = kept.recorder() })
	srcMark, dstMark := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
	report, err := e.Run(t.Context(), []plan.Item{{Name: "a.txt", Verdict: plan.VerdictSame}})
	if err != nil || report.SkippedEqual != 1 || report.Copied != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if src, dst := w.srcMock.RequestLog()[srcMark:], w.dstMock.RequestLog()[dstMark:]; len(src)+len(dst) != 0 || len(engine.calls) != 0 {
		t.Fatalf("a skipped name cost requests: source %q, destination %q, engine %q", src, dst, engine.calls)
	}
	if got := kept.executed["a.txt"]; got.Result != ResultSame || !kept.verified["a.txt"] {
		t.Fatalf("recorded %+v, second record %v", got, kept.verified["a.txt"])
	}
	if headDst(t, w, "a.txt").Meta["Kept"] != "yes" {
		t.Fatal("the skipped object was written")
	}
}

// A pair the listings cannot compare, an empty row or a large object, is read
// once on each side, and the store's description of the two decides: the same
// length and tag are the same content, a name that is gone is a first copy,
// and anything else is a replacement, refused unless it was confirmed.
func TestAPairTheListingsCannotCompareIsReadOnceOnEachSide(t *testing.T) {
	big := "0123456789abcdefghij"
	sameCut := swifttest.Object{Manifest: "backup_segments/big/", ETag: md5hex("")}
	for _, tc := range []struct {
		name        string
		source      string
		destination func(w *world)
		replace     bool
		result      Result
		wrote       bool
	}{
		{name: "two empty objects", source: "", destination: func(w *world) { seed(w.dstMock, "backup", "x", nil, nil) }, result: ResultSame},
		{name: "a destination that is gone", source: "", destination: func(*world) {}, result: ResultCopied, wrote: true},
		{name: "a large object cut at the same places", source: big, destination: func(w *world) {
			seed(w.dstMock, "backup_segments", "big/00000000", []byte(big[:8]), nil)
			seed(w.dstMock, "backup_segments", "big/00000001", []byte(big[8:16]), nil)
			seed(w.dstMock, "backup_segments", "big/00000002", []byte(big[16:]), nil)
			w.dstMock.PutObject("backup", "x", sameCut)
		}, result: ResultSame},
		{name: "a link to the same content, which is never the same as anything", source: "link", destination: func(w *world) {
			seed(w.dstMock, "backup", "target.txt", []byte("alpha"), nil)
			w.dstMock.PutObject("backup", "x", swifttest.Object{SymlinkTarget: "backup/target.txt"})
		}, result: ResultRefused},
		{name: "a large object cut elsewhere, not confirmed", source: big, destination: func(w *world) {
			seed(w.dstMock, "backup", "x", []byte(big), nil)
		}, result: ResultRefused},
		{name: "a large object cut elsewhere, confirmed", source: big, replace: true, destination: func(w *world) {
			seed(w.dstMock, "backup", "x", []byte(big), nil)
		}, result: ResultCopied, wrote: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.srcMock.AddContainer("media_segments", "standard")
			w.dstMock.AddContainer("backup_segments", "standard")
			switch tc.source {
			case "":
				seed(w.srcMock, "media", "x", nil, nil)
			case "link":
				seed(w.srcMock, "media", "target.txt", []byte("alpha"), nil)
				w.srcMock.PutObject("media", "x", swifttest.Object{SymlinkTarget: "media/target.txt"})
			default:
				seed(w.srcMock, "media_segments", "x/00000000", []byte(tc.source[:8]), nil)
				seed(w.srcMock, "media_segments", "x/00000001", []byte(tc.source[8:16]), nil)
				seed(w.srcMock, "media_segments", "x/00000002", []byte(tc.source[16:]), nil)
				w.srcMock.PutObject("media", "x", swifttest.Object{Manifest: "media_segments/x/", ETag: md5hex("")})
			}
			tc.destination(w)
			engine := &lineEngine{w: w, chunk: 8}
			var kept records
			e := newExecutor(t, w, engine, func(c *Config) { c.Replace, c.Recorder = tc.replace, kept.recorder() })
			srcMark, dstMark := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
			report, err := e.Run(t.Context(), []plan.Item{{Name: "x", Verdict: plan.VerdictUnknown, Route: plan.RouteRclone}})
			if err != nil {
				t.Fatalf("report = %+v, %v", report, err)
			}
			if got := kept.executed["x"].Result; got != tc.result {
				t.Fatalf("result = %s, want %s (report %+v)", got, tc.result, report)
			}
			if (len(engine.calls) == 1) != tc.wrote {
				t.Fatalf("engine runs = %q, want a run: %v", engine.calls, tc.wrote)
			}
			if heads := count(w.dstMock.RequestLog()[dstMark:], "HEAD /v1/AUTH_test/backup/x"); heads != 1 {
				t.Fatalf("the destination was read %d times, want once: %q", heads, w.dstMock.RequestLog()[dstMark:])
			}
			// The engine's own reads of the source are in this log too, so
			// only the application's HEAD is counted.
			if heads := count(w.srcMock.RequestLog()[srcMark:], "HEAD /v1/AUTH_test/media/x"); heads != 1 {
				t.Fatalf("the source was read %d times by the application, want once: %q", heads, w.srcMock.RequestLog()[srcMark:])
			}
		})
	}
}

func count(log []string, prefix string) int {
	n := 0
	for _, line := range log {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// A name the listings show as different is replaced only when the operator
// confirmed replacing (ADR 0122 decision 2). Refused, it is left as it was
// and nothing is sent for it; confirmed, the engine writes over it.
func TestAChangedNameIsReplacedOnlyWhenReplacingWasConfirmed(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "not confirmed", true: "confirmed"}[replace], func(t *testing.T) {
			w := newWorld(t)
			seed(w.srcMock, "media", "a.txt", []byte("new content"), nil)
			seed(w.dstMock, "backup", "a.txt", []byte("old"), nil)
			engine := &lineEngine{w: w}
			var kept records
			e := newExecutor(t, w, engine, func(c *Config) { c.Replace, c.Recorder = replace, kept.recorder() })
			mark := len(w.dstMock.RequestLog())
			report, err := e.Run(t.Context(), []plan.Item{{Name: "a.txt", Verdict: plan.VerdictChanged, Route: plan.RouteRclone}})
			sent := w.dstMock.RequestLog()[mark:]
			if err != nil || len(sent) != 0 {
				t.Fatalf("report = %+v, %v; the application sent the destination %q", report, err, sent)
			}
			if !replace {
				if report.OverwritesRefused != 1 || report.Copied != 0 || len(engine.calls) != 0 || kept.executed["a.txt"].Result != ResultRefused {
					t.Fatalf("report = %+v, engine %q, record %+v", report, engine.calls, kept.executed["a.txt"])
				}
				if readDst(t, w, "a.txt") != "old" {
					t.Fatal("a refused name was written")
				}
				return
			}
			if report.Copied != 1 || report.Overwrites != 1 || readDst(t, w, "a.txt") != "new content" {
				t.Fatalf("report = %+v, destination %q", report, readDst(t, w, "a.txt"))
			}
		})
	}
}

// ADR 0122 decision 7, measured on the pinned engine: an object's outcome is
// what the engine said about it. Its line is the object written. Its error
// line is the object failed with that error. Nothing said is unknown when the
// engine itself failed, and not written when it ended cleanly, which is what
// a listed name that is no longer at the source gets.
func TestAnObjectsOutcomeIsWhatTheEngineSaid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		engine func(w *world) *lineEngine
		result Result
		effect Effect
		// says is in the failure the job reports, and never is not.
		says, never string
	}{
		{name: "its line", engine: func(w *world) *lineEngine { return &lineEngine{w: w} }, result: ResultCopied, effect: EffectReportedApplied},
		{name: "an error line", engine: func(w *world) *lineEngine {
			return &lineEngine{w: w, exit: 5, refuse: map[string]string{"a.txt": "Failed to copy: unexpected EOF"}}
		}, result: ResultFailed, effect: EffectUnresolved, says: "Failed to copy: unexpected EOF", never: "without reporting this object"},
		{name: "nothing, and the engine failed", engine: func(w *world) *lineEngine {
			return &lineEngine{w: w, exit: 3, gone: map[string]bool{"a.txt": true}}
		}, result: ResultFailed, effect: EffectUnresolved, says: "the engine stopped without reporting this object"},
		{name: "nothing, and the engine ended cleanly", engine: func(w *world) *lineEngine {
			return &lineEngine{w: w, gone: map[string]bool{"a.txt": true}}
		}, result: ResultFailed, effect: EffectNotAttempted, says: "it may no longer be at the source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			seed(w.srcMock, "media", "a.txt", []byte("alpha"), nil)
			var kept records
			e := newExecutor(t, w, tc.engine(w), func(c *Config) { c.Recorder = kept.recorder() })
			report, _ := e.Run(t.Context(), []plan.Item{newItem("a.txt")})
			got := kept.executed["a.txt"]
			if got.Result != tc.result || got.Effect != tc.effect {
				t.Fatalf("recorded %+v, want %s with effect %s (report %+v)", got, tc.result, tc.effect, report)
			}
			if tc.says != "" && (len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err, tc.says) ||
				tc.never != "" && strings.Contains(report.Failures[0].Err, tc.never)) {
				t.Fatalf("failures = %+v, want one that says %q and not %q", report.Failures, tc.says, tc.never)
			}
			if kept.verified["a.txt"] != (tc.result == ResultCopied) {
				t.Fatalf("second record = %v for a %s object", kept.verified["a.txt"], tc.result)
			}
		})
	}
}

// Measured on the pinned engine: it copies the content type and the source's
// modification time, and no other header. The application gives the
// destination the source's set in one POST after the engine wrote the object,
// and sends none where the engine already delivered all of it.
func TestACopyCarriesTheHeadersTheEngineDoesNot(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "plain.txt", []byte("alpha"), nil)
	seed(w.srcMock, "media", "timed.txt", []byte("beta"), map[string]string{"Mtime": "1700000000.5"})
	w.srcMock.PutObject("media", "tagged.txt", swifttest.Object{Bytes: 5, ETag: md5hex("gamma"), ContentType: "text/plain",
		Meta: map[string]string{"Colour": "blue", "Mtime": "1700000000.5"}, DeleteAt: 4102444800, Data: []byte("gamma")})
	seed(w.srcMock, "media", "untimed.txt", []byte("delta"), map[string]string{"Colour": "red"})
	e := newExecutor(t, w, &lineEngine{w: w})
	mark := len(w.dstMock.RequestLog())
	report, err := e.Run(t.Context(), []plan.Item{newItem("plain.txt"), newItem("tagged.txt"), newItem("timed.txt"), newItem("untimed.txt")})
	sent := w.dstMock.RequestLog()[mark:]
	if err != nil || report.Copied != 4 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if want := []string{"POST /v1/AUTH_test/backup/tagged.txt", "POST /v1/AUTH_test/backup/untimed.txt"}; !reflect.DeepEqual(sent, want) {
		t.Fatalf("the application sent the destination %q, want %q", sent, want)
	}
	got := headDst(t, w, "tagged.txt")
	if want := map[string]string{"Colour": "blue", "Mtime": "1700000000.5"}; !reflect.DeepEqual(got.Meta, want) || got.DeleteAt.Unix() != 4102444800 || got.ContentType != "text/plain" {
		t.Fatalf("destination carries %v, expiry %v, type %q; want the source's %v and its expiry", got.Meta, got.DeleteAt, got.ContentType, want)
	}
	// The POST replaces the destination's whole set, so for a source that
	// states no modification time it repeats the one the engine wrote: the
	// time the source was last changed, in whole seconds.
	source, err := w.src.ObjectDetail(t.Context(), "media", "untimed.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"Colour": "red", "Mtime": strconv.FormatInt(source.LastModified.Unix(), 10)}; !reflect.DeepEqual(headDst(t, w, "untimed.txt").Meta, want) {
		t.Fatalf("destination carries %v, want %v", headDst(t, w, "untimed.txt").Meta, want)
	}
}

// A POST to a dynamic manifest that does not repeat its manifest header
// empties the object (measured on the owned Swift, and the mock does the
// same). So the POST for an object the engine wrote in segments names the
// path the engine reported, and an object too large to be anything but
// segmented, whose path the engine did not report, is sent no POST: it is
// reported failed for its metadata and stays the whole object it is.
func TestCarryingHeadersToASegmentedObjectKeepsItsManifest(t *testing.T) {
	const content = "0123456789abcdefghij"
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "the engine said where the segments are", true: "the engine did not say"}[quiet], func(t *testing.T) {
			w := newWorld(t)
			w.dstMock.AddContainer("backup_segments", "standard")
			seed(w.srcMock, "media", "big.bin", []byte(content), map[string]string{"Colour": "blue"})
			e := newExecutor(t, w, &lineEngine{w: w, chunk: 8, quiet: quiet})
			mark := len(w.dstMock.RequestLog())
			report, err := e.Run(t.Context(), []plan.Item{newItem("big.bin")})
			posts := count(w.dstMock.RequestLog()[mark:], "POST ")
			got := headDst(t, w, "big.bin")
			if err != nil || got.Class != swiftcontrol.ClassDLO || readDst(t, w, "big.bin") != content {
				t.Fatalf("destination is %s holding %q, %v; want the dynamic object whole", got.Class, readDst(t, w, "big.bin"), err)
			}
			if quiet {
				if posts != 0 || report.Copied != 0 || len(report.Failures) != 1 || report.Failures[0].Stage != "metadata" {
					t.Fatalf("POSTs = %d, report = %+v; want no POST and the object failed for its metadata", posts, report)
				}
				return
			}
			if posts != 1 || report.Copied != 1 || got.Meta["Colour"] != "blue" {
				t.Fatalf("POSTs = %d, report = %+v, metadata %v", posts, report, got.Meta)
			}
		})
	}
}

// An object the engine wrote gets its headers even when the job was
// cancelled while the engine ran, so that a cancel leaves no copied object
// without them. A POST the store refuses fails the object with that reason.
func TestHeadersAreCarriedAfterACancelAndARefusalFailsTheObject(t *testing.T) {
	t.Run("cancelled while the engine ran", func(t *testing.T) {
		w := newWorld(t)
		seed(w.srcMock, "media", "tagged.txt", []byte("gamma"), map[string]string{"Colour": "blue"})
		ctx, cancel := context.WithCancel(t.Context())
		e := newExecutor(t, w, &lineEngine{w: w, during: cancel})
		report, err := e.Run(ctx, []plan.Item{newItem("tagged.txt")})
		if !errors.Is(err, context.Canceled) || report.Copied != 1 || headDst(t, w, "tagged.txt").Meta["Colour"] != "blue" {
			t.Fatalf("report = %+v, %v, metadata %v", report, err, headDst(t, w, "tagged.txt").Meta)
		}
	})
	t.Run("refused by the store", func(t *testing.T) {
		w := newWorld(t)
		seed(w.srcMock, "media", "tagged.txt", []byte("gamma"), map[string]string{"Colour": "blue"})
		w.dstMock.SetObjectPOSTStatus(http.StatusForbidden)
		var kept records
		e := newExecutor(t, w, &lineEngine{w: w}, func(c *Config) { c.Recorder = kept.recorder() })
		report, err := e.Run(t.Context(), []plan.Item{newItem("tagged.txt")})
		if err != nil || report.Copied != 0 || len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err, "its metadata was not set") {
			t.Fatalf("report = %+v, %v", report, err)
		}
		if got := kept.executed["tagged.txt"]; got.Result != ResultFailed || got.Effect != EffectUnresolved || kept.verified["tagged.txt"] {
			t.Fatalf("recorded %+v, second record %v", got, kept.verified["tagged.txt"])
		}
	})
}

// A name the engine cannot carry is read and written by the application in
// one pass: one GET, and one PUT that declares the source's entity tag,
// carries its headers and sends no condition. Nothing is read afterwards, and
// a body the store refuses against the tag leaves the name as it was.
func TestTheApplicationsOwnCopyIsOneReadAndOneWrite(t *testing.T) {
	const name = "line\n/object"
	var puts []string
	w := newWorldWrappingDest(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/AUTH_test/backup/") {
				puts = append(puts, "etag="+r.Header.Get("ETag")+" if-none-match="+r.Header.Get("If-None-Match")+" colour="+r.Header.Get("X-Object-Meta-Colour"))
			}
			next.ServeHTTP(rw, r)
		})
	})
	seed(w.srcMock, "media", name, []byte("alpha"), map[string]string{"Colour": "blue"})
	engine := &lineEngine{w: w}
	e := newExecutor(t, w, engine)
	srcMark, dstMark := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
	report, err := e.Run(t.Context(), []plan.Item{{Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative}})
	if err != nil || report.Copied != 1 || report.NativeRouted != 1 || len(engine.calls) != 0 {
		t.Fatalf("report = %+v, %v, engine %q", report, err, engine.calls)
	}
	if src, dst := w.srcMock.RequestLog()[srcMark:], w.dstMock.RequestLog()[dstMark:]; len(src) != 1 || !strings.HasPrefix(src[0], "GET ") || len(dst) != 1 || !strings.HasPrefix(dst[0], "PUT ") {
		t.Fatalf("requests: source %q, destination %q; want one GET and one PUT", src, dst)
	}
	if want := []string{"etag=" + md5hex("alpha") + " if-none-match= colour=blue"}; !reflect.DeepEqual(puts, want) {
		t.Fatalf("the PUT carried %q, want %q", puts, want)
	}
	if readDst(t, w, name) != "alpha" {
		t.Fatal("the name does not hold the object")
	}
}

func TestTheApplicationsOwnCopyFailsWhenTheStoreRefusesTheBytes(t *testing.T) {
	const name = "line\n/object"
	w := newWorld(t)
	// A source whose stated tag is not the MD5 of what it serves: the store
	// refuses the declared tag against the bytes that arrive.
	w.srcMock.PutObject("media", name, swifttest.Object{Bytes: 5, ETag: md5hex("other"), ContentType: "text/plain", Data: []byte("alpha")})
	seed(w.dstMock, "backup", name, []byte("old"), nil)
	var kept records
	e := newExecutor(t, w, nil, func(c *Config) { c.Replace, c.Recorder = true, kept.recorder() })
	report, err := e.Run(t.Context(), []plan.Item{{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative}})
	if err != nil || report.Copied != 0 || len(report.Failures) != 1 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if got := kept.executed[name]; got.Result != ResultFailed || got.Effect != EffectNotAttempted {
		t.Fatalf("recorded %+v, want a refusal that wrote nothing", got)
	}
	if readDst(t, w, name) != "old" {
		t.Fatal("the refused write changed the name")
	}
}

// The engine copies a link as its target's bytes (measured), so a link is
// found by the read of the source that precedes the engine and is carried by
// the application as a link, in one PUT with nothing read after it. A link
// the store does not follow on a plain read, such as one into another
// account, is recognised by its own header and carried the same way.
func TestALinkIsCarriedAsALinkAndNeverGivenToTheEngine(t *testing.T) {
	for _, tc := range []struct {
		name          string
		link          swifttest.Object
		route         plan.Route
		wantedAccount string
	}{
		{name: "a link the store follows", route: plan.RouteRclone,
			link: swifttest.Object{SymlinkTarget: "media/target.txt", ContentType: "application/symlink", Meta: map[string]string{"Colour": "blue"}}},
		{name: "a link into another account, on the application's own route", route: plan.RouteNative, wantedAccount: "AUTH_other",
			link: swifttest.Object{SymlinkTarget: "media/target.txt", SymlinkTargetAccount: "AUTH_other", ContentType: "application/symlink", Meta: map[string]string{"Colour": "blue"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			seed(w.srcMock, "media", "target.txt", []byte("alpha"), nil)
			w.srcMock.PutObject("media", "link", tc.link)
			engine := &lineEngine{w: w}
			e := newExecutor(t, w, engine)
			mark := len(w.dstMock.RequestLog())
			report, err := e.Run(t.Context(), []plan.Item{{Name: "link", Verdict: plan.VerdictNew, Route: tc.route}})
			sent := w.dstMock.RequestLog()[mark:]
			if err != nil || report.Copied != 1 || len(engine.calls) != 0 {
				t.Fatalf("report = %+v, %v, engine %q", report, err, engine.calls)
			}
			if len(sent) != 1 || !strings.HasPrefix(sent[0], "PUT /v1/AUTH_test/backup/link") {
				t.Fatalf("the application sent the destination %q, want one PUT", sent)
			}
			link, err := w.dst.SymlinkGet(t.Context(), "backup", "link")
			if err != nil || link.SymlinkTarget != "media/target.txt" || link.SymlinkTargetAccount != tc.wantedAccount || link.Meta["Colour"] != "blue" {
				t.Fatalf("destination link = %+v, %v", link, err)
			}
		})
	}
}

// A copy parks only at the head of an item and the head of an engine batch
// (ADR 0104). A batch that is never dispatched, because the job was cancelled
// first, is recorded as not attempted and the engine is not started.
func TestABatchThatIsNotDispatchedIsRecordedAsNotAttempted(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "a.txt", []byte("alpha"), nil)
	engine := &lineEngine{w: w}
	var kept records
	e := newExecutor(t, w, engine, func(c *Config) { c.Recorder = kept.recorder() })
	ctx, cancel := context.WithCancel(t.Context())
	if err := e.Handle(ctx, newItem("a.txt")); err != nil {
		t.Fatal(err)
	}
	cancel()
	report, err := e.Finish(ctx)
	if !errors.Is(err, context.Canceled) || len(engine.calls) != 0 || report.Copied != 0 {
		t.Fatalf("report = %+v, %v, engine %q", report, err, engine.calls)
	}
	if got := kept.executed["a.txt"]; got.Result != ResultNotAttempted || got.Effect != EffectNotAttempted {
		t.Fatalf("recorded %+v, want not attempted", got)
	}
}

// A recorded run needs all three records, and a record that cannot be kept
// stops the run: nothing further is dispatched.
func TestARecordThatCannotBeKeptStopsTheRun(t *testing.T) {
	w := newWorld(t)
	for _, partial := range []*Recorder{
		{Execute: func(string, Observation) error { return nil }, Verify: func(string, Observation) error { return nil }},
		{Admit: func(Admission) error { return nil }, Verify: func(string, Observation) error { return nil }},
		{Admit: func(Admission) error { return nil }, Execute: func(string, Observation) error { return nil }},
	} {
		if _, err := New(Config{Source: w.src, Dest: w.dst, SrcContainer: "media", DstContainer: "backup", Recorder: partial}); err == nil {
			t.Fatalf("a recorder missing a record was accepted: %+v", partial)
		}
	}
	seed(w.srcMock, "media", "a.txt", []byte("alpha"), nil)
	seed(w.srcMock, "media", "b.txt", []byte("beta"), nil)
	lost := errors.New("the job store is gone")
	for _, tc := range []struct {
		name               string
		admit, exec, check error
		runs               int
	}{
		{name: "the admission", admit: lost, runs: 0},
		{name: "the outcome", exec: lost, runs: 1},
		{name: "the second record", check: lost, runs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &lineEngine{w: w}
			recorder := &Recorder{
				Admit:   func(Admission) error { return tc.admit },
				Execute: func(string, Observation) error { return tc.exec },
				Verify:  func(string, Observation) error { return tc.check },
			}
			e := newExecutor(t, w, engine, func(c *Config) { c.Recorder, c.BatchSize = recorder, 1 })
			_, err := e.Run(t.Context(), []plan.Item{newItem("a.txt"), newItem("b.txt")})
			if !errors.Is(err, lost) || len(engine.calls) != tc.runs {
				t.Fatalf("run ended %v after %d engine runs, want the lost record and %d", err, len(engine.calls), tc.runs)
			}
		})
	}
}

// Where the engine put an object's segments is kept as the engine reports it
// (ADR 0122 decision 8), and a record of it that cannot be kept stops the run.
// The engine has had the whole batch by then, so every object of it still
// gets its outcome: both are written, and both are recorded as written.
func TestTheEnginesSegmentPathsAreKept(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "kept", true: "cannot be kept"}[fails], func(t *testing.T) {
			w := newWorld(t)
			w.dstMock.AddContainer("backup_segments", "standard")
			seed(w.srcMock, "media", "a.bin", []byte("0123456789abcdefghij"), nil)
			seed(w.srcMock, "media", "b.bin", []byte("ABCDEFGHIJ0123456789"), nil)
			var paths []string
			var kept records
			lost := errors.New("the recovery store is gone")
			e := newExecutor(t, w, &lineEngine{w: w, chunk: 8}, func(c *Config) {
				c.Recorder = kept.recorder()
				c.OnRecovery = func(kind, container, prefix, object string) error {
					paths = append(paths, kind+" "+container+"/"+prefix)
					if fails {
						return lost
					}
					return nil
				}
			})
			report, err := e.Run(t.Context(), []plan.Item{newItem("a.bin"), newItem("b.bin")})
			// A run that is stopped asks for no further record of a path.
			want := []string{"captured backup_segments/a.bin/2026-11-16-000000-run/", "captured backup_segments/b.bin/2026-11-16-000000-run/"}
			if fails {
				want = want[:1]
			}
			if !reflect.DeepEqual(paths, want) || len(report.CapturedPrefixes) != 2 {
				t.Fatalf("kept %q and reported %+v, want %q", paths, report.CapturedPrefixes, want)
			}
			if fails != errors.Is(err, lost) {
				t.Fatalf("run ended %v", err)
			}
			for _, name := range []string{"a.bin", "b.bin"} {
				if got := kept.executed[name]; got.Result != ResultCopied || !kept.verified[name] {
					t.Fatalf("%s recorded %+v, second record %v; want every object the engine wrote recorded as written", name, got, kept.verified[name])
				}
			}
			if report.Copied != 2 {
				t.Fatalf("report = %+v, want both objects counted", report)
			}
		})
	}
}

// One failure that looks like a rejected token gets one fresh child, after
// the tokens are invalidated; the second failure is the batch's outcome.
func TestARejectedTokenGetsOneFreshChild(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "a.txt", []byte("alpha"), nil)
	engine := &lineEngine{w: w, exit: 1, refuse: map[string]string{"a.txt": "401 Unauthorized"}}
	reauthenticated := 0
	e := newExecutor(t, w, engine, func(c *Config) {
		c.OnAuthFailure = func() {
			reauthenticated++
			engine.exit, engine.refuse = 0, nil
		}
	})
	report, err := e.Run(t.Context(), []plan.Item{newItem("a.txt")})
	if err != nil || reauthenticated != 1 || len(engine.calls) != 2 || report.Copied != 1 {
		t.Fatalf("report = %+v, %v, reauthenticated %d, engine runs %d", report, err, reauthenticated, len(engine.calls))
	}
}
