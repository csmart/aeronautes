package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
)

type JobHistoryDTO struct {
	Jobs       []JobDTO `json:"jobs"`
	NextCursor string   `json:"nextCursor"`
}

type JobItemLocationDTO struct {
	Kind       string `json:"kind"`
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Object     string `json:"object"`
	LocalPath  string `json:"localPath"`
	Display    string `json:"display"`
}

type JobItemObservationDTO struct {
	Result      string `json:"result"`
	Effect      string `json:"effect"`
	Stage       string `json:"stage"`
	Code        string `json:"code"`
	HTTPStatus  int    `json:"httpStatus"`
	CopiedBytes int64  `json:"copiedBytes"`
	CreatedAt   string `json:"createdAt"`
}

type JobItemDTO struct {
	ID           string                 `json:"id"`
	Key          string                 `json:"key"`
	KeyDisplay   string                 `json:"keyDisplay"`
	Source       JobItemLocationDTO     `json:"source"`
	Destination  JobItemLocationDTO     `json:"destination"`
	Planned      bool                   `json:"planned"`
	Admitted     bool                   `json:"admitted"`
	ListedBytes  int64                  `json:"listedBytes"`
	CreatedAt    string                 `json:"createdAt"`
	Execution    *JobItemObservationDTO `json:"execution"`
	Verification *JobItemObservationDTO `json:"verification"`
	// IssuedAt is when a deletion member's issued mark was written, before
	// its final HEAD; empty when it never was. A member issued with no
	// execution record may have been deleted (ADR 0093 decision 4).
	IssuedAt string `json:"issuedAt"`
	// ListedETag and ListedSLO are the facts a deletion member was listed
	// with beside ListedBytes.
	ListedETag string `json:"listedEtag"`
	ListedSLO  bool   `json:"listedSlo"`
}

type JobItemPageDTO struct {
	Status     string       `json:"status"`
	Items      []JobItemDTO `json:"items"`
	NextCursor string       `json:"nextCursor"`
}

type JobItemTotalsDTO struct {
	Status   string `json:"status"`
	Items    int64  `json:"items"`
	Planned  int64  `json:"planned"`
	Admitted int64  `json:"admitted"`
	Failed   int64  `json:"failed"`
	// Incomplete is the names a cancellation stopped part-way: the part of
	// Failed that is not a real failure (ADR 0129).
	Incomplete int64 `json:"incomplete,omitempty"`
	Conflicts  int64 `json:"conflicts"`
	Refused    int64 `json:"refused"`
	Unresolved int64 `json:"unresolved"`
	Unverified int64 `json:"unverified"`
	Problems   int64 `json:"problems"`
	// Issued and Deleted are a deletion job's (ADR 0093 decision 4): how
	// many members were marked issued, and how many were deleted; Unsettled
	// how many were issued and have no outcome yet.
	Issued    int64 `json:"issued"`
	Deleted   int64 `json:"deleted"`
	Unsettled int64 `json:"unsettled"`
}

func (s *JobService) History(ctx context.Context, bookmarkID, cursor string, limit int) (JobHistoryDTO, error) {
	page, err := s.jobs.History(ctx, bookmarkID, cursor, limit)
	if err != nil {
		return JobHistoryDTO{}, err
	}
	out := JobHistoryDTO{Jobs: make([]JobDTO, 0, len(page.Jobs)), NextCursor: page.NextCursor}
	for _, job := range page.Jobs {
		out.Jobs = append(out.Jobs, s.dto(job))
	}
	return out, nil
}

// Items keeps SQLite cursors as decimal strings across the JavaScript boundary.
func (s *JobService) Items(ctx context.Context, bookmarkID, jobID, cursor string, limit int) (JobItemPageDTO, error) {
	if _, err := s.jobInAccount(ctx, bookmarkID, jobID); err != nil {
		if errors.Is(err, jobs.ErrItemJobNotFound) || errors.Is(err, ErrAccountScopeChanged) {
			return JobItemPageDTO{}, err
		}
		return JobItemPageDTO{}, itemReadError(err)
	}
	var after int64
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after <= 0 || strconv.FormatInt(after, 10) != cursor {
			return JobItemPageDTO{}, jobs.ErrInvalidItemPage
		}
	}
	page, err := s.jobs.ReadItems(ctx, jobID, after, limit)
	if errors.Is(err, jobs.ErrItemRecordsNotCaptured) {
		return JobItemPageDTO{Status: "not_captured", Items: []JobItemDTO{}}, nil
	}
	if err != nil {
		return JobItemPageDTO{}, itemReadError(err)
	}
	out := JobItemPageDTO{Status: "available", Items: make([]JobItemDTO, 0, len(page.Items))}
	if page.NextCursor != 0 {
		out.NextCursor = strconv.FormatInt(page.NextCursor, 10)
	}
	for _, item := range page.Items {
		out.Items = append(out.Items, jobItemDTO(item))
	}
	return out, nil
}

