package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

func rerunFixture(t *testing.T, h *Harness, src, dst string, version int) (jobs.Job, *jobs.Store) {
	t.Helper()
	store := jobs.NewStore(h.database)
	job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: version,
		Source: src + "/scope/", Destination: dst + "/scope/",
		SourceBookmarkID: h.Primary.BookmarkID, DestBookmarkID: h.Secondary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	return job, store
}

func rerunIntent(h *Harness, src, dst, key string) jobs.ItemIntent {
	return jobs.ItemIntent{ItemIdentity: jobs.ItemIdentity{Key: key,
		Source:      jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: src, Object: "scope/" + key},
		Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: dst, Object: "scope/" + key}}, Planned: true, Admitted: true}
}

func finishRerunFixture(t *testing.T, h *Harness, store *jobs.Store, job jobs.Job) {
	t.Helper()
	if job.ItemRecordsVersion == 0 {
		if err := store.Transition(t.Context(), job.ID, jobs.StateCancelled); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := store.FinishFromItems(t.Context(), job.ID, jobs.Completion{State: jobs.StateCancelled,
		Audit: audit.Event{Operation: "transfer.fixture", Resource: "constructed prior facts", Result: audit.ResultFailed}}, audit.NewStore(h.database)); err != nil {
		t.Fatal(err)
	}
}

func rerunItemsRefuse(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "rerun-source"), h.Container(h.Secondary, "rerun-destination")
	segments, aliases := h.Container(h.Secondary, "rerun-segments"), h.Container(h.Secondary, "rerun-aliases")
	h.Seed(h.Primary, src, "scope/new\nname", []byte("must remain at source"), nil)
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	body := []byte("shared segment bytes")
	h.Seed(h.Secondary, segments, "shared/0", body, nil)
	h.Seed(h.Secondary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Secondary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(body), len(body)))
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	sourceTrace, targetTrace := copyCompletionProxy(t, h, h.Primary, ""), copyCompletionProxy(t, h, h.Secondary, "")
	var credited *Receipt
	for _, reason := range []string{"not captured", "unresolved", "unverified"} {
		version := 1
		if reason == "not captured" {
			version = 0
		}
		job, store := rerunFixture(t, h, src, dst, version)
		if version == 1 {
			intent := rerunIntent(h, src, dst, "prior\nname")
			if err := store.AdmitItems(t.Context(), job.ID, []jobs.ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			if reason == "unverified" {
				if err := store.RecordExecution(t.Context(), job.ID, intent.Key, jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := store.RecoveryAdd(t.Context(), jobs.RecoveryRecord{JobID: job.ID, BookmarkID: h.Secondary.BookmarkID,
			Kind: jobs.RecoveryCaptured, Container: segments, Prefix: "shared/"}); err != nil {
			t.Fatal(err)
		}
		finishRerunFixture(t, h, store, job)
		request := itemStorageRequest{Legacy: job.ID}
		if version == 1 {
			request.Recorded, request.Legacy = []string{job.ID}, ""
		}
		before := readInterruption(t, h.database, store, request)
		beforeJobs, err := store.List(t.Context(), jobs.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		from, to := sourceTrace.requestCount(), targetTrace.requestCount()
		receipt := h.RerunJob(t.Context(), job.ID)
		if receipt.JobID() != "" {
			h.AwaitJob(receipt.JobID())
		}
		if receipt.Err() == nil || receipt.JobID() != "" || !strings.Contains(receipt.Err().Error(), reason) {
			t.Fatalf("%s rerun admitted work: %q %v", reason, receipt.JobID(), receipt.Err())
		}
		if sourceTrace.requestCount() != from || targetTrace.requestCount() != to {
			t.Fatalf("%s rerun contacted a storage account", reason)
		}
		afterJobs, err := store.List(t.Context(), jobs.Filter{})
		if err != nil || !reflect.DeepEqual(beforeJobs, afterJobs) || !reflect.DeepEqual(before, readInterruption(t, h.database, store, request)) {
			t.Fatalf("%s rerun changed jobs, retained item facts, recovery or audits: %v", reason, err)
		}
		credited = receipt
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	credited.CreditRefusal()
}

func TestRerunItemsRefuseMock(t *testing.T)      { rerunItemsRefuse(t, findMockHarness(t)) }
func TestRerunItemsRefuseRealSwift(t *testing.T) { rerunItemsRefuse(t, New(t)) }

func rerunItemsFreshPlan(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "rerun-source"), h.Container(h.Secondary, "rerun-destination")
	priorKey := "prior\nname"
	h.Seed(h.Primary, src, "scope/"+priorKey, []byte("old"), nil)
	h.Seed(h.Secondary, dst, "scope/"+priorKey, []byte("old"), nil)
	h.Seed(h.Secondary, dst, "unselected", []byte("destination sentinel"), nil)
	job, store := rerunFixture(t, h, src, dst, 1)
	intent := rerunIntent(h, src, dst, priorKey)
	if err := store.AdmitItems(t.Context(), job.ID, []jobs.ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordExecution(t.Context(), job.ID, priorKey, jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}); err != nil {
		t.Fatal(err)
	}
	finishRerunFixture(t, h, store, job)
	prior := readInterruption(t, h.database, store, itemStorageRequest{Recorded: []string{job.ID}}).Items
	newKey, body := "current\nname", []byte("new source after historical observation")
	h.Seed(h.Primary, src, "scope/"+newKey, body, nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	run := h.RerunJob(t.Context(), job.ID)
	if run.Err() != nil || run.JobID() == "" || run.JobID() == job.ID {
		t.Fatalf("inspectable prior facts did not start a fresh job: %q %v", run.JobID(), run.Err())
	}
	result := h.AwaitJob(run.JobID())
	if result.State != "complete" || result.Counters.ObjectsCopied != 1 || result.Counters.ObjectsPlanned != 1 || result.Source != job.Source || result.Destination != job.Destination {
		t.Fatalf("rerun did not plan the current scope: %+v", result)
	}
	afterB := h.SnapshotNamespace(h.Secondary)
	got, ok := afterB.Containers[dst].Objects["scope/"+newKey]
	if !ok || got.Size != int64(len(body)) || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || got.Class != "plain" {
		t.Fatalf("fresh source bytes were not copied exactly: %+v", got)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, afterB, dst+"/scope/"+newKey)
	if !reflect.DeepEqual(prior, readInterruption(t, h.database, store, itemStorageRequest{Recorded: []string{job.ID}}).Items) {
		t.Fatal("rerun rewrote the historical admission or observations")
	}
	run.CreditHappy()
}

func TestRerunItemsFreshPlanMock(t *testing.T)      { rerunItemsFreshPlan(t, findMockHarness(t)) }
func TestRerunItemsFreshPlanRealSwift(t *testing.T) { rerunItemsFreshPlan(t, New(t)) }
