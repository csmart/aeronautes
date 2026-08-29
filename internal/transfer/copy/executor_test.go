package copy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
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
	t.Helper()
	w := &world{}
	build := func(mock *swifttest.Server) *swiftcontrol.Session {
		srv := httptest.NewServer(mock)
		t.Cleanup(srv.Close)
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
	w.src = build(w.srcMock)
	w.dst = build(w.dstMock)
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

func TestLargeOverwriteStageAndCommit(t *testing.T) {
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
	w.dstMock.PutObject("backup", "live.bin", swifttest.Object{
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
		Name: "live.bin", Verdict: plan.VerdictChanged, Route: plan.RouteRclone,
		SourceBytes: 5000, DestBytes: int64(len(sharedSegment)),
		DestETag: "d41d8cd98f00b204e9800998ecf8427e", // plan-time etag of the zero-byte manifest
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 1 || len(report.Failures) != 0 {
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
	// Old manifest recorded as evidence; old SHARED segments untouched;
	// the sibling still assembles.
	if report.OldManifests["live.bin"] != oldManifest {
		t.Errorf("old manifest = %v", report.OldManifests)
	}
	sibling := headDst(t, w, "sibling.bin")
	if sibling.ContentLength != int64(len(sharedSegment)) {
		t.Fatalf("shared segments were harmed by the overwrite: %+v", sibling)
	}
	// Staging litter cleaned: manifest gone, no residue; committed
	// prefix recorded so reconciliation keeps it.
	if len(report.StagedResidue) != 0 {
		t.Errorf("staged residue = %v", report.StagedResidue)
	}
	if len(report.CommittedPrefixes) != 1 || len(report.CapturedPrefixes) != 1 {
		t.Errorf("prefixes: captured %v committed %v", report.CapturedPrefixes, report.CommittedPrefixes)
	}
	if _, err := w.dst.ObjectDetail(ctx, "backup", executor.StagingPrefix()+"000001"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("staged manifest still present: %v", err)
	}
}

func TestPlainOverwriteCommitsByServerCopy(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	seed(w.srcMock, "media", "doc.txt", []byte("new content"), map[string]string{"Rev": "2"})
	seed(w.dstMock, "backup", "doc.txt", []byte("old content"), nil)
	oldETag := headDst(t, w, "doc.txt").ETag

	engine := &fakeEngine{w: w, interruptAfter: -1}
	report, err := newExecutor(t, w, engine).Run(ctx, []plan.Item{{
		Name: "doc.txt", Verdict: plan.VerdictChanged, Route: plan.RouteRclone,
		SourceBytes: 11, DestBytes: 11, DestETag: oldETag,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	d := headDst(t, w, "doc.txt")
	sum := md5.Sum([]byte("new content"))
	if d.ETag != hex.EncodeToString(sum[:]) || d.Meta["Rev"] != "2" {
		t.Errorf("committed = etag %s meta %v", d.ETag, d.Meta)
	}
	if len(report.StagedResidue) != 0 {
		t.Errorf("staging not cleaned: %v", report.StagedResidue)
	}
}

func TestInterveningWriteConflicts(t *testing.T) {
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
	if len(report.Conflicts) != 1 || report.Copied != 0 {
		t.Fatalf("report = %+v", report)
	}
	// The external write survived; staging was cleaned.
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
	// diff.txt: differs → overwrite.
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
	if report.SkippedEqual != 1 || report.Copied != 2 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if headDst(t, w, "diff.txt").ETag == "" {
	}
	sum := md5.Sum([]byte("aaaa"))
	if headDst(t, w, "diff.txt").ETag != hex.EncodeToString(sum[:]) {
		t.Error("diff.txt not overwritten")
	}
	if headDst(t, w, "fresh.txt").ContentLength != 5 {
		t.Error("fresh.txt not copied")
	}
}

func TestNativeUnsafeNames(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	hostile := []string{"/lead", "trail/", "a//b", "line\nbreak"}
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

func TestReconcileSweepsOnlyOwnedOrphans(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	executor := newExecutor(t, w, nil)
	w.dstMock.AddContainer("backup_segments", "standard")

	// A completed upload: live DLO references its captured prefix.
	completedPrefix := "video.bin/2026-08-29-0000000000000000000001/"
	w.dstMock.PutObject("backup_segments", completedPrefix+"00000000", swifttest.Object{Bytes: 4, ETag: "aaaa", Data: []byte("data")})
	w.dstMock.PutObject("backup", "video.bin", swifttest.Object{Manifest: "backup_segments/" + completedPrefix})

	// An interrupted upload: segments, no manifest anywhere.
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
	if out.SweptStaged != 1 || out.SweptSegments != 3 {
		t.Fatalf("reconcile = %+v", out)
	}
	// The completed object's segments survived (its prefix was kept).
	if headDst(t, w, "video.bin").ContentLength != 4 {
		t.Error("completed DLO broken by reconciliation")
	}
	if len(out.KeptPrefixes) != 1 || !strings.Contains(out.KeptPrefixes[0], completedPrefix) {
		t.Errorf("kept = %v", out.KeptPrefixes)
	}
	// Orphan segments gone.
	if _, err := w.dst.ObjectDetail(ctx, "backup_segments", orphanPrefix+"00000000"); !swiftcontrol.IsNotFound(err) {
		t.Error("orphan segment survived the sweep")
	}
}