func (s *JobService) ItemTotals(ctx context.Context, bookmarkID, jobID string) (JobItemTotalsDTO, error) {
	if _, err := s.jobInAccount(ctx, bookmarkID, jobID); err != nil {
		if errors.Is(err, jobs.ErrItemJobNotFound) || errors.Is(err, ErrAccountScopeChanged) {
			return JobItemTotalsDTO{}, err
		}
		return JobItemTotalsDTO{}, itemReadError(err)
	}
	totals, err := s.jobs.ReadItemTotals(ctx, jobID)
	if errors.Is(err, jobs.ErrItemRecordsNotCaptured) {
		return JobItemTotalsDTO{Status: "not_captured"}, nil
	}
	if err != nil {
		return JobItemTotalsDTO{}, itemReadError(err)
	}
	return JobItemTotalsDTO{Status: "available", Items: totals.Items, Planned: totals.Planned,
		Admitted: totals.Admitted, Failed: totals.Failed, Incomplete: totals.Incomplete, Conflicts: totals.Conflicts,
		Refused: totals.Refused, Unresolved: totals.Unresolved, Unverified: totals.Unverified, Problems: totals.Problems,
		Issued: totals.Issued, Deleted: totals.Deleted, Unsettled: totals.Unsettled}, nil
}

func itemReadError(err error) error {
	if errors.Is(err, jobs.ErrInvalidItemPage) {
		return jobs.ErrInvalidItemPage
	}
	if errors.Is(err, jobs.ErrItemJobNotFound) {
		return jobs.ErrItemJobNotFound
	}
	return errors.New("item records are unavailable; retained outcomes cannot be confirmed")
}

func jobItemDTO(item jobs.ItemRecord) JobItemDTO {
	out := JobItemDTO{ID: strconv.FormatInt(item.ID, 10), Key: item.Key, KeyDisplay: strconv.Quote(item.Key),
		Source: jobItemLocationDTO(item.Source), Destination: jobItemLocationDTO(item.Destination),
		Planned: item.Planned, Admitted: item.Admitted, ListedBytes: item.ListedBytes, ListedETag: item.ListedETag, ListedSLO: item.ListedSLO,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano),
		Execution: jobItemObservationDTO(item.Execution), Verification: jobItemObservationDTO(item.Verification)}
	if !item.IssuedAt.IsZero() {
		out.IssuedAt = item.IssuedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func jobItemLocationDTO(location jobs.ItemLocation) JobItemLocationDTO {
	display := strconv.Quote(location.LocalPath)
	if location.Kind == jobs.ItemSwift {
		// Quote each field separately so a separator or control cannot hide its boundary.
		display = strconv.Quote(location.BookmarkID) + " / " + strconv.Quote(location.Container) + " / " + strconv.Quote(location.Object)
	}
	return JobItemLocationDTO{Kind: string(location.Kind), BookmarkID: location.BookmarkID,
		Container: location.Container, Object: location.Object, LocalPath: location.LocalPath, Display: display}
}

func jobItemObservationDTO(observation *jobs.RecordedObservation) *JobItemObservationDTO {
	if observation == nil {
		return nil
	}
	return &JobItemObservationDTO{Result: string(observation.Result), Effect: string(observation.Effect),
		Stage: string(observation.Diagnostic.Stage), Code: string(observation.Diagnostic.Code), HTTPStatus: observation.Diagnostic.HTTPStatus,
		CopiedBytes: observation.CopiedBytes, CreatedAt: observation.CreatedAt.UTC().Format(time.RFC3339Nano)}
}
