package copy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
	return newWorldWithResponseBarrier(t, nil)
}

func newWorldWithResponseBarrier(t *testing.T, barrier *swifttest.HTTPResponseBarrier) *world {
	t.Helper()
	w := &world{}
	build := func(mock *swifttest.Server, barrier *swifttest.HTTPResponseBarrier) *swiftcontrol.Session {
		var handler http.Handler = mock
		if barrier != nil {
			handler = barrier.Wrap(mock)
		}
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		if barrier != nil {
			t.Cleanup(barrier.Close)
		}
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
	w.srcMock = swifttest.New("test:tester", "testing")
	w.dstMock = swifttest.New("test:tester", "testing")
	w.src = build(w.srcMock, nil)
	w.dst = build(w.dstMock, barrier)
	w.srcMock.AddContainer("media", "standard")
	w.dstMock.AddContainer("backup", "standard")
	return w
}

func seed(mock *swifttest.Server, container, name string, data []byte, meta map[string]string) {
	sum := md5.Sum(data)
	mock.PutObject(container, name, swifttest.Object{
		Bytes: int64(len(data)), ETag: hex.EncodeToString(sum[:]),
		ContentType: "text/plain", Meta: meta, Data: data,
	})
}

// fakeEngine mimics the measured rclone behaviours: it drops custom
// metadata, segments anything over chunk into a generated prefix under
// the auto _segments sidecar (destination DLO), and can silently skip
// names while still "succeeding" — the §14.3 worst finding.
type fakeEngine struct {
	w     *world
	chunk int64
	drops map[string]bool
	// interruptAfter stops a Copy batch after N objects (simulating
	// SIGINT mid-batch); -1 disables.
	interruptAfter int
}

func parseSpec(spec string) (container, prefix string) {
	path := spec[strings.Index(spec, ":")+1:]
	if slash := strings.Index(path, "/"); slash >= 0 {
		return path[:slash], path[slash+1:] + "/"
	}
	return path, ""
}

func (f *fakeEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	srcC, srcP := parseSpec(src)
	dstC, dstP := parseSpec(dst)
	result := &rclone.Result{}
	done := 0
	for _, name := range names {
		if f.interruptAfter >= 0 && done >= f.interruptAfter {
			result.ExitCode = 1
			return result, nil
		}
		if f.drops[name] {
			continue // silent, exit 0: the measured trap
		}
		if err := f.one(ctx, srcC, srcP+name, dstC, dstP+name, result); err != nil {
			return result, err
		}
		done++
	}
	return result, nil
}

func (f *fakeEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	srcC, srcRest := parseSpec(src)
	dstC, dstRest := parseSpec(dst)
	result := &rclone.Result{}
	err := f.one(ctx, srcC, strings.TrimSuffix(srcRest, "/"), dstC, strings.TrimSuffix(dstRest, "/"), result)
	return result, err
}

var fakeUploadN = 0

func (f *fakeEngine) one(ctx context.Context, srcC, srcName, dstC, dstName string, result *rclone.Result) error {
	body, _, err := f.w.src.OpenObject(ctx, srcC, srcName)
	if err != nil {
		return err
	}
	defer body.Close()
	var data bytes.Buffer
	if _, err := data.ReadFrom(body); err != nil {
		return err
	}
	mtime := map[string]string{"Mtime": "1756400000.000000000"}
	if f.chunk > 0 && int64(data.Len()) > f.chunk {
		fakeUploadN++
		// PutObject creates the sidecar if missing (AddContainer would
		// clobber pre-seeded segments).
		segContainer := dstC + "_segments"
		prefix := fmt.Sprintf("%s/2026-08-29-%022d/", dstName, fakeUploadN)
		raw := data.Bytes()
		for i := 0; int64(i)*f.chunk < int64(len(raw)); i++ {
			end := min(int64(i+1)*f.chunk, int64(len(raw)))
			segment := raw[int64(i)*f.chunk : end]
			sum := md5.Sum(segment)
			f.w.dstMock.PutObject(segContainer, fmt.Sprintf("%s%08d", prefix, i), swifttest.Object{
				Bytes: int64(len(segment)), ETag: hex.EncodeToString(sum[:]), Data: segment,
			})
		}
		result.SegmentPrefixes = append(result.SegmentPrefixes, rclone.SegmentPrefix{Container: segContainer, Prefix: prefix})
		if _, err := f.w.dst.PutObject(ctx, dstC, dstName, strings.NewReader(""), "application/octet-stream",
			map[string]string{"X-Object-Manifest": segContainer + "/" + prefix, "X-Object-Meta-Mtime": mtime["Mtime"]}); err != nil {
			return err
		}
		return nil
	}
	_, err = f.w.dst.PutObject(ctx, dstC, dstName, &data, "text/plain",
		map[string]string{"X-Object-Meta-Mtime": mtime["Mtime"]})
	return err
}

// newWorldWrappingDest puts a handler of the caller's choosing in front of the
// DESTINATION mock, so a case can act between the requests this executor makes
// without reaching inside it. It is deliberately separate from the barrier
// variant above rather than folded into it: that variant's cleanup order is part
// of how a held reply is released, and this does not need it.
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

func newExecutor(t *testing.T, w *world, engine Engine) *Executor {
	t.Helper()
	e, err := New(Config{
		Source: w.src, Dest: w.dst, Engine: engine,
		SrcSpec: "aeronautes_src:media", DstSpec: "aeronautes_dst:backup",
		SrcContainer: "media", DstContainer: "backup",
		JobID: "job01",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	})
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

// ADR 0105 decision 3: the staging namespace is not a destination. A source
// that holds another job's stage is listed like any other, and the copy
// refuses each item whose destination name would land in a staging area,
// records it, creates nothing there and copies the rest.
func TestCopyRefusesADestinationInTheStagingNamespace(t *testing.T) {
	w := newWorld(t)
	const staged = ".aeronautes/stage/0a1b/batch/part.bin"
	seed(w.srcMock, "media", "a.txt", []byte("alpha"), nil)
	seed(w.srcMock, "media", staged, []byte("someone's stage"), nil)
	engine := &fakeEngine{w: w, interruptAfter: -1}

	report, err := newExecutor(t, w, engine).Run(context.Background(), []plan.Item{
		{Name: staged, Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 15},
		{Name: "a.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The refusal is a failure of that one name with its reason, and not an
	// overwrite refusal, which is what the job would otherwise report.
	if report.Copied != 1 || report.OverwritesRefused != 0 || len(report.Failures) != 1 ||
		report.Failures[0].Name != staged || !strings.Contains(report.Failures[0].Err, "staging area") {
		t.Fatalf("report = %+v, want the ordinary name copied and the staged one refused with its reason", report)
	}
	if _, err := w.dst.ObjectDetail(context.Background(), "backup", staged); err == nil {
		t.Error("the copy published into the staging namespace")
	}
	headDst(t, w, "a.txt")
}

func TestPlannedNewBatchFinalizes(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	seed(w.srcMock, "media", "a.txt", []byte("alpha"), map[string]string{"Shoot": "wedding"})
	seed(w.srcMock, "media", "b.txt", []byte("bravo!"), nil)
	engine := &fakeEngine{w: w, interruptAfter: -1}

	report, err := newExecutor(t, w, engine).Run(ctx, []plan.Item{
		{Name: "a.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5},
		{Name: "b.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 2 || len(report.Failures) != 0 || report.BytesCopied != 11 {
		t.Fatalf("report = %+v", report)
	}
	// The engine dropped custom metadata (measured §27.4); finalize
	// re-applied it and stamped the fingerprint.
	d := headDst(t, w, "a.txt")
	if d.Meta["Shoot"] != "wedding" {
		t.Errorf("metadata not re-applied: %v", d.Meta)
	}
	sum := md5.Sum([]byte("alpha"))
	want := "v1:5:" + hex.EncodeToString(sum[:])
	if d.Meta["Aeronautes-Fingerprint"] != want {
		t.Errorf("fingerprint = %q, want %q", d.Meta["Aeronautes-Fingerprint"], want)
	}
}

func TestSilentDropDetected(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "kept.txt", []byte("kept"), nil)
	seed(w.srcMock, "media", "dropped.txt", []byte("gone"), nil)
	engine := &fakeEngine{w: w, drops: map[string]bool{"dropped.txt": true}, interruptAfter: -1}

	report, err := newExecutor(t, w, engine).Run(context.Background(), []plan.Item{
		{Name: "dropped.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone},
		{Name: "kept.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Exit 0 + a missing object MUST surface as a failure (§14.3: exit
	// codes are useless for completeness).
	if report.Copied != 1 || len(report.Failures) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Failures[0].Name != "dropped.txt" || !strings.Contains(report.Failures[0].Err, "silent drop") {
		t.Errorf("failure = %+v", report.Failures[0])
	}
}

func TestLargePlannedNewStageAndCommit(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	// Live destination: a DLO whose segments are SHARED with another
	// manifest — the §28.6 acceptance fixture. Overwriting must leave
	// the other logical object fully intact.
	w.dstMock.AddContainer("backup_segments", "standard")
	sharedSegment := []byte("shared-segment-bytes")
	sum := md5.Sum(sharedSegment)
	w.dstMock.PutObject("backup_segments", "old/live.bin/1", swifttest.Object{
		Bytes: int64(len(sharedSegment)), ETag: hex.EncodeToString(sum[:]), Data: sharedSegment,
	})
	oldManifest := "backup_segments/old/live.bin/"
	w.dstMock.PutObject("backup", "previous.bin", swifttest.Object{
		Manifest: oldManifest, ContentType: "application/octet-stream",
	})
	w.dstMock.PutObject("backup", "sibling.bin", swifttest.Object{
		Manifest: oldManifest, ContentType: "application/octet-stream",
	})

	// Source: 5000 bytes, chunked at 2000 by the engine → 3 segments.
	payload := bytes.Repeat([]byte("x"), 5000)
	seed(w.srcMock, "media", "live.bin", payload, map[string]string{"Kind": "video"})

	engine := &fakeEngine{w: w, chunk: 2000, interruptAfter: -1}
	executor := newExecutor(t, w, engine)
	report, err := executor.Run(ctx, []plan.Item{{
		Name: "live.bin", Verdict: plan.VerdictNew, Route: plan.RouteRclone,
		SourceBytes: 5000, DestBytes: int64(len(sharedSegment)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 0 || report.OverwritesRefused != 0 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}

	// The live name is now a DLO over the staged prefix, fingerprinted,
	// with source metadata.
	live := headDst(t, w, "live.bin")
	if live.Class != swiftcontrol.ClassDLO || live.ContentLength != 5000 {
		t.Fatalf("live = %+v", live)
	}
	if live.Manifest == oldManifest {
		t.Fatal("manifest not swapped")
	}
	if live.Meta["Kind"] != "video" || live.Meta["Aeronautes-Fingerprint"] == "" {
		t.Errorf("metadata = %v", live.Meta)
	}
	// Unrelated DLOs retain their shared backing objects.
	sibling := headDst(t, w, "sibling.bin")
	if sibling.ContentLength != int64(len(sharedSegment)) {
		t.Fatalf("shared segments were harmed by the copy: %+v", sibling)
	}
	// Staging litter cleaned: manifest gone, no residue; committed
	// prefix recorded so reconciliation keeps it.
	if len(report.StagedResidue) != 0 {
		t.Errorf("staged residue = %v", report.StagedResidue)
	}
	if len(report.CommittedPrefixes) != 1 || len(report.CapturedPrefixes) != 1 {
		t.Errorf("prefixes: captured %v committed %v", report.CapturedPrefixes, report.CommittedPrefixes)
	}
	if _, err := w.dst.ObjectDetail(ctx, "backup", executor.StagingPrefix()+"batch/live.bin"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("staged manifest still present: %v", err)
	}
}

func TestRefusalPreservesReplacedDestination(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "doc.txt", []byte("source-v2"), nil)
	seed(w.dstMock, "backup", "doc.txt", []byte("external!"), nil)

	engine := &fakeEngine{w: w, interruptAfter: -1}
	report, err := newExecutor(t, w, engine).Run(context.Background(), []plan.Item{{
		Name: "doc.txt", Verdict: plan.VerdictChanged, Route: plan.RouteRclone,
		SourceBytes: 9, DestBytes: 9, DestETag: "plan-time-etag-no-longer-current",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || len(report.Conflicts) != 0 || report.Copied != 0 {
		t.Fatalf("report = %+v", report)
	}
	// Refusal must preserve the competing writer without creating staging.
	d := headDst(t, w, "doc.txt")
	sum := md5.Sum([]byte("external!"))
	if d.ETag != hex.EncodeToString(sum[:]) {
		t.Error("conflicted destination was overwritten")
	}
	if len(report.StagedResidue) != 0 {
		t.Errorf("staging not cleaned: %v", report.StagedResidue)
	}
}

func TestUnknownResolution(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// equal.txt: identical both sides → proven equal, skipped.
	seed(w.srcMock, "media", "equal.txt", []byte("same"), nil)
	seed(w.dstMock, "backup", "equal.txt", []byte("same"), nil)
	// fresh.txt: destination vanished since planning → runs as new.
	seed(w.srcMock, "media", "fresh.txt", []byte("fresh"), nil)
	// Unequal existing destinations are refused after HEAD resolution.
	seed(w.srcMock, "media", "diff.txt", []byte("aaaa"), nil)
	seed(w.dstMock, "backup", "diff.txt", []byte("bbbb"), nil)

	engine := &fakeEngine{w: w, interruptAfter: -1}
	items := []plan.Item{
		{Name: "diff.txt", Verdict: plan.VerdictUnknown, Route: plan.RouteRclone, Reason: plan.ReasonPossibleManifest},
		{Name: "equal.txt", Verdict: plan.VerdictUnknown, Route: plan.RouteRclone, Reason: plan.ReasonPossibleManifest},
		{Name: "fresh.txt", Verdict: plan.VerdictUnknown, Route: plan.RouteRclone, Reason: plan.ReasonPossibleManifest},
	}
	report, err := newExecutor(t, w, engine).Run(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedEqual != 1 || report.Copied != 1 || report.OverwritesRefused != 1 || report.Overwrites != 0 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	sum := md5.Sum([]byte("bbbb"))
	if headDst(t, w, "diff.txt").ETag != hex.EncodeToString(sum[:]) {
		t.Error("refused diff.txt changed")
	}
	if headDst(t, w, "fresh.txt").ContentLength != 5 {
		t.Error("fresh.txt not copied")
	}
}

func TestNativeUnsafeNames(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// Run takes strictly name-ordered items, as its own contract says and as
	// plan.Copy produces, because this executor's private spools are written in
	// that order and read forward. This list was hand-written out of order, which
	// no planner produces; sorting it keeps the case about hostile NAMES rather
	// than about an input the contract excludes.
	hostile := []string{"/lead", "trail/", "a//b", "line\nbreak"}
	slices.Sort(hostile)
	for _, name := range hostile {
		seed(w.srcMock, "media", name, []byte("payload-"+name), map[string]string{"Origin": "suite"})
	}

	var items []plan.Item
	for _, name := range hostile {
		items = append(items, plan.Item{
			Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonUnsafeName,
		})
	}
	// No engine at all: native routing must not need one.
	report, err := newExecutor(t, w, nil).Run(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != int64(len(hostile)) || report.NativeRouted != int64(len(hostile)) || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	for _, name := range hostile {
		d := headDst(t, w, name)
		sum := md5.Sum([]byte("payload-" + name))
		if d.ETag != hex.EncodeToString(sum[:]) || d.Meta["Origin"] != "suite" {
			t.Errorf("%q arrived wrong: etag %s meta %v", name, d.ETag, d.Meta)
		}
		// ADR 0045: this route publishes unstamped. The stamp asserts that the
		// destination holds the source's content, and a one-request publication
		// offers no moment at which that is already established.
		if d.Meta[fingerprintMetaKey] != "" {
			t.Errorf("%q was stamped %q", name, d.Meta[fingerprintMetaKey])
		}
	}
}

// A manifest source this route cannot hold is not attempted: ADR 0084 binds a
// manifest by digesting the bytes it holds, so above the bound there is nothing
// to declare and the alternative would be publishing bytes nobody compared.
func TestNativeManifestPastTheHoldBoundIsNotAttempted(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.srcMock.AddContainer("media_segments", "standard")
	// Two segments whose assembled length is one mebibyte past the bound.
	half := heldManifestBound/2 + 512*1024
	for i := 0; i < 2; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i)}, int(half))
		sum := md5.Sum(chunk)
		w.srcMock.PutObject("media_segments", fmt.Sprintf("weird//big/%d", i), swifttest.Object{
			Bytes: int64(len(chunk)), ETag: hex.EncodeToString(sum[:]), Data: chunk,
		})
	}
	w.srcMock.PutObject("media", "weird//big", swifttest.Object{
		Manifest: "media_segments/weird//big/", ContentType: "video/mp4",
	})

	report, err := newExecutor(t, w, nil).Run(ctx, []plan.Item{{
		Name: "weird//big", Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonPossibleManifest,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || len(report.Failures) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if got := report.Failures[0]; got.Stage != "native-class" || !strings.Contains(got.Err, "bound") {
		t.Errorf("failure = %+v, want the hold bound named at the native-class stage", got)
	}
	// Nothing was published, so there is no residue to retain.
	if _, err := w.dst.ObjectDetail(ctx, "backup", "weird//big"); err == nil {
		t.Error("a source past the bound published something")
	}
}

func TestNativeDLOMaterializesSmallLogical(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// An unsafe-named DLO with small logical content: the native path
	// serves the assembled stream and lands a plain object (§25 logical
	// replication; re-segmentation to one inline "segment").
	w.srcMock.AddContainer("media_segments", "standard")
	for i, chunk := range []string{"part-one|", "part-two"} {
		sum := md5.Sum([]byte(chunk))
		w.srcMock.PutObject("media_segments", fmt.Sprintf("weird//dlo/%d", i), swifttest.Object{
			Bytes: int64(len(chunk)), ETag: hex.EncodeToString(sum[:]), Data: []byte(chunk),
		})
	}
	w.srcMock.PutObject("media", "weird//dlo", swifttest.Object{
		Manifest: "media_segments/weird//dlo/", ContentType: "video/mp4",
	})

	report, err := newExecutor(t, w, nil).Run(ctx, []plan.Item{{
		Name: "weird//dlo", Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonPossibleManifest,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	d := headDst(t, w, "weird//dlo")
	if d.Class != swiftcontrol.ClassPlain || d.ContentLength != 17 {
		t.Errorf("materialized = %+v", d)
	}
}

func TestVerifyCatchesSabotage(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	seed(w.srcMock, "media", "ok.txt", []byte("fine"), nil)
	seed(w.dstMock, "backup", "ok.txt", []byte("fine"), nil)
	seed(w.srcMock, "media", "hurt.txt", []byte("real bytes"), nil)
	seed(w.dstMock, "backup", "hurt.txt", []byte("corrupted!"), nil)
	seed(w.srcMock, "media", "missing.txt", []byte("never arrived"), nil)

	executor := newExecutor(t, w, nil)
	report, err := executor.Verify(ctx,
		w.src.Objects(ctx, "media", swiftcontrol.ObjectListOptions{Deep: true}),
		w.dst.Objects(ctx, "backup", swiftcontrol.ObjectListOptions{Deep: true}))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified != 1 || len(report.Failures) != 2 {
		t.Fatalf("verify = %+v", report)
	}
}

func TestReconcileRetainsUncertainData(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	executor := newExecutor(t, w, nil)
	w.dstMock.AddContainer("backup_segments", "standard")

	// A completed upload: live DLO references its captured prefix.
	completedPrefix := "video.bin/2026-08-29-0000000000000000000001/"
	w.dstMock.PutObject("backup_segments", completedPrefix+"00000000", swifttest.Object{Bytes: 4, ETag: "aaaa", Data: []byte("data")})
	w.dstMock.PutObject("backup", "video.bin", swifttest.Object{Manifest: "backup_segments/" + completedPrefix})

	// Missing local references cannot establish deletion authority.
	orphanPrefix := "big.bin/2026-08-29-0000000000000000000002/"
	for i := 0; i < 3; i++ {
		w.dstMock.PutObject("backup_segments", fmt.Sprintf("%s%08d", orphanPrefix, i), swifttest.Object{Bytes: 4, ETag: "bbbb", Data: []byte("data")})
	}

	// Staged residue left by a crash.
	staged := executor.StagingPrefix() + "000007"
	w.dstMock.PutObject("backup", staged, swifttest.Object{Bytes: 1, ETag: "cccc", Data: []byte("x")})

	report := &Report{
		CapturedPrefixes: []rclone.SegmentPrefix{
			{Container: "backup_segments", Prefix: completedPrefix},
			{Container: "backup_segments", Prefix: orphanPrefix},
		},
		StagedResidue: []string{staged},
	}
	out, err := executor.Reconcile(ctx, report)
	if err != nil {
		t.Fatal(err)
	}
	if out.SweptStaged != 0 || out.SweptSegments != 0 || len(out.Notes) == 0 {
		t.Fatalf("reconcile = %+v", out)
	}
	// The completed object's segments survived (its prefix was kept).
	if headDst(t, w, "video.bin").ContentLength != 4 {
		t.Error("completed DLO broken by reconciliation")
	}
	if len(out.KeptPrefixes) != 2 || !strings.Contains(out.KeptPrefixes[0], completedPrefix) {
		t.Errorf("kept = %v", out.KeptPrefixes)
	}
	if _, err := w.dst.ObjectDetail(ctx, "backup_segments", orphanPrefix+"00000000"); err != nil {
		t.Errorf("uncertain segment was not retained: %v", err)
	}
	if _, err := w.dst.ObjectDetail(ctx, "backup", staged); err != nil {
		t.Errorf("uncertain staging object was not retained: %v", err)
	}
}

// statusEngine wraps another engine and force-returns canned results for
// the first N Copy calls — the exit-status contract fixture.
type statusEngine struct {
	inner  Engine
	canned []*rclone.Result
	calls  int
}

func (s *statusEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	s.calls++
	if len(s.canned) > 0 {
		r := s.canned[0]
		s.canned = s.canned[1:]
		return r, nil
	}
	return s.inner.Copy(ctx, src, dst, names, onEvent)
}

func (s *statusEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	s.calls++
	if len(s.canned) > 0 {
		r := s.canned[0]
		s.canned = s.canned[1:]
		return r, nil
	}
	return s.inner.CopyTo(ctx, src, dst, onEvent)
}

func TestEngineStatusIsAuthoritativeFailure(t *testing.T) {
	ctx := context.Background()
	items := func() []plan.Item {
		return []plan.Item{{Name: "a.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5}}
	}
	seedBoth := func() *world {
		w := newWorld(t)
		seed(w.srcMock, "media", "a.txt", []byte("aaaaa"), nil)
		return w
	}

	// Nonzero exit fails the batch even when a same-size destination
	// answers the verification HEAD (stale/partial output must not be
	// silently accepted).
	w := seedBoth()
	seed(w.dstMock, "backup", "a.txt", []byte("bbbbb"), nil)
	e := newExecutor(t, w, &statusEngine{canned: []*rclone.Result{{ExitCode: 9, Errors: []string{"io broke"}}}})
	report, err := e.Run(ctx, items())
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || len(report.Failures) != 1 || report.Failures[0].Stage != "engine-status" {
		t.Fatalf("exit 9 report = %+v", report)
	}

	// A cancelled child fails the names rather than finalizing them.
	w = seedBoth()
	e = newExecutor(t, w, &statusEngine{canned: []*rclone.Result{{Cancelled: true}}})
	report, _ = e.Run(ctx, items())
	if report.Copied != 0 || len(report.Failures) != 1 {
		t.Fatalf("cancelled report = %+v", report)
	}

	// A truncated log stream means capture cannot be trusted: failure.
	w = seedBoth()
	e = newExecutor(t, w, &statusEngine{canned: []*rclone.Result{{LogStreamErr: "bufio: token too long"}}})
	report, _ = e.Run(ctx, items())
	if report.Copied != 0 || len(report.Failures) != 1 {
		t.Fatalf("log-stream report = %+v", report)
	}

	// An auth-shaped failure earns exactly one fresh child; the second
	// spawn succeeds and the name lands verified.
	w = seedBoth()
	authy := &statusEngine{
		inner:  &fakeEngine{w: w, interruptAfter: -1},
		canned: []*rclone.Result{{ExitCode: 1, Errors: []string{"HTTP Error: 401 Unauthorized"}}},
	}
	e = newExecutor(t, w, authy)
	report, err = e.Run(ctx, items())
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || len(report.Failures) != 0 || authy.calls != 2 {
		t.Fatalf("auth retry report = %+v calls=%d", report, authy.calls)
	}

	// Two auth failures in a row: the retry is bounded at one.
	w = seedBoth()
	twice := &statusEngine{canned: []*rclone.Result{
		{ExitCode: 1, Errors: []string{"401 Unauthorized"}},
		{ExitCode: 1, Errors: []string{"401 Unauthorized"}},
	}}
	e = newExecutor(t, w, twice)
	report, _ = e.Run(ctx, items())
	if report.Copied != 0 || len(report.Failures) != 1 || twice.calls != 2 {
		t.Fatalf("bounded retry report = %+v calls=%d", report, twice.calls)
	}
}

// recordingEngine wraps an engine and records every destination spec the
// executor hands it.
type recordingEngine struct {
	inner Engine
	dsts  []string
}

func (r *recordingEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	r.dsts = append(r.dsts, dst)
	return r.inner.Copy(ctx, src, dst, names, onEvent)
}

func (r *recordingEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	r.dsts = append(r.dsts, dst)
	return r.inner.CopyTo(ctx, src, dst, onEvent)
}

func TestEngineNeverReceivesLiveNames(t *testing.T) {
	// K-XFER-014's absolute boundary, mechanized: every engine write —
	// planned-new batches only — carries the job
	// staging namespace, never a live destination path.
	w := newWorld(t)
	seed(w.srcMock, "media", "new-a.txt", []byte("alpha"), nil)
	seed(w.srcMock, "media", "new-b.txt", []byte("bravo"), nil)
	seed(w.srcMock, "media", "changed.txt", []byte("fresh content"), nil)
	seed(w.dstMock, "backup", "changed.txt", []byte("old"), nil)

	rec := &recordingEngine{inner: &fakeEngine{w: w, interruptAfter: -1}}
	e := newExecutor(t, w, rec)
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "new-a.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5},
		{Name: "new-b.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5},
		{Name: "changed.txt", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: 13, DestETag: md5hex("old")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 2 || report.OverwritesRefused != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(rec.dsts) == 0 {
		t.Fatal("engine never invoked")
	}
	for _, dst := range rec.dsts {
		if !strings.Contains(dst, "/.aeronautes/stage/") {
			t.Errorf("engine received a live destination %q", dst)
		}
	}
	// New names committed; the refused existing name remains unchanged.
	ctx := context.Background()
	for _, name := range []string{"new-a.txt", "new-b.txt", "changed.txt"} {
		if _, err := w.dst.ObjectDetail(ctx, "backup", name); err != nil {
			t.Errorf("%s missing after staged commit: %v", name, err)
		}
	}
	if headDst(t, w, "changed.txt").ETag != md5hex("old") {
		t.Error("refused changed name was modified")
	}
	if len(report.StagedResidue) != 0 {
		t.Errorf("staging residue after clean run: %v", report.StagedResidue)
	}
}

func TestPlannedNewConflictsWhenDestinationAppears(t *testing.T) {
	// The C-02 race: a name classified New gains a live DLO (whose
	// segments another manifest shares) before the engine batch lands.
	// The commit must CONFLICT — the shared segments survive untouched.
	w := newWorld(t)
	seed(w.srcMock, "media", "video.bin", []byte("new content!"), nil)
	w.dstMock.AddContainer("backup_segments", "standard")
	seed(w.dstMock, "backup_segments", "shared/0", bytes.Repeat([]byte("s"), 40), nil)
	w.dstMock.PutObject("backup", "video.bin", swifttest.Object{Manifest: "backup_segments/shared/"})
	w.dstMock.PutObject("backup", "sibling.bin", swifttest.Object{Manifest: "backup_segments/shared/"})

	e := newExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1})
	report, err := e.Run(context.Background(), []plan.Item{
		// Planned as New: the advisory listing predates the DLO.
		{Name: "video.bin", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Conflicts) != 1 || report.Copied != 0 {
		t.Fatalf("report = %+v, want one conflict and no copy", report)
	}
	// The concurrent DLO and its shared segments are intact.
	ctx := context.Background()
	d, err := w.dst.ObjectDetail(ctx, "backup", "sibling.bin")
	if err != nil || d.ContentLength != 40 {
		t.Fatalf("sibling = %+v %v: shared segments were harmed", d, err)
	}
	live, err := w.dst.ObjectDetail(ctx, "backup", "video.bin")
	if err != nil || live.Class != swiftcontrol.ClassDLO {
		t.Fatalf("live DLO = %+v %v: the racing object was overwritten", live, err)
	}
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestUnobservedManifestNeverPromoted(t *testing.T) {
	// H-01: a destination manifest whose prefix this job's child was
	// never observed creating must not become a committed (ownership)
	// prefix, whatever the commit path sees.
	w := newWorld(t)
	e := newExecutor(t, w, nil)
	e.markCommitted("backup_segments/somebody-elses/prefix/")
	if len(e.report.CommittedPrefixes) != 0 {
		t.Fatalf("unobserved manifest promoted: %+v", e.report.CommittedPrefixes)
	}
	// An observed (captured) prefix promotes normally.
	e.capturePrefix(rclone.SegmentPrefix{Container: "backup_segments", Prefix: "ours/gen/"})
	e.markCommitted("backup_segments/ours/gen/")
	if len(e.report.CommittedPrefixes) != 1 {
		t.Fatalf("observed manifest not promoted: %+v", e.report.CommittedPrefixes)
	}
}

func TestStaleManifestFingerprintCannotSkip(t *testing.T) {
	// H-04: a destination DLO carrying a matching fingerprint but whose
	// live logical size no longer matches (a segment vanished) must NOT
	// be skipped as proven-equal.
	w := newWorld(t)
	seed(w.srcMock, "media", "video.bin", bytes.Repeat([]byte("v"), 3000), nil)
	src, err := w.src.ObjectDetail(context.Background(), "media", "video.bin")
	if err != nil {
		t.Fatal(err)
	}
	w.dstMock.AddContainer("backup_segments", "standard")
	// Destination DLO: fingerprint stamped for the FULL size, but only
	// ONE segment actually remains (2000 of 3000 bytes).
	seed(w.dstMock, "backup_segments", "video.bin/gen/00000000", bytes.Repeat([]byte("v"), 2000), nil)
	w.dstMock.PutObject("backup", "video.bin", swifttest.Object{
		Manifest:    "backup_segments/video.bin/gen/",
		ContentType: "text/plain", // a faithful copy carries the source's content type (§27.4)
		Meta:        map[string]string{"Aeronautes-Fingerprint": fingerprint(src.ContentLength, src.ETag)},
	})
	dst, err := w.dst.ObjectDetail(context.Background(), "backup", "video.bin")
	if err != nil {
		t.Fatal(err)
	}
	if skippableEqual(src, dst) {
		t.Fatal("stale fingerprint with mismatched live length passed as proven-equal")
	}

	// Restoring the missing bytes makes the live length match again, but
	// a destination this executor never published carries no staged
	// aggregate, and an absent stamp is not evidence.
	seed(w.dstMock, "backup_segments", "video.bin/gen/00000001", bytes.Repeat([]byte("v"), 1000), nil)
	dst, _ = w.dst.ObjectDetail(context.Background(), "backup", "video.bin")
	if skippableEqual(src, dst) {
		t.Fatal("unstamped DLO destination passed as proven-equal")
	}

	// Stamped with the aggregate actually observed, it IS proven.
	w.dstMock.PutObject("backup", "video.bin", swifttest.Object{
		Manifest:    "backup_segments/video.bin/gen/",
		ContentType: "text/plain", // a faithful copy carries the source's content type (§27.4)
		Meta: map[string]string{
			"Aeronautes-Fingerprint":      fingerprint(src.ContentLength, src.ETag),
			"Aeronautes-Staged-Aggregate": aggregateStamp(dst.ContentLength, dst.ETag),
		},
	})
	dst, _ = w.dst.ObjectDetail(context.Background(), "backup", "video.bin")
	if !skippableEqual(src, dst) {
		t.Fatal("intact stamped DLO no longer proves equal")
	}
}

func TestStagedSegmentCompletenessGate(t *testing.T) {
	// K-CON-001: a staged DLO whose segments do not sum to the source's
	// logical size must fail before commit — driven end to end with a
	// chunking engine that drops one segment.
	w := newWorld(t)
	seed(w.srcMock, "media", "big.bin", bytes.Repeat([]byte("b"), 5000), nil)
	w.dstMock.AddContainer("backup_segments", "standard")

	engine := &segmentDroppingEngine{fakeEngine{w: w, chunk: 1024, interruptAfter: -1}}
	e := newExecutor(t, w, engine)
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "big.bin", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || len(report.Failures) != 1 {
		t.Fatalf("report = %+v, want the truncated stage to fail", report)
	}
	// Incomplete staged bytes must never become a new live object.
	live, err := w.dst.ObjectDetail(context.Background(), "backup", "big.bin")
	if !swiftcontrol.IsNotFound(err) {
		t.Fatalf("live = %+v %v (truncated commit went live)", live, err)
	}
}

// segmentDroppingEngine chunks like the fake engine, then deletes one
// segment after upload — a truncated sidecar whose manifest still
// answers.
type segmentDroppingEngine struct {
	fakeEngine
}

func (s *segmentDroppingEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	result, err := s.fakeEngine.Copy(ctx, src, dst, names, onEvent)
	if err == nil && len(result.SegmentPrefixes) > 0 {
		p := result.SegmentPrefixes[0]
		var names []string
		for entry, listErr := range s.w.dst.Objects(ctx, p.Container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: p.Prefix}) {
			if listErr != nil {
				break
			}
			if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
				names = append(names, object.Name)
			}
		}
		if len(names) > 0 {
			_ = s.w.dst.DeleteObject(ctx, p.Container, names[len(names)-1])
		}
	}
	return result, err
}

func TestSymlinkCopiedAsSymlink(t *testing.T) {
	// §17.1's carry-when-present row, finally executable: a source
	// symlink lands at the destination AS a symlink with the link's own
	// metadata — never materialized target bytes.
	w := newWorld(t)
	seed(w.srcMock, "media", "real/data.bin", []byte("target-bytes"), nil)
	w.srcMock.PutObject("media", "link.bin", swifttest.Object{
		ContentType:   "application/octet-stream",
		SymlinkTarget: "media/real/data.bin",
		Meta:          map[string]string{"Kind": "pointer"},
	})

	e := newExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1})
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "link.bin", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	link, err := w.dst.SymlinkGet(context.Background(), "backup", "link.bin")
	if err != nil {
		t.Fatal(err)
	}
	if link.SymlinkTarget != "media/real/data.bin" || link.Meta["Kind"] != "pointer" {
		t.Fatalf("destination link = %+v", link)
	}
}

func TestReheadRefusesDestinationCreatedDuringStaging(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "raced.txt", []byte("mine"), nil)
	e := newExecutor(t, w, &liveRacingEngine{fakeEngine{w: w, interruptAfter: -1}})
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "raced.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Conflicts) != 1 || report.Copied != 0 {
		t.Fatalf("report = %+v, want a commit-time conflict", report)
	}
	live, err := w.dst.ObjectDetail(context.Background(), "backup", "raced.txt")
	if err != nil || live.ContentLength != int64(len("theirs, first")) {
		t.Fatalf("live = %+v %v (the racing writer's object was overwritten)", live, err)
	}
}

// This writer arrives during staging, before the final destination HEAD.
// The acknowledged response-barrier tests cover the later race window.
type liveRacingEngine struct {
	fakeEngine
}

func (l *liveRacingEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	result, err := l.fakeEngine.Copy(ctx, src, dst, names, onEvent)
	seed(l.w.dstMock, "backup", "raced.txt", []byte("theirs, first"), nil)
	return result, err
}

// placeSLO stores a static large object whose recorded manifest states the
// given parts and whose body serves exactly servedBytes.
//
// Swift reports the aggregate a static manifest RECORDED rather than one
// derived from the segments present now, so a manifest and a body can
// disagree while every header stays self-consistent. That is the condition
// ADR 0084's comparison exists to catch, and the one a digest of delivered
// bytes cannot see: the delivered bytes always hash to their own digest.
func placeSLO(t *testing.T, mock *swifttest.Server, container, name string, parts [][]byte, servedBytes []byte) {
	t.Helper()
	var entries []string
	var total int64
	aggregate := md5.New()
	for i, part := range parts {
		sum := md5.Sum(part)
		hash := hex.EncodeToString(sum[:])
		segment := fmt.Sprintf("%s/%d", name, i)
		mock.PutObject("media_segments", segment, swifttest.Object{
			Bytes: int64(len(part)), ETag: hash, Data: part,
		})
		entries = append(entries, fmt.Sprintf(
			`{"name": "/media_segments/%s", "hash": "%s", "bytes": %d, "content_type": "application/octet-stream"}`,
			segment, hash, len(part)))
		total += int64(len(part))
		if _, err := aggregate.Write([]byte(hash)); err != nil {
			t.Fatal(err)
		}
	}
	etag := hex.EncodeToString(aggregate.Sum(nil))
	mock.PutObject(container, name, swifttest.Object{
		Bytes: total, ETag: `"` + etag + `"`, SLOEtag: etag, SLO: true,
		SLOManifestJSON: "[" + strings.Join(entries, ",") + "]",
		ContentType:     "video/mp4", Data: servedBytes,
	})
}

// A manifest source's copy is bound to the source's OWN manifest, not to
// whatever the GET delivered (ADR 0084).
//
// The drifted rows are the regression guard for the shape this route was
// first written in: hold the delivered bytes, hash THEM, declare that
// digest. Under that shape the destination's PUT always satisfies its own
// declaration, so same-length content that is not what the manifest
// describes publishes cleanly and verifies cleanly — the binding proves
// only that the transport did not corrupt what it was handed. Comparing
// against the ladder the manifest itself supplies is what makes the
// declaration mean the source's content.
func TestNativeManifestBindsAgainstItsManifest(t *testing.T) {
	parts := [][]byte{[]byte("part-one|"), []byte("part-two")}
	whole := append(append([]byte{}, parts[0]...), parts[1]...)
	// Same length, different bytes: the only substitution a length check
	// and a self-consistent digest both miss.
	drifted := append(append([]byte{}, parts[0]...), []byte("EVIL-two")...)

	for _, tc := range []struct {
		name     string
		static   bool
		drift    bool
		copied   int64
		errorHas string
	}{
		{name: "slo_matches_its_manifest", static: true, copied: 1},
		{name: "slo_content_drifted", static: true, drift: true, errorHas: "did not match its manifest"},
		{name: "dlo_matches_its_segments", copied: 1},
		{name: "dlo_content_drifted", drift: true, errorHas: "did not match its manifest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			ctx := context.Background()
			w.srcMock.AddContainer("media_segments", "standard")
			const name = "weird//manifest"
			if tc.static {
				served := whole
				if tc.drift {
					served = drifted
				}
				placeSLO(t, w.srcMock, "media", name, parts, served)
			} else {
				for i, part := range parts {
					sum := md5.Sum(part)
					w.srcMock.PutObject("media_segments", fmt.Sprintf("%s/%d", name, i), swifttest.Object{
						Bytes: int64(len(part)), ETag: hex.EncodeToString(sum[:]), Data: part,
					})
				}
				w.srcMock.PutObject("media", name, swifttest.Object{
					Manifest: "media_segments/" + name + "/", ContentType: "video/mp4",
				})
				if tc.drift {
					// The segment's recorded entity tag stays as it was, so
					// the listing still recombines to the aggregate the HEAD
					// reports and the ladder still builds: only the bytes the
					// store serves for that run have changed.
					sum := md5.Sum(parts[1])
					w.srcMock.PutObject("media_segments", fmt.Sprintf("%s/1", name), swifttest.Object{
						Bytes: int64(len(parts[1])), ETag: hex.EncodeToString(sum[:]), Data: []byte("EVIL-two"),
					})
				}
			}
			before, err := w.src.ObjectDetail(ctx, "media", name)
			if err != nil {
				t.Fatal(err)
			}

			report, err := newExecutor(t, w, nil).Run(ctx, []plan.Item{{
				Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonPossibleManifest,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Copied != tc.copied {
				t.Fatalf("report = %+v, want %d copied", report, tc.copied)
			}
			if tc.errorHas == "" {
				if len(report.Failures) != 0 {
					t.Fatalf("report = %+v", report)
				}
				// The declaration the store enforced is the digest of the
				// bytes that matched, so the destination carries it.
				sum := md5.Sum(whole)
				d := headDst(t, w, name)
				if d.Class != swiftcontrol.ClassPlain || d.ETag != hex.EncodeToString(sum[:]) || d.ContentLength != int64(len(whole)) {
					t.Errorf("materialized = %+v, want a plain %d-byte object with the verified digest", d, len(whole))
				}
			} else {
				if len(report.Failures) != 1 {
					t.Fatalf("report = %+v, want one failure", report)
				}
				if got := report.Failures[0]; got.Stage != "native-hold" || !strings.Contains(got.Err, tc.errorHas) {
					t.Errorf("failure = %+v, want %q at the native-hold stage", got, tc.errorHas)
				}
				// Refused BEFORE the destination PUT: nothing was placed, so
				// there is no residue and no overwrite decision to make.
				if _, err := w.dst.ObjectDetail(ctx, "backup", name); err == nil {
					t.Error("a source whose content did not match its manifest published something")
				}
			}
			// A read never writes: the source is as it was either way.
			after, err := w.src.ObjectDetail(ctx, "media", name)
			if err != nil {
				t.Fatal(err)
			}
			if after.ETag != before.ETag || after.ContentLength != before.ContentLength {
				t.Errorf("source changed: %+v, was %+v", after, before)
			}
		})
	}
}

// The hold's two wrappers, at the boundaries the route depends on: the cap
// refuses rather than growing, and the body is closed once even though the
// comparison and this route's own deferred close both close it.
func TestHeldManifestWrappers(t *testing.T) {
	hold := &cappedHold{limit: 4}
	if n, err := hold.Write([]byte("DATA")); n != 4 || err != nil {
		t.Fatalf("write at the limit = %d, %v", n, err)
	}
	if _, err := hold.Write([]byte("X")); err == nil || !strings.Contains(err.Error(), "4-byte hold") {
		t.Errorf("write past the limit = %v, want the limit named", err)
	}
	if string(hold.bytes) != "DATA" {
		t.Errorf("held = %q, want the refused write excluded", hold.bytes)
	}

	failure := errors.New("source body close failed")
	closes := 0
	once := closeOnce(readCloser{Reader: strings.NewReader(""), Closer: closerFunc(func() error {
		closes++
		return failure
	})})
	if err := once.Close(); !errors.Is(err, failure) {
		t.Errorf("first close = %v, want the body's own failure", err)
	}
	if err := once.Close(); err != nil {
		t.Errorf("second close = %v, want nil", err)
	}
	if closes != 1 {
		t.Errorf("delegated closes = %d, want one", closes)
	}
}

// A plain source that does not state a complete MD5 states nothing this route
// can declare, so it is not attempted. Swift states one for every plain object;
// this is the shape of a source that is not what it says it is, and the point of
// naming it here is that the alternative is a PUT the store refuses, reported as
// a commit failure for a source that was never usable.
func TestNativePlainSourceWithoutAnMD5IsNotAttempted(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.srcMock.PutObject("media", "weird//short", swifttest.Object{
		Bytes: 5, ETag: "short", Data: []byte("alpha"), ContentType: "text/plain",
	})

	report, err := newExecutor(t, w, nil).Run(ctx, []plan.Item{{
		Name: "weird//short", Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonUnsafeName,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || len(report.Failures) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if got := report.Failures[0]; got.Stage != "native-etag" || !strings.Contains(got.Err, "complete MD5") {
		t.Errorf("failure = %+v, want the missing MD5 named at the native-etag stage", got)
	}
	if _, err := w.dst.ObjectDetail(ctx, "backup", "weird//short"); err == nil {
		t.Error("a source with no declarable digest published something")
	}
}

// Out-of-order items stop the job rather than quietly losing a name's evidence.
// The spool carrying a native copy's proof is read forward, so a name written out
// of order would be skipped by that read and a good copy would lose its verified
// credit with nothing saying why. Run's contract already requires ascending names
// and plan.Copy produces them; this is what happens when a caller breaks it. The
// publication has already happened by then, which is why the run stops and
// reports rather than pretending otherwise. The sources here are manifests
// because those are the copies whose proof the spool carries.
func TestNativeOutOfOrderItemsStopTheJob(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.srcMock.AddContainer("media_segments", "standard")
	for _, name := range []string{"b//dlo", "a//dlo"} {
		chunk := []byte("payload-" + name)
		sum := md5.Sum(chunk)
		w.srcMock.PutObject("media_segments", name+"/0", swifttest.Object{
			Bytes: int64(len(chunk)), ETag: hex.EncodeToString(sum[:]), Data: chunk,
		})
		w.srcMock.PutObject("media", name, swifttest.Object{
			Manifest: "media_segments/" + name + "/", ContentType: "video/mp4",
		})
	}
	_, err := newExecutor(t, w, nil).Run(ctx, []plan.Item{
		{Name: "b//dlo", Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonPossibleManifest},
		{Name: "a//dlo", Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonPossibleManifest},
	})
	if err == nil || !strings.Contains(err.Error(), "strictly ordered") {
		t.Fatalf("Run = %v, want the spool's ordering requirement named", err)
	}
	// Both names were published before the second one's evidence could not be
	// stored, and nothing deletes them: the stop is about evidence, not residue.
	for _, name := range []string{"b//dlo", "a//dlo"} {
		if _, err := w.dst.ObjectDetail(ctx, "backup", name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
}

// A copy's own evidence is about a SOURCE as well as a destination. The native
// route publishes unstamped, so what a successful copy proved is kept in the job
// rather than on the object — and a destination still holding exactly the verified
// bytes says nothing once the source has become different content of the same
// length. The copy was true when it was made; the two sides no longer agree, and
// a fresh pass that credited it would turn a stale proof into a current one.
func TestNativeEvidenceRejectsAChangedSource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   bool
		verified int64
		failures int
	}{
		{name: "source_unchanged_is_verified", verified: 1},
		{name: "source_changed_after_the_copy_is_not", change: true, failures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			ctx := context.Background()
			w.srcMock.AddContainer("media_segments", "standard")
			for i, chunk := range []string{"part-one|", "part-two"} {
				sum := md5.Sum([]byte(chunk))
				w.srcMock.PutObject("media_segments", fmt.Sprintf("weird//dlo/%d", i), swifttest.Object{
					Bytes: int64(len(chunk)), ETag: hex.EncodeToString(sum[:]), Data: []byte(chunk),
				})
			}
			w.srcMock.PutObject("media", "weird//dlo", swifttest.Object{
				Manifest: "media_segments/weird//dlo/", ContentType: "video/mp4",
			})

			executor := newExecutor(t, w, nil)
			report, err := executor.Run(ctx, []plan.Item{{
				Name: "weird//dlo", Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonPossibleManifest,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Copied != 1 || len(report.Failures) != 0 {
				t.Fatalf("report = %+v", report)
			}
			if tc.change {
				// An honest source change: the segment's recorded entity tag
				// moves with its bytes, so the manifest's aggregate moves too and
				// the source now describes different content of the same length.
				chunk := []byte("EVIL-two")
				sum := md5.Sum(chunk)
				w.srcMock.PutObject("media_segments", "weird//dlo/1", swifttest.Object{
					Bytes: int64(len(chunk)), ETag: hex.EncodeToString(sum[:]), Data: chunk,
				})
			}

			verify, err := executor.Verify(ctx,
				w.src.Objects(ctx, "media", swiftcontrol.ObjectListOptions{Deep: true}),
				w.dst.Objects(ctx, "backup", swiftcontrol.ObjectListOptions{Deep: true}))
			if err != nil {
				t.Fatal(err)
			}
			if verify.Verified != tc.verified || len(verify.Failures) != tc.failures {
				t.Fatalf("verify = %+v, want %d verified and %d failures", verify, tc.verified, tc.failures)
			}
			// The destination is untouched either way: this is about what may be
			// claimed, not about repairing anything (ADR 0020).
			if d := headDst(t, w, "weird//dlo"); d.ContentLength != 17 {
				t.Errorf("destination changed: %+v", d)
			}
		})
	}
}

// A name this job recorded as a content failure takes no verified credit, no
// Same verdict and no skip in the same job, independently of any stamp (ADR
// 0045). The fresh listing-grade pass is where that bites: a destination replaced
// with equal bytes after the failure makes two listing rows agree, and that
// agreement is about the rows, not about the copy this job already found wrong.
func TestRecordedContentFailureTakesNoLaterCredit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sabotage bool
		copied   int64
		verified int64
		failures int
	}{
		{name: "healthy_copy_is_verified", copied: 1, verified: 1},
		{name: "recorded_failure_is_not_verified_when_the_rows_later_agree", sabotage: true, failures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const name = "weird//plain"
			body := []byte("alpha")
			var w *world
			// The destination is replaced between this route's PUT and the HEAD
			// that checks it, which is what the post-PUT comparison exists to
			// catch. The replacement is same-length, so nothing but the digest
			// says it happened.
			w = newWorldWrappingDest(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(rw, r)
					if !tc.sabotage || r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/"+name) {
						return
					}
					sum := md5.Sum([]byte("EVILS"))
					w.dstMock.PutObject("backup", name, swifttest.Object{
						Bytes: 5, ETag: hex.EncodeToString(sum[:]), Data: []byte("EVILS"), ContentType: "text/plain",
					})
				})
			})
			seed(w.srcMock, "media", name, body, nil)

			executor := newExecutor(t, w, nil)
			report, err := executor.Run(ctx, []plan.Item{{
				Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonUnsafeName,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Copied != tc.copied {
				t.Fatalf("report = %+v, want %d copied", report, tc.copied)
			}
			if tc.sabotage {
				if len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err, "declared") {
					t.Fatalf("report = %+v, want the declaration named", report)
				}
				// Equal bytes restored before verification: the rows now agree.
				sum := md5.Sum(body)
				w.dstMock.PutObject("backup", name, swifttest.Object{
					Bytes: int64(len(body)), ETag: hex.EncodeToString(sum[:]), Data: body, ContentType: "text/plain",
				})
				d := headDst(t, w, name)
				if d.ETag != hex.EncodeToString(sum[:]) {
					t.Fatalf("the fixture did not restore equal bytes: %+v", d)
				}
			}

			verify, err := executor.Verify(ctx,
				w.src.Objects(ctx, "media", swiftcontrol.ObjectListOptions{Deep: true}),
				w.dst.Objects(ctx, "backup", swiftcontrol.ObjectListOptions{Deep: true}))
			if err != nil {
				t.Fatal(err)
			}
			if verify.Verified != tc.verified || len(verify.Failures) != tc.failures {
				t.Fatalf("verify = %+v, want %d verified and %d failures", verify, tc.verified, tc.failures)
			}
			if tc.sabotage && !strings.Contains(verify.Failures[0].Err, "recorded a content failure") {
				t.Errorf("verify failure = %+v, want the recorded failure named", verify.Failures[0])
			}
		})
	}
}

// The spool itself: ordering, framing, forward lookup and a latched write
// failure. These are the properties the verification pass trusts without being
// able to see them, and the reason the evidence is bounded by construction — one
// record resident at a time, whatever the namespace size.
func TestCopyEvidenceSpool(t *testing.T) {
	var broken evidenceLog
	t.Cleanup(func() {
		if err := broken.close(); err != nil {
			t.Error(err)
		}
	})
	if err := broken.copied("a", "v1:3:aaa", "dddd"); err != nil {
		t.Fatal(err)
	}
	if err := broken.contentFailure("a"); err == nil {
		t.Error("a repeated name was accepted")
	}
	// That refusal latched, and every later use has to report it rather than
	// hand back partial evidence.
	if err := broken.contentFailure("z"); err == nil {
		t.Error("the spool kept writing after a refusal")
	}
	if _, err := broken.reader(); err == nil {
		t.Error("the verification pass was given a broken spool")
	}

	var log evidenceLog
	t.Cleanup(func() {
		if err := log.close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"a", "c", "e"} {
		if err := log.copied(name, "v1:1:"+name, "digest-"+name); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.contentFailure("g"); err != nil {
		t.Fatal(err)
	}
	reader, err := log.reader()
	if err != nil {
		t.Fatal(err)
	}
	// Forward only, including names the spool does not hold and names that fall
	// between two it does.
	for _, tc := range []struct {
		name, payload string
		kind          evidenceKind
	}{
		{name: "a", kind: evidenceCopied, payload: "v1:1:a\x00digest-a"},
		{name: "b", kind: evidenceNone},
		{name: "c", kind: evidenceCopied, payload: "v1:1:c\x00digest-c"},
		{name: "d", kind: evidenceNone},
		{name: "e", kind: evidenceCopied, payload: "v1:1:e\x00digest-e"},
		{name: "g", kind: evidenceContentFailure},
	} {
		kind, payload, err := reader.at(tc.name)
		if err != nil {
			t.Fatalf("at %q: %v", tc.name, err)
		}
		if kind != tc.kind || payload != tc.payload {
			t.Errorf("at %q = %d %q, want %d %q", tc.name, kind, payload, tc.kind, tc.payload)
		}
	}
	if err := reader.finish(); err != nil {
		t.Fatal(err)
	}
}

// The store's own refusal is a content disagreement too. A PUT whose body does
// not hash to what this route declared comes back 422 with nothing stored, and
// that verdict is about this name's content, so ADR 0045's same-job exclusion
// covers it. Without the record a later equal pair of listing rows verifies a
// name whose publication the store refused.
func TestDeclaredDigestRefusalTakesNoLaterCredit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		alter    bool
		copied   int64
		verified int64
		failures int
	}{
		{name: "healthy_copy_is_verified", copied: 1, verified: 1},
		{name: "refused_then_equal_is_not_verified", alter: true, failures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const name = "weird//refused"
			body := []byte("alpha")
			// The body is altered on its way to the store while the declared tag
			// and the conditional header travel untouched, so what answers is the
			// store's real verdict rather than a fixture's.
			w := newWorldWrappingDest(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					if tc.alter && r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/"+name) {
						r.Body = io.NopCloser(strings.NewReader("EVILS"))
						r.ContentLength = 5
					}
					next.ServeHTTP(rw, r)
				})
			})
			seed(w.srcMock, "media", name, body, nil)

			executor := newExecutor(t, w, nil)
			report, err := executor.Run(ctx, []plan.Item{{
				Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonUnsafeName,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Copied != tc.copied {
				t.Fatalf("report = %+v, want %d copied", report, tc.copied)
			}
			if tc.alter {
				if len(report.Failures) != 1 {
					t.Fatalf("report = %+v, want the refusal reported", report)
				}
				// Refused means nothing was stored, which is what makes the next
				// step a fixture's placement rather than a repair.
				if _, err := w.dst.ObjectDetail(ctx, "backup", name); err == nil {
					t.Fatal("a refused publication left an object behind")
				}
				sum := md5.Sum(body)
				w.dstMock.PutObject("backup", name, swifttest.Object{
					Bytes: int64(len(body)), ETag: hex.EncodeToString(sum[:]), Data: body, ContentType: "text/plain",
				})
			}

			verify, err := executor.Verify(ctx,
				w.src.Objects(ctx, "media", swiftcontrol.ObjectListOptions{Deep: true}),
				w.dst.Objects(ctx, "backup", swiftcontrol.ObjectListOptions{Deep: true}))
			if err != nil {
				t.Fatal(err)
			}
			if verify.Verified != tc.verified || len(verify.Failures) != tc.failures {
				t.Fatalf("verify = %+v, want %d verified and %d failures", verify, tc.verified, tc.failures)
			}
			if tc.alter && !strings.Contains(verify.Failures[0].Err, "recorded a content failure") {
				t.Errorf("verify failure = %+v, want the recorded failure named", verify.Failures[0])
			}
		})
	}
}

// A spool that is not the one this job wrote releases nothing. The forward read
// answers absence from the records it has passed, so a substituted record of the
// same length would look like a name the spool never held, and absence is what
// lets a name reach the pass's success paths. Validating the whole file before
// the pass begins means no credit can be counted or SAVED against evidence that
// is later contradicted — zeroing a returned count afterwards would not undo a
// persisted observation.
func TestCorruptEvidenceSpoolWithholdsCreditAndSavesNone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		corrupt  bool
		verified int64
	}{
		{name: "intact_spool_withholds_the_failed_name", verified: 0},
		{name: "corrupt_spool_credits_nothing_at_all", corrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const name = "a//bad"
			body := []byte("alpha")
			var w *world
			w = newWorldWrappingDest(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(rw, r)
					if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/"+name) {
						return
					}
					sum := md5.Sum([]byte("EVILS"))
					w.dstMock.PutObject("backup", name, swifttest.Object{
						Bytes: 5, ETag: hex.EncodeToString(sum[:]), Data: []byte("EVILS"), ContentType: "text/plain",
					})
				})
			})
			seed(w.srcMock, "media", name, body, nil)

			var saved []string
			recorder := &Recorder{
				Admit:   func(Admission) error { return nil },
				Execute: func(string, Observation) error { return nil },
				Verify: func(itemName string, o Observation) error {
					saved = append(saved, itemName+"="+string(o.Result))
					return nil
				},
			}
			executor := recordingExecutor(t, w, nil, recorder)
			report, err := executor.Run(ctx, []plan.Item{{
				Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative, Reason: plan.ReasonUnsafeName,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Copied != 0 || len(report.Failures) != 1 {
				t.Fatalf("report = %+v, want the post-PUT comparison to fail it", report)
			}
			// Equal bytes restored, so the fresh listing rows agree and only the
			// recorded failure stands between this name and verified credit.
			sum := md5.Sum(body)
			w.dstMock.PutObject("backup", name, swifttest.Object{
				Bytes: int64(len(body)), ETag: hex.EncodeToString(sum[:]), Data: body, ContentType: "text/plain",
			})
			if tc.corrupt {
				// One name swapped for another of the same length: the file's
				// size and record count are untouched, and only its digest
				// disagrees.
				if _, err := executor.evidence.file.WriteAt([]byte("b//bad"), 4); err != nil {
					t.Fatal(err)
				}
			}

			verify, err := executor.Verify(ctx,
				w.src.Objects(ctx, "media", swiftcontrol.ObjectListOptions{Deep: true}),
				w.dst.Objects(ctx, "backup", swiftcontrol.ObjectListOptions{Deep: true}))
			if tc.corrupt {
				if err == nil || !strings.Contains(err.Error(), "incomplete or corrupt") {
					t.Fatalf("Verify = %v, want the spool's integrity reported", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if verify.Verified != tc.verified {
				t.Errorf("verified = %d, want %d", verify.Verified, tc.verified)
			}
			// Neither case may leave a verified observation saved beside the
			// failed execution. The intact spool withholds it by excluding the
			// name; the corrupt one withholds it by not deciding at all.
			for _, entry := range saved {
				if strings.HasSuffix(entry, "=verified") {
					t.Errorf("a verified observation was saved: %q (all: %q)", entry, saved)
				}
			}
			if tc.corrupt && len(saved) != 0 {
				t.Errorf("a corrupt spool still produced saved observations: %q", saved)
			}
		})
	}
}
