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

// JobItemPredecessorDTO is what was live at an item's destination when this
// item was about to be published over it (ADR 0081 decision 7).
// It is an observation and never an outcome, in two ways. It is read before
// the publication, so a record exists for items whose publication was then
// refused, failed or never attempted, and the item's own outcome is the only
// thing that says which happened. And where a publication did land, this
// contract admits a racing writer, so the object replaced may not be the one
// described.
// The entries themselves are deliberately not here. A page is bounded by its
// item limit and one item can hold thousands of entries, so the page states
// how many were read and how many are held, and the entries stay in the
// durable record an explicit cleanup would read (K-PERF-001).
type JobItemPredecessorDTO struct {
	// Present is false when the name held nothing at that moment although
	// the preflight found it live.
	Present      bool   `json:"present"`
	ETag         string `json:"etag"`
	Length       int64  `json:"length"`
	LastModified string `json:"lastModified"`
	Class        string `json:"class"`
	// Prefix is the replaced dynamic manifest's container and prefix, which
	// stays listable afterwards, empty for anything else.
	Prefix string `json:"prefix"`
	// Counted is how many entries were read; Named is how many of them the
	// record holds, which is fewer only for a listed prefix, where the
	// per-item cap stopped naming them.
	Counted int `json:"counted"`
	Named   int `json:"named"`
	// Inline, Ranged and Nested total the entries that are not simple
	// whole-object remote parts. Which entry had which shape is in the
	// record itself, not here: inline data has no remote object at all, a
	// ranged entry references a containing object, and a nested entry names
	// another manifest.
	Inline int `json:"inline"`
	Ranged int `json:"ranged"`
	Nested int `json:"nested"`
	// Exact is true only when the entries read are the whole set the
	// manifest named. A listed prefix is a lower bound.
	Exact bool `json:"exact"`
	// Unread is why no inventory could be taken, empty when one was.
	Unread    string `json:"unread"`
	CreatedAt string `json:"createdAt"`
}

// JobItemSuccessorDTO is what a deletion member's verifying read found at
// the name afterwards (ADR 0093 decision 4): nothing, or something with its
// class, length and entity tag. It is an observation, never an outcome: the
// item's execution says whether the member was deleted, and a successor
// beside a deletion says the name did not stay empty.
type JobItemSuccessorDTO struct {
	Present bool   `json:"present"`
	Class   string `json:"class"`
	Length  int64  `json:"length"`
	ETag    string `json:"etag"`
	// Role names the read that saw it: "after", the verifying read after the
	// DELETE, or "comparison", the comparison HEAD that found the member
	// changed since it was listed before any DELETE.
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

// JobItemBaselineDTO is the identity a deletion member's comparison was made
// against (ADR 0093 decision 3): the listed row as a plain object for an
// ordinary member, or the HEAD taken first for a member listed as zero bytes
// or as a static large object.
type JobItemBaselineDTO struct {
	Class  string `json:"class"`
	Length int64  `json:"length"`
	ETag   string `json:"etag"`
}

type JobItemDTO struct {
	ID                  string                 `json:"id"`
	Key                 string                 `json:"key"`
	KeyDisplay          string                 `json:"keyDisplay"`
	Source              JobItemLocationDTO     `json:"source"`
	Destination         JobItemLocationDTO     `json:"destination"`
	Staging             *JobItemLocationDTO    `json:"staging"`
	Planned             bool                   `json:"planned"`
	Admitted            bool                   `json:"admitted"`
	ListedBytes         int64                  `json:"listedBytes"`
	CreatedAt           string                 `json:"createdAt"`
	Execution           *JobItemObservationDTO `json:"execution"`
	Verification        *JobItemObservationDTO `json:"verification"`
	PublicationRequired bool                   `json:"publicationRequired"`
	Publication         *JobItemObservationDTO `json:"publication"`
	// Predecessor is set on an item that was ABOUT to publish over a live
	// name, read before the attempt. It says nothing about whether the
	// attempt happened: Execution and Verification do.
	Predecessor *JobItemPredecessorDTO `json:"predecessor"`
	// IssuedAt is when a deletion member's issued mark was written, before
	// its final HEAD; empty when it never was. A member issued with no
	// execution record may have been deleted (ADR 0093 decision 4).
	IssuedAt string `json:"issuedAt"`
	// Successor is what a deletion member's read found at the name, the
	// verifying read's after the DELETE or the comparison HEAD's when the
	// member was found changed; nil when nothing was recorded.
	Successor *JobItemSuccessorDTO `json:"successor"`
	// ListedETag and ListedSLO are the facts a deletion member was listed
	// with beside ListedBytes; Baseline is the identity its comparison was
	// made against, nil when it never reached one.
	ListedETag string              `json:"listedEtag"`
	ListedSLO  bool                `json:"listedSlo"`
	Baseline   *JobItemBaselineDTO `json:"baseline"`
}

type JobItemPageDTO struct {
	Status     string       `json:"status"`
	Items      []JobItemDTO `json:"items"`
	NextCursor string       `json:"nextCursor"`
}

type JobItemTotalsDTO struct {
	Status     string `json:"status"`
	Items      int64  `json:"items"`
	Planned    int64  `json:"planned"`
	Admitted   int64  `json:"admitted"`
	Failed     int64  `json:"failed"`
	Conflicts  int64  `json:"conflicts"`
	Refused    int64  `json:"refused"`
	Unresolved int64  `json:"unresolved"`
	Unverified int64  `json:"unverified"`
	Problems   int64  `json:"problems"`
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
		out.Jobs = append(out.Jobs, jobDTO(job))
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
		Admitted: totals.Admitted, Failed: totals.Failed, Conflicts: totals.Conflicts,
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
		Execution: jobItemObservationDTO(item.Execution), Verification: jobItemObservationDTO(item.Verification),
		PublicationRequired: item.PublicationRequired, Publication: jobItemObservationDTO(item.Publication)}
	if item.Staging != nil {
		staging := jobItemLocationDTO(*item.Staging)
		out.Staging = &staging
	}
	out.Predecessor = jobItemPredecessorDTO(item.Predecessor)
	if !item.IssuedAt.IsZero() {
		out.IssuedAt = item.IssuedAt.UTC().Format(time.RFC3339Nano)
	}
	if item.Successor != nil {
		role := item.Successor.Role
		if role == "" {
			role = jobs.SuccessorAfter
		}
		out.Successor = &JobItemSuccessorDTO{Present: item.Successor.Present, Class: item.Successor.Class,
			Length: item.Successor.Length, ETag: item.Successor.ETag, Role: role,
			CreatedAt: item.Successor.CreatedAt.UTC().Format(time.RFC3339Nano)}
	}
	if item.Baseline != nil {
		out.Baseline = &JobItemBaselineDTO{Class: item.Baseline.Class, Length: item.Baseline.Length, ETag: item.Baseline.ETag}
	}
	return out
}

func jobItemPredecessorDTO(observed *jobs.ItemPredecessor) *JobItemPredecessorDTO {
	if observed == nil {
		return nil
	}
	out := &JobItemPredecessorDTO{Present: observed.Present, ETag: observed.ETag, Length: observed.Length,
		Class: observed.Class, Prefix: observed.Prefix, Counted: observed.Counted, Named: observed.Named,
		Inline: observed.Inline, Ranged: observed.Ranged, Nested: observed.Nested, Exact: observed.Exact,
		Unread: observed.Unread, CreatedAt: observed.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if !observed.LastModified.IsZero() {
		out.LastModified = observed.LastModified.UTC().Format(time.RFC3339Nano)
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
