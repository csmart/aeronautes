package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// SelectedDeleteRequestDTO starts the deletion of the selected rows as one
// job (ADR 0093 decisions 2 and 8): the scope the view was built under, the
// container, the member rows as the working list carries them, objects and
// folders, whether each large object loses its segments too, and the typed
// confirmation, which is "delete " and the number of members.
type SelectedDeleteRequestDTO struct {
	Scope     ListingScopeDTO `json:"scope"`
	Container string          `json:"container"`
	Members   []ListingRowDTO `json:"members"`
	// Segments asks that each large object the job deletes lose its segments
	// after its manifest, as the folder deletion's option does (ADR 0119,
	// ADR 0120); the confirmation then ends " and segments".
	Segments bool   `json:"segments"`
	Confirm  string `json:"confirm"`
}

// selectionCeiling is the most members one selected-set deletion admits:
// the selection ceiling of ADR 0072 decision 4, the operator's download
// object ceiling setting, which ADR 0093 decision 2 reuses for selected
// sets. The shell wires the setting; without it the default applies.
func (s *DeleteService) selectionCeiling(ctx context.Context) int {
	if s.ceiling != nil {
		if n := s.ceiling(ctx); n > 0 {
			return n
		}
	}
	return defaultDownloadObjectCeiling
}

// WireDeleteSettings gives the deletion service the operator's selection
// ceiling, the download object ceiling of ADR 0072 decision 4.
func WireDeleteSettings(s *DeleteService, settings *SettingsService) {
	s.ceiling = func(ctx context.Context) int {
		current, err := settings.Get(ctx)
		if err != nil {
			return 0
		}
		return current.DownloadObjectCeiling
	}
}

// selectedConfirmation is what the operator types to start a selected-set
// deletion (ADR 0093 decision 9).
func selectedConfirmation(req SelectedDeleteRequestDTO) string {
	words := "delete " + strconv.Itoa(len(req.Members))
	if req.Segments {
		return words + " and segments"
	}
	return words
}

func (s *DeleteService) validateSelected(ctx context.Context, req SelectedDeleteRequestDTO) error {
	if req.Container == "" {
		return errors.New("a selected-set deletion needs its container")
	}
	if len(req.Members) == 0 {
		return errors.New("nothing is selected; a selected-set deletion needs at least one object or folder")
	}
	if ceiling := s.selectionCeiling(ctx); len(req.Members) > ceiling {
		return fmt.Errorf("%d members exceed the selection ceiling of %d; narrow the selection", len(req.Members), ceiling)
	}
	seen := make(map[string]bool, len(req.Members))
	folders := map[string]bool{}
	for _, member := range req.Members {
		switch {
		case member.Name == "":
			return errors.New("a member needs its name; an empty name would address the container itself")
		case member.Kind == "prefix" && !strings.HasSuffix(member.Name, "/"):
			return fmt.Errorf("%q is a folder row whose name does not end in a slash", member.Name)
		case member.Kind != "object" && member.Kind != "prefix":
			return fmt.Errorf("%q is a %s row; only objects and folders are deleted here", member.Name, member.Kind)
		case seen[member.Name]:
			return fmt.Errorf("%q is selected twice; a member is deleted once", member.Name)
		}
		seen[member.Name] = true
		if member.Kind == "prefix" {
			folders[member.Name] = true
		}
	}
	// A member inside a selected folder would be deleted twice, once as
	// itself and once with the folder.
	for _, member := range req.Members {
		for i := 0; i < len(member.Name)-1; i++ {
			if folder := member.Name[:i+1]; member.Name[i] == '/' && folders[folder] {
				return fmt.Errorf("%q is inside the selected folder %q, which is deleted with everything in it; select one or the other", member.Name, folder)
			}
		}
	}
	return nil
}

// SelectedFolderPreviewDTO keeps a failed count beside the folder it could
// not read, without hiding the answers from the other selected folders.
type SelectedFolderPreviewDTO struct {
	Prefix  string            `json:"prefix"`
	Preview *DeletePreviewDTO `json:"preview"`
	Error   string            `json:"error"`
}

