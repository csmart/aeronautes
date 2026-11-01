package systemtest

import (
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0088 decisions 4 and 5 through the bridge: the notice reads the
// undisclosed interruptions with a total and honest per-entry facts, and the
// dismissal records disclosure for exactly the entries named, leaving the rest
// to return. It reuses the recovery-status harness: like counting retained
// records, telling the operator what a restart interrupted must not depend on
// Swift or credential authority. Local-db, no SAIO.
func TestInterruptedDisclosuresNoticeAndDismissal(t *testing.T) {
	h, store, _ := recoveryStatusHarness(t)
	ctx := t.Context()

	download, err := store.Create(ctx, jobs.Job{Type: jobs.TypeDownload, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := store.Create(ctx, jobs.Job{Type: jobs.TypeUpload, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}

	page, read := h.InterruptedDisclosures(ctx, 0)
	if read.Err() != nil || page.Total != 2 || len(page.Entries) != 2 {
		t.Fatalf("notice = %+v, %v", page, read.Err())
	}
	byID := map[string]any{}
	for _, e := range page.Entries {
		byID[e.JobID] = e
		if e.InterruptedState != "queued" || e.Attempted {
			t.Fatalf("a queued interruption with no recorded work must read as not attempted: %+v", e)
		}
		if e.ItemsCaptured {
			t.Fatalf("a queued job kept no item ledger: %+v", e)
		}
		// The local-residue facet must survive the bridge honestly: a download
		// places bytes in the operator's own directory, an upload does not.
		switch e.Type {
		case "download":
			if !e.PlacesLocalResidue {
				t.Fatalf("a download places local residue: %+v", e)
			}
		case "upload":
			if e.PlacesLocalResidue {
				t.Fatalf("an upload does not place local residue: %+v", e)
			}
		default:
			t.Fatalf("unexpected interrupted type %q", e.Type)
		}
	}
	if _, ok := byID[download.ID]; !ok {
		t.Fatalf("the download interruption is missing from the notice")
	}
	if _, ok := byID[upload.ID]; !ok {
		t.Fatalf("the upload interruption is missing from the notice")
	}

	result, mutate := h.DismissInterrupted(ctx, []string{download.ID})
	if mutate.Err() != nil || result.Dismissed != 1 {
		t.Fatalf("dismissal = %+v, %v", result, mutate.Err())
	}
	after, afterRead := h.InterruptedDisclosures(ctx, 0)
	if afterRead.Err() != nil || after.Total != 1 || len(after.Entries) != 1 || after.Entries[0].JobID != upload.ID {
		t.Fatalf("after dismissing one, the rest must return: %+v, %v", after, afterRead.Err())
	}

	read.CreditRead()
	mutate.CreditApplied()
}

// ADR 0088 decision 4 (read) and decision 7 (dismissal): an unavailable store
// is reported as a failure by both, never as "no interruptions" or a silent
// success.
func TestInterruptedDisclosuresRefuseWhenUnavailable(t *testing.T) {
	h, _, database := recoveryStatusHarness(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	page, read := h.InterruptedDisclosures(ctx, 0)
	if read.Err() == nil || page.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("an unavailable read claimed a page: %+v, %v", page, read.Err())
	}
	result, mutate := h.DismissInterrupted(ctx, []string{"any"})
	if mutate.Err() == nil || result.Dismissed != 0 {
		t.Fatalf("an unavailable dismissal claimed success: %+v, %v", result, mutate.Err())
	}

	read.CreditRefusal()
	mutate.CreditRefusal()
}
