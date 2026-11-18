package app

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func TestNativeDownloadCleanupKeepsCopiedOutcome(t *testing.T) {
	h := newHarness(t)
	base := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 21}
	bm := h.ensureBookmark(t)
	job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, Counters: base, DestBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	report := &local.Report{Done: 1, Verified: 1, Bytes: 21, NativeRouted: 1, PostCommitIssues: 1,
		PostCommitIssueSamples: []local.Failure{{Name: "selected", Stage: "temporary-cleanup", Err: "remove refused"}}}
	h.transfer.runLocal(t.Context(), job.ID, "", 0, base, func(context.Context, func(int64, int64, int64, int64)) (*local.Report, error) {
		return report, nil
	})
	visible, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), job.ID)
	want := base
	want.ObjectsCopied, want.ObjectsVerified, want.BytesCopied, want.NativeRouted = 1, 1, 21, 1
	if err != nil || visible.State != "partial" || visible.Counters != want {
		t.Fatalf("native cleanup changed witnessed copies: %+v %v", visible, err)
	}
	for _, reason := range []string{"1 post-commit issues", "selected", "temporary-cleanup", "remove refused"} {
		if !strings.Contains(visible.Error, reason) {
			t.Errorf("native cleanup lost %q: %q", reason, visible.Error)
		}
	}
	audits, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
	if err != nil || len(audits) != 1 || audits[0].Operation != "transfer.local.partial" || audits[0].Resource != "job "+job.ID || audits[0].Result != "verify_mismatch" || audits[0].Summary != "" {
		t.Errorf("native cleanup audit differs: %+v %v", audits, err)
	}
}
