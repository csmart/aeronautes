package copy

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// A copy skips a destination only when it is a FULL-FIDELITY replica, not merely
// content-equal (P17, ADR 0096): the §27.4 carried set — content type, content
// headers, expiry, user metadata — and link identity must match too. These cases
// hold the bytes equal (so the content check passes) and vary one fidelity
// attribute, so each asserts the fidelity gate alone.
func TestSkippableEqualRequiresFullFidelity(t *testing.T) {
	data := []byte("identical content bytes")
	for _, tc := range []struct {
		name string
		src  swifttest.Object
		dst  swifttest.Object
		want bool
	}{
		{
			name: "identical plain object is a replica",
			src:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "alice"}},
			dst:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "alice"}},
			want: true,
		},
		{
			name: "a reserved fingerprint on the destination is not a difference",
			src:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "alice"}},
			dst:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "alice", "Aeronautes-Fingerprint": "v1:anything"}},
			want: true,
		},
		{
			name: "a different content type is not a replica",
			src:  swifttest.Object{ContentType: "text/plain"},
			dst:  swifttest.Object{ContentType: "application/json"},
			want: false,
		},
		{
			name: "a different expiry is not a replica",
			src:  swifttest.Object{ContentType: "text/plain", DeleteAt: 4102444800},
			dst:  swifttest.Object{ContentType: "text/plain"},
			want: false,
		},
		{
			name: "different user metadata is not a replica",
			src:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "alice"}},
			dst:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "bob"}},
			want: false,
		},
		{
			name: "extra user metadata on the destination is not a replica",
			src:  swifttest.Object{ContentType: "text/plain"},
			dst:  swifttest.Object{ContentType: "text/plain", Meta: map[string]string{"Owner": "alice"}},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tc.src.Bytes, tc.src.ETag, tc.src.Data = int64(len(data)), etagOf(data), data
			tc.dst.Bytes, tc.dst.ETag, tc.dst.Data = int64(len(data)), etagOf(data), data
			w.srcMock.PutObject("media", "obj", tc.src)
			w.dstMock.PutObject("backup", "obj", tc.dst)
			src, err := w.src.ObjectDetail(context.Background(), "media", "obj")
			if err != nil {
				t.Fatal(err)
			}
			dst, err := w.dst.ObjectDetail(context.Background(), "backup", "obj")
			if err != nil {
				t.Fatal(err)
			}
			if got := skippableEqual(src, dst); got != tc.want {
				t.Fatalf("skippableEqual = %t, want %t (content equal, fidelity varied)", got, tc.want)
			}
		})
	}
}

// A symlink is carried as a link, never skipped on the content its following HEAD
// resolves to, so it is never a skippable replica of the followed target even when
// that target's content matches the destination (P17 link targets, ADR 0096 d2).
func TestSkippableEqualNeverSkipsASymlink(t *testing.T) {
	w := newWorld(t)
	target := []byte("the shared target bytes")
	seed(w.srcMock, "media", "target", target, nil)
	w.srcMock.PutObject("media", "link", swifttest.Object{ContentType: "text/plain", SymlinkTarget: "media/target"})
	// The destination is a plain object whose content equals the link's followed
	// target, so a content-only comparison would skip it.
	seed(w.dstMock, "backup", "link", target, nil)

	src, err := w.src.ObjectDetail(context.Background(), "media", "link")
	if err != nil {
		t.Fatal(err)
	}
	if !src.FollowedSymlink {
		t.Fatalf("source HEAD did not follow the symlink: %+v", src)
	}
	dst, err := w.dst.ObjectDetail(context.Background(), "backup", "link")
	if err != nil {
		t.Fatal(err)
	}
	if skippableEqual(src, dst) {
		t.Fatal("a symlink was skipped as equal to its followed target's content")
	}
}

// A symlink that a plain HEAD did not follow — a cross-account or dangling link —
// is the LITERAL representation: X-Symlink-Target is set but Content-Location is
// not. It carries a target/account identity that content says nothing about, so
// it is never skipped as a plain equal either (P17 link targets, ADR 0096 d2).
func TestSkippableEqualNeverSkipsALiteralSymlink(t *testing.T) {
	w := newWorld(t)
	// A cross-account link is not followed on a plain HEAD, so ObjectDetail
	// exposes it literally rather than as its target's content.
	w.srcMock.PutObject("media", "link", swifttest.Object{
		ContentType: "text/plain", SymlinkTarget: "elsewhere/target", SymlinkTargetAccount: "AUTH_other",
	})
	seed(w.dstMock, "backup", "link", []byte(""), nil)

	src, err := w.src.ObjectDetail(context.Background(), "media", "link")
	if err != nil {
		t.Fatal(err)
	}
	if src.FollowedSymlink {
		t.Fatalf("a cross-account link was followed on a plain HEAD: %+v", src)
	}
	if src.SymlinkTarget == "" {
		t.Fatalf("the literal link representation exposes no target: %+v", src)
	}
	dst, err := w.dst.ObjectDetail(context.Background(), "backup", "link")
	if err != nil {
		t.Fatal(err)
	}
	if skippableEqual(src, dst) {
		t.Fatal("a literal (unfollowed) symlink was skipped as a plain equal")
	}
}

// A literal symlink source — here a cross-account link, which a plain HEAD/GET
// does not chase (account scoping), so it is exposed by X-Symlink-Target rather
// than its target's content — must be recognised downstream and carried AS a
// link, not flattened into a plain copy of its empty body. Without recognising
// SymlinkTarget the native route would PUT a 0-byte plain object, dropping the
// link (ADR 0096 d2, P17 link targets).
func TestLiteralSymlinkSourceIsCarriedNotPlain(t *testing.T) {
	w := newWorld(t)
	w.srcMock.PutObject("media", "link", swifttest.Object{
		ContentType: "text/plain", SymlinkTarget: "other/target", SymlinkTargetAccount: "AUTH_other",
	})

	rec := newOverwriteRecorder()
	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "link", Verdict: plan.VerdictNew, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 {
		t.Fatalf("the literal link source was not carried: %+v", report)
	}
	written, serr := w.dst.SymlinkGet(context.Background(), "backup", "link")
	if serr != nil || written.SymlinkTarget != "other/target" || written.SymlinkTargetAccount != "AUTH_other" {
		t.Fatalf("the literal link source was not carried as a cross-account symlink: %+v err=%v", written, serr)
	}
}
