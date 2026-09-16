package local

import (
	"fmt"
	"strings"
	"testing"
)

func TestDownloadPreflightViewPreservesCounts(t *testing.T) {
	report := &DownloadPreflight{TotalBytes: 9}
	for i := range maxFailures + 7 {
		report.Items = append(report.Items, DownloadItem{Name: fmt.Sprintf("scope/n-%03d", i), RelPath: fmt.Sprintf("n-%03d", i), Size: 1})
		report.Objects++
		report.TotalBytes++
	}
	view := report.View()
	if view.Objects != int64(maxFailures+7) || view.TotalBytes != report.TotalBytes || view.ProblemCount != 0 || view.Rejected {
		t.Fatalf("view rebuilt counts from the sample: %+v", view)
	}
	if len(view.ItemSamples) != maxFailures || len(view.ProblemSamples) != 0 {
		t.Fatalf("sample bound = items %d problems %d", len(view.ItemSamples), len(view.ProblemSamples))
	}
	if int64(len(view.ItemSamples)) == view.Objects {
		t.Fatal("sample length equalled the object count; the bound did not apply")
	}
	view.ItemSamples[0].Name = "mutated"
	if report.Items[0].Name == "mutated" {
		t.Fatal("mutating the view changed the execution inventory")
	}
}

func TestDownloadPreflightViewRejectedFromCompleteProblems(t *testing.T) {
	report := &DownloadPreflight{Objects: 1, Items: []DownloadItem{{Name: "scope/a", RelPath: "a", Size: 1}}}
	for i := range maxFailures {
		report.Problems = append(report.Problems, Problem{Name: fmt.Sprintf("warn-%d", i), Kind: "portability", Detail: "warning", Reject: false})
	}
	report.Problems = append(report.Problems, Problem{Name: "scope/hidden", Kind: "case-fold", Detail: "tail refusal", Reject: true})
	view := report.View()
	if !view.Rejected || view.ProblemCount != int64(maxFailures+1) || len(view.ProblemSamples) != maxFailures {
		t.Fatalf("rejected flag or problem count followed the sample: %+v", view)
	}
	for _, problem := range view.ProblemSamples {
		if problem.Reject {
			t.Fatalf("sample contained a rejecting row, so the test would not prove the field: %+v", problem)
		}
	}
}

func TestCollisionDetailKeepsTrueCount(t *testing.T) {
	twins := make([]string, maxFailures+3)
	for i := range twins {
		twins[i] = fmt.Sprintf("Name-%d", i)
	}
	detail := collisionDetail("case-fold", twins)
	if !strings.HasPrefix(detail, "203 names collide on case-insensitive filesystems:") {
		t.Fatalf("detail lost the true count: %s", detail)
	}
	if strings.Contains(detail, "Name-200") {
		t.Fatalf("quoted names were not bounded: %s", detail)
	}
	if !strings.Contains(detail, "Name-199") {
		t.Fatalf("sample omitted the last quoted name: %s", detail)
	}
}
