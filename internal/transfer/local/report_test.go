package local

import (
	"errors"
	"fmt"
	"testing"
)

func TestPostCommitIssuesKeepBoundedExamples(t *testing.T) {
	report := &Report{Done: 7, Bytes: 70, Failures: []Failure{{Name: "failed-transfer"}}}
	for index := range 203 {
		report.postCommitIssue(fmt.Sprintf("live/%03d", index), "staging-cleanup", errors.New("delete refused"))
	}
	if report.PostCommitIssues != 203 || len(report.PostCommitIssueSamples) != 200 {
		t.Fatalf("issue total or bounded examples changed: total=%d examples=%d", report.PostCommitIssues, len(report.PostCommitIssueSamples))
	}
	for index, issue := range report.PostCommitIssueSamples {
		if issue != (Failure{Name: fmt.Sprintf("live/%03d", index), Stage: "staging-cleanup", Err: "delete refused"}) {
			t.Errorf("issue identity changed at %d: %+v", index, issue)
		}
	}
	if report.Done != 7 || report.Bytes != 70 || len(report.Failures) != 1 || report.Failures[0].Name != "failed-transfer" {
		t.Errorf("post-commit issues changed transfer results: %+v", report)
	}
}