// PreviewSelectedDelete counts selected folders with at most eight readers
// and shares one archive observation. Object-only selections do not read an
// archive scan, matching the dialog's conservative segment default.
func (s *DeleteService) PreviewSelectedDelete(ctx context.Context, req SelectedDeleteRequestDTO) ([]SelectedFolderPreviewDTO, error) {
	if err := s.validateSelected(ctx, req); err != nil {
		return nil, err
	}
	entry, _, _, err := s.connections.boundSnapshot(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	previews := make([]SelectedFolderPreviewDTO, 0)
	for _, member := range req.Members {
		if member.Kind == "prefix" {
			previews = append(previews, SelectedFolderPreviewDTO{Prefix: member.Name})
		}
	}
	work := make(chan int)
	var readers sync.WaitGroup
	for range min(8, len(previews)) {
		readers.Go(func() {
			for i := range work {
				if ctx.Err() != nil {
					continue
				}
				preview, err := countDeleteScope(ctx, entry.session, req.Container, previews[i].Prefix)
				previews[i].Preview = preview
				if err != nil {
					previews[i].Error = err.Error()
				}
			}
		})
	}
dispatch:
	for i := range previews {
		select {
		case work <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(work)
	readers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A failed count has no preview. Do not scan when none could be read,
	// and do not let one failed folder clear the dialog's unknown default.
	var owners *swiftcontrol.ArchiveOwners
	for i := range previews {
		if preview := previews[i].Preview; preview != nil {
			if owners == nil {
				read := entry.session.ArchiveOwners(ctx, req.Container, archiveScanBound)
				owners = &read
			}
			setDeleteArchive(preview, *owners)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return previews, nil
}

// StartSelectedDelete deletes the selected objects, and everything under each
// selected folder, as one job whose items are each deleted by one DELETE of
// the name (ADR 0093 decisions 2 and 8, ADR 0122 decision 6). The request is
// refused before any job row when the scope is empty or has moved, when it
// names no member, a row that is neither an object nor a folder, an empty or
// repeated name, a member inside a selected folder, or more members than the
// selection ceiling, when the typed confirmation is not its words, or when it
// asks for segments in a container that keeps versions. The job lists the
// folders and admits the whole set before any request.
func (s *DeleteService) StartSelectedDelete(ctx context.Context, req SelectedDeleteRequestDTO) (string, error) {
	if err := refuseIfDeferred("DeleteService.StartSelectedDelete"); err != nil {
		return "", err
	}
	if err := s.validateSelected(ctx, req); err != nil {
		return "", err
	}
	if want := selectedConfirmation(req); req.Confirm != want {
		return "", fmt.Errorf("confirmation %q does not match %q; type it exactly", req.Confirm, want)
	}
	entry, bookmarkID, _, err := s.connections.boundSnapshot(ctx, req.Scope)
	if err != nil {
		return "", err
	}
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, bookmarkID)
	if err != nil {
		return "", err
	}
	owner.hold(releasePins)
	if req.Segments {
		if err := refuseSegmentsWhereVersionsAreKept(ctx, entry.session, req.Container); err != nil {
			return "", err
		}
	}
	// The selection may be rows a running upload or copy is writing, which
	// the working list shows like any others, and a selected folder is
	// claimed as a prefix deletion claims its prefix (ADR 0105 decision 3).
	account := swiftcontrol.AccountIdentity(entry.session.StorageURL())
	for _, member := range req.Members {
		if member.Kind == "prefix" {
			if err := owner.claim(sweepClaim(account, req.Container, member.Name)); err != nil {
				return "", err
			}
			continue
		}
		if err := s.runner.refuseClaimedName(account, req.Container, member.Name); err != nil {
			return "", err
		}
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type: jobs.TypeDeleteSelected, ItemRecordsVersion: 1,
		Source: req.Container, Destination: "(deleted)", SourceBookmarkID: bookmarkID, Engine: "native",
		Settings: fmt.Sprintf(`{"segments":%t}`, req.Segments),
	})
	if err != nil {
		return "", err
	}
	s.runner.register(job.ID, owner.cancel)
	s.runner.record(ctx, bookmarkID, "delete.selected.start", req.Container,
		fmt.Sprintf("job %s · %d members", job.ID, len(req.Members)), audit.ResultApplied)
	owner.launch(job.ID, bookmarkID, func(runCtx context.Context) {
		s.runSelectedDelete(runCtx, job.ID, req, bookmarkID, entry.session, owner)
	}, func(background context.Context) {
		// Cancelled before it ran: nothing was listed, admitted or issued.
		s.finishSelected(background, job.ID, req, bookmarkID, jobs.StateCancelled, "", 0)
	})
	return job.ID, nil
}

// runSelectedDelete is the job goroutine: the selected folders listed, the
// whole set admitted once, each item deleted by the folder deletion's own
// machinery, and the terminal state from the items' answers (ADR 0093
// decisions 4 and 8). A lost record leaves the job open with its status
// unavailable.
func (s *DeleteService) runSelectedDelete(ctx context.Context, jobID string, req SelectedDeleteRequestDTO, bookmarkID string, session *swiftcontrol.Session, owner *jobOwner) {
	background := context.WithoutCancel(ctx)
	if err := s.jobs.Transition(background, jobID, jobs.StatePlanning); err != nil {
		applog.Errorf("job %s: %v", jobID, err)
	}
	rows, folders, err := s.selectionRows(ctx, session, req)
	if err != nil {
		if ctx.Err() != nil {
			s.finishSelected(background, jobID, req, bookmarkID, jobs.StateCancelled, "", 0)
			return
		}
		s.finishSelected(background, jobID, req, bookmarkID, jobs.StateFailed, "nothing was deleted: "+err.Error(), 0)
		return
	}
	if err := s.jobs.Transition(background, jobID, jobs.StateRunning); err != nil {
		applog.Errorf("job %s: %v", jobID, err)
	}
	sweep := &prefixSweep{s: s, jobID: jobID, bookmarkID: bookmarkID, container: req.Container, session: session,
		segments: req.Segments, owner: owner, selection: true, folders: folders}
	sweep.runRows(ctx, rows)
	lost := sweep.lostReason()
	if lost == "" {
		lost = s.storeStoppedBeside()
	}
	if lost != "" {
		s.leaveSelectedOpen(background, jobID, req, bookmarkID, lost)
		return
	}
	state, reason := s.sweepEnd(background, jobID, sweep)
	if left := sweep.segmentsLeft(); left != "" && (state == jobs.StateComplete || state == jobs.StatePartial) {
		state = jobs.StatePartial
		if reason == "" {
			reason = left
		} else {
			reason += "; " + left
		}
	}
	if state == jobs.StateComplete {
		if err := s.jobs.Transition(background, jobID, jobs.StateVerifying); err != nil {
			applog.Errorf("job %s: %v", jobID, err)
		}
	}
	s.finishSelected(background, jobID, req, bookmarkID, state, reason, sweep.counts().admitted)
}

// selectionRows is what the job deletes: each selected object as it was
// listed, and every object a listing of each selected folder shows now, the
// folder's own marker included (ADR 0093 decision 8). The ceiling counts the
// whole set, and a set beyond it is refused before any request, naming the
// folder where the count crossed it. folders are the selected folders.
func (s *DeleteService) selectionRows(ctx context.Context, session *swiftcontrol.Session, req SelectedDeleteRequestDTO) (rows []sweepMember, folders []string, err error) {
	ceiling := s.selectionCeiling(ctx)
	for _, member := range req.Members {
		if member.Kind == "object" {
			rows = append(rows, sweepMember{container: req.Container, name: member.Name, bytes: member.Bytes, etag: member.ETag, slo: member.SLOListed})
		}
	}
	for _, member := range req.Members {
		if member.Kind != "prefix" {
			continue
		}
		folders = append(folders, member.Name)
		for page, listErr := range session.ObjectPages(ctx, req.Container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: member.Name}) {
			if listErr != nil {
				return nil, nil, fmt.Errorf("the listing of the selected folder %q could not be read: %w", member.Name, listErr)
			}
			for _, entry := range page.Entries {
				if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
					rows = append(rows, sweepMember{container: req.Container, name: object.Name, bytes: object.Bytes, etag: object.ETag, slo: object.SLOListed})
				}
			}
			if len(rows) > ceiling {
				return nil, nil, fmt.Errorf("the selection holds more than the selection ceiling of %d objects once the selected folder %q is counted; narrow the selection", ceiling, member.Name)
			}
		}
	}
	return rows, folders, nil
}

// leaveSelectedOpen ends a selected-set deletion whose records can no longer
// be written. It is left open with its status unavailable, the ledger's own
// latch: a member in flight is unresolved because its DELETE may have been
// sent, no later member is touched, nothing is retried, and the reopen rule
// settles the records when the store is next available (ADR 0093 decision 4).
// A terminal snapshot written now could not say which members were never
// requested.
func (s *DeleteService) leaveSelectedOpen(ctx context.Context, jobID string, req SelectedDeleteRequestDTO, bookmarkID, reason string) {
	s.runner.release(jobID)
	s.runner.record(ctx, bookmarkID, "delete.selected.unavailable", req.Container, "job "+jobID+" · "+reason, audit.ResultUnverified)
	applog.Warnf("job %s: %s", jobID, reason)
}

// storeStoppedBeside is why a deletion job's records cannot be written when
// it was another job's lost record that stopped the store: the runner then cancels
// this job (ADR 0105 decision 9), and the store refuses every deletion record
// from then on. A deletion job that ends that way is left open with its
// status unavailable, as one whose own record was lost is, so that the
// restart settles its members; a terminal row written now could not say which
// of them were never requested. It is "" while the store records.
func (s *DeleteService) storeStoppedBeside() string {
	if s.jobs.Available() == nil {
		return ""
	}
	return "the job store stopped recording while this job ran, so its remaining members could not be recorded"
}

func (s *DeleteService) finishSelected(ctx context.Context, jobID string, req SelectedDeleteRequestDTO, bookmarkID string, state jobs.State, message string, admitted int64) {
	s.runner.release(jobID)
	err := s.jobs.FinishFromItems(ctx, jobID, jobs.Completion{
		State: state, Counters: jobs.Counters{ObjectsPlanned: admitted}, Error: message,
		Audit: audit.Event{BookmarkID: bookmarkID, Operation: "delete.selected." + string(state), Resource: req.Container,
			Summary: fmt.Sprintf("job %s · %d selected · %d objects", jobID, len(req.Members), admitted), Result: terminalAuditResult(state)},
	}, s.runner.audit)
	if err != nil {
		applog.Errorf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}

// memberOutcome puts the store's answer to one member's DELETE in the
// ledger's words (ADR 0122 decision 6): accepted, or already gone, is
// deleted; no answer or a server error is unverified, because nothing says
// what it did; any other answer is a failure with the store's status, and
// deleted nothing.
func memberOutcome(result *swiftcontrol.ObjectDeleteResult, err error) jobs.ItemObservation {
	var se *swiftcontrol.SwiftError
	switch {
	case err == nil:
		return jobs.ItemObservation{Result: jobs.ItemDeleted, Effect: jobs.EffectReportedApplied}
	case errors.Is(err, swiftcontrol.ErrUnverifiedDelete):
		return jobs.ItemObservation{Result: jobs.ItemUnverified, Effect: jobs.EffectUnresolved,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeUnverified, HTTPStatus: statusOf(result)}}
	case errors.As(err, &se):
		return jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: itemCodeOf(se.Kind), HTTPStatus: se.HTTPStatus}}
	}
	return jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
		Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeUnknown}}
}

// statusOf is the store's status for a DELETE that was answered, 0 otherwise.
func statusOf(result *swiftcontrol.ObjectDeleteResult) int {
	if result == nil {
		return 0
	}
	return result.Status
}

func itemCodeOf(kind swiftcontrol.ErrorKind) jobs.ItemCode {
	switch kind {
	case swiftcontrol.KindUnauthorized:
		return jobs.CodeAuthentication
	case swiftcontrol.KindForbidden:
		return jobs.CodeForbidden
	case swiftcontrol.KindNotFound:
		return jobs.CodeMissing
	case swiftcontrol.KindConflict:
		return jobs.CodeConflict
	case swiftcontrol.KindTimeout:
		return jobs.CodeTimeout
	case swiftcontrol.KindNetwork:
		return jobs.CodeIO
	default:
		return jobs.CodeUnknown
	}
}
