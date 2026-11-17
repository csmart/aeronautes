package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// ADR 0122 decision 12: after the confirmation the engine compares each name
// as rclone copy does, and what it says is each file's outcome. These are the
// pinned engine's lines (docs/rclone.md): replaced, left because the size and
// time are the same, and left after only its time was set.
func TestAConfirmedDownloadTakesEachFilesOutcomeFromTheEnginesComparison(t *testing.T) {
	s, mock := session(t)
	for _, name := range []string{"scope/differs.txt", "scope/same.txt", "scope/touched.txt", "scope/new.txt"} {
		seed(mock, "media", name, "object bytes")
	}
	dest := t.TempDir()
	for _, name := range []string{"differs.txt", "same.txt", "touched.txt"} {
		writeFile(t, dest, name, "local bytes")
	}
	refused, err := PreflightDownloadMembersInto(t.Context(), s, "media", "scope/", testDestination(t, dest), 0, nil)
	if err != nil || !refused.Rejected() || refused.View().Replaceable != 3 {
		t.Fatalf("unconfirmed preflight = %+v %v, want three refusals it could replace", refused, err)
	}
	preflight, err := PreflightDownloadMembersIntoReplacing(t.Context(), s, "media", "scope/", testDestination(t, dest), 0, nil)
	if err != nil || preflight.Rejected() || !preflight.Replacing || preflight.View().Replacing != 3 {
		t.Fatalf("replacing preflight = %+v %v, want every file admitted and three that may be replaced", preflight, err)
	}
	engine := &scriptedEngine{run: func(_ context.Context, _, _ string, _ []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		onEvent(rclone.Event{Level: "info", Object: "differs.txt", Msg: "Copied (replaced existing)"})
		onEvent(rclone.Event{Level: "debug", Object: "same.txt", Msg: "Unchanged skipping"})
		onEvent(rclone.Event{Level: "info", Object: "touched.txt", Msg: "Updated modification time in destination"})
		onEvent(rclone.Event{Level: "debug", Object: "touched.txt", Msg: "Unchanged skipping"})
		onEvent(rclone.Event{Level: "info", Object: "new.txt", Msg: "Copied (new)"})
		return &rclone.Result{}, nil
	}}
	var kept downloadRecords
	report, err := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, kept.recorder())
	if err != nil || len(report.Failures) != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if report.Done != 2 || report.Replaced != 1 || report.Same != 2 || report.Verified != 4 {
		t.Fatalf("done %d, replaced %d, same %d, verified %d; want 2, 1, 2 and 4", report.Done, report.Replaced, report.Same, report.Verified)
	}
	for name, want := range map[string]transfercopy.Result{
		"scope/differs.txt": transfercopy.ResultCopied, "scope/same.txt": transfercopy.ResultSame,
		"scope/touched.txt": transfercopy.ResultSame, "scope/new.txt": transfercopy.ResultCopied,
	} {
		if got := kept.executed[name]; got.Result != want || !kept.verified[name] {
			t.Errorf("%s recorded %+v, verified %v; want %s and verified", name, got, kept.verified[name], want)
		}
	}
}

// Replacing a file is not replacing a folder or a link: those stay refused
// when the operator would confirm, and a report that refuses one of them
// does not offer to replace the rest either.
func TestAConfirmedDownloadStillRefusesWhatIsNotAFile(t *testing.T) {
	s, mock := session(t)
	for _, name := range []string{"scope/a.txt", "scope/b.txt", "scope/c.txt"} {
		seed(mock, "media", name, "object bytes")
	}
	dest := t.TempDir()
	if err := os.Mkdir(filepath.Join(dest, "a.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), filepath.Join(dest, "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dest, "c.txt", "local bytes")
	refused, err := PreflightDownloadMembersInto(t.Context(), s, "media", "scope/", testDestination(t, dest), 0, nil)
	if err != nil || refused.View().Replaceable != 0 {
		t.Fatalf("unconfirmed preflight = %+v %v, want nothing offered beside a folder and a link", refused, err)
	}
	preflight, err := PreflightDownloadMembersIntoReplacing(t.Context(), s, "media", "scope/", testDestination(t, dest), 0, nil)
	if err != nil || !preflight.Rejected() {
		t.Fatalf("replacing preflight = %+v %v, want the folder and the link refused", preflight, err)
	}
	refusedNames := map[string]bool{}
	for _, problem := range preflight.Problems {
		if problem.Reject {
			refusedNames[problem.Name] = true
		}
	}
	if !refusedNames["scope/a.txt"] || !refusedNames["scope/b.txt"] || refusedNames["scope/c.txt"] {
		t.Fatalf("refused %v, want the folder and the link and not the file", refusedNames)
	}
}

// The application's own route does not compare: confirmed, it renames its
// temporary over what is there; unconfirmed, its link does not replace.
func TestTheApplicationsOwnDownloadReplacesOnlyWhenConfirmed(t *testing.T) {
	const name = "scope/a：b.txt" // the full-width colon is reserved to the engine
	for _, replacing := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "unconfirmed"}[replacing], func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", name, "native bytes")
			dest := t.TempDir()
			writeFile(t, dest, "a：b.txt", "the operator's own file")
			item := DownloadItem{Name: name, RelPath: "a：b.txt", Size: int64(len("native bytes")), Route: "native"}
			outcome := nativeDownload(t.Context(), s, "media", item, testDestination(t, dest), replacing)
			want, holds := transfercopy.ResultConflict, "the operator's own file"
			if replacing {
				want, holds = transfercopy.ResultCopied, "native bytes"
			}
			if outcome.Result != want || len(outcome.issues) != 0 || readLocal(t, filepath.Join(dest, "a：b.txt")) != holds {
				t.Fatalf("outcome %+v and the file holds %q; want %s and %q", outcome.Observation, readLocal(t, filepath.Join(dest, "a：b.txt")), want, holds)
			}
			for _, entry := range folderNames(t, dest) {
				if strings.HasPrefix(entry, downloadPartPrefix) {
					t.Fatalf("a temporary is left: %v", folderNames(t, dest))
				}
			}
		})
	}
}

// A confirmed upload's engine compares too: a file it found the same is
// verified and not copied, and a large one is not published again, because
// the static manifest is written only for a file the engine wrote.
func TestAConfirmedUploadLeavesWhatTheEngineFoundTheSame(t *testing.T) {
	mock := segmentMock()
	s := servedSession(t, mock)
	if err := s.Authenticate(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	big, small := writeFile(t, dir, "big", "0123456789abcdefghijKLMNO"), writeFile(t, dir, "small", "tiny")
	preflight := staticPlan(t, s, true, big, small)
	engine := &scriptedEngine{run: func(_ context.Context, _, _ string, _ []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		onEvent(rclone.Event{Level: "debug", Object: "big", Msg: "Unchanged skipping"})
		onEvent(rclone.Event{Level: "info", Object: "small", Msg: "Updated modification time in destination"})
		onEvent(rclone.Event{Level: "debug", Object: "small", Msg: "Unchanged skipping"})
		return &rclone.Result{}, nil
	}}
	mark := len(mock.RequestLog())
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil)
	if err != nil || len(report.Failures) != 0 || report.Done != 0 || report.Same != 2 || report.Verified != 2 {
		t.Fatalf("report = %+v, %v; want both files the same and verified, none copied", report, err)
	}
	if log := since(mock, mark); count(log, "PUT ") != 0 {
		t.Fatalf("a file the engine left was written to: %q", log)
	}
}
