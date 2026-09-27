package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
)

// sentTagForFixture is the entity tag of the bytes the fixture's files
// hold, so a record can be shown naming what was replaced rather than what
// replaced it.
const sentTagForFixture = "97cbc70467cd15e9c5a28636d2dc962d"

// recordingOverwriteConfirmer answers, and keeps what it was asked.
type recordingOverwriteConfirmer struct {
	mu    sync.Mutex
	asked []OverwriteConfirmation
	yes   bool
	err   error
	// before runs inside the dialog, which is where a connection can move
	// under an answer.
	before func()
}

func (c *recordingOverwriteConfirmer) ConfirmOverwrite(_ context.Context, about OverwriteConfirmation) (bool, error) {
	c.mu.Lock()
	c.asked = append(c.asked, about)
	c.mu.Unlock()
	if c.before != nil {
		c.before()
	}
	return c.yes, c.err
}

func (c *recordingOverwriteConfirmer) questions() []OverwriteConfirmation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]OverwriteConfirmation(nil), c.asked...)
}

// overwriteFixture is one connected harness with a live object at
// live/taken and a selection holding two files, one of which collides.
type overwriteFixture struct {
	h        *harness
	bookmark string
	token    string
	// tag is the live object's entity tag before anything ran.
	tag string
}

func newOverwriteFixture(t *testing.T) *overwriteFixture {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.installEngine(nil)
	h.mock.AddContainer("replaceable", "standard")
	seedObject(h.mock, "replaceable", "live/taken", "the bytes already there", nil)
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"taken", "free"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("the bytes this job would send"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	h.transfer.setFilePicker(fakePicker{files: paths})
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{
		BookmarkID: bookmark, Container: "replaceable", Prefix: "live/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	f := &overwriteFixture{h: h, bookmark: bookmark, token: picked.Token}
	f.tag = f.liveTag(t)
	return f
}

func (f *overwriteFixture) request(acknowledge bool) UploadRequestDTO {
	return UploadRequestDTO{BookmarkID: f.bookmark, Container: "replaceable", Prefix: "live/",
		SelectionToken: f.token, AcknowledgeOverwrite: acknowledge}
}

// liveTag identifies what is actually at the name now, read back through
// the inspector rather than through anything this feature says about
// itself. An entity tag is enough: if it is unchanged, the bytes are.
func (f *overwriteFixture) liveTag(t *testing.T) string {
	t.Helper()
	detail, err := f.h.inspector.Object(context.Background(), f.bookmark, "replaceable", "live/taken")
	if err != nil {
		t.Fatalf("the live object could not be read back: %v", err)
	}
	return strings.Trim(detail.ETag, `"`)
}

// ADR 0081 decision 1: without the acknowledgement nothing changes. The
// preflight refuses the collision and the start refuses with it.
func TestUploadWithoutAcknowledgementStillRefusesALiveName(t *testing.T) {
	f := newOverwriteFixture(t)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})
	report, err := f.h.transfer.PreflightUpload(t.Context(), f.request(false))
	if err != nil || report == nil || !report.Rejected() {
		t.Fatalf("an unacknowledged preflight admitted a live name: %+v %v", report, err)
	}
	if _, err := f.h.transfer.StartUpload(t.Context(), f.request(false)); err == nil {
		t.Fatal("an unacknowledged start was admitted")
	}
	if got := f.liveTag(t); got != f.tag {
		t.Fatalf("the live object changed: %q, was %q", got, f.tag)
	}
}

// ADR 0081 decision 2: no confirmation, no replacement. A build with no
// confirmer wired refuses rather than assuming an answer.
func TestAcknowledgedUploadWithNoConfirmerRefuses(t *testing.T) {
	f := newOverwriteFixture(t)
	_, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err == nil || !strings.Contains(err.Error(), "shell's confirmation") {
		t.Fatalf("an acknowledged start without a confirmer was admitted: %v", err)
	}
	if got := f.liveTag(t); got != f.tag {
		t.Fatalf("the live object changed: %q, was %q", got, f.tag)
	}
}

// The page's own acknowledgement is not the authority: an answer of no,
// and a confirmation that fails to be raised, each refuse.
func TestAcknowledgedUploadNeedsAYes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		confirmer *recordingOverwriteConfirmer
		wants     string
	}{
		{"answered no", &recordingOverwriteConfirmer{yes: false}, "not confirmed"},
		{"could not be raised", &recordingOverwriteConfirmer{err: errors.New("no display")}, "no display"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOverwriteFixture(t)
			f.h.transfer.setOverwriteConfirmer(tc.confirmer)
			_, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
			if err == nil || !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the start was admitted: %v", err)
			}
			if len(tc.confirmer.questions()) != 1 {
				t.Fatalf("the confirmation was asked %d times", len(tc.confirmer.questions()))
			}
			if got := f.liveTag(t); got != f.tag {
				t.Fatalf("the live object changed: %q, was %q", got, f.tag)
			}
		})
	}
}

// ADR 0081 decision 3: the question names the account the backend
// resolved, the container and prefix it resolved, and every name it would
// replace. Decision 8: it states what the container said about keeping
// versions, as an observation.
func TestAcknowledgedUploadAsksAboutTheResolvedPlan(t *testing.T) {
	f := newOverwriteFixture(t)
	confirmer := &recordingOverwriteConfirmer{yes: true}
	f.h.transfer.setOverwriteConfirmer(confirmer)
	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatalf("a confirmed start was refused: %v", err)
	}
	// A confirmed replacement publishes: both names are copied and
	// verified, and the one that was live now holds the bytes this job
	// sent rather than the bytes it replaced.
	job := awaitTerminal(t, f.h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsVerified != 2 ||
		job.Counters.ObjectsFailed != 0 {
		t.Fatalf("a confirmed replacement did not publish: %s %+v", job.State, job.Counters)
	}
	// One of the two names was live, so exactly one was replaced. A job
	// that replaced objects and reported none of them would be a counter
	// that lies by omission.
	if job.Counters.Overwrites != 1 {
		t.Fatalf("the job reports %d replacements of 1: %+v", job.Counters.Overwrites, job.Counters)
	}
	if got := f.liveTag(t); got == f.tag {
		t.Fatalf("the live object was not replaced: still %q", got)
	} else if got != sentTagForFixture {
		t.Fatalf("the live object holds neither what it had nor what was sent: %q", got)
	}
	asked := confirmer.questions()
	if len(asked) != 1 {
		t.Fatalf("the confirmation was asked %d times", len(asked))
	}
	about := asked[0]
	account := f.h.browse.connections.AccountName()
	if account == "" || !strings.Contains(about.Account, account) {
		t.Errorf("the question does not name the resolved account %q: %+v", account, about)
	}
	if about.Container != "replaceable" || about.Prefix != "live/" {
		t.Errorf("the question does not name the resolved target: %+v", about)
	}
	// The whole set, so the question cannot be asked about fewer names
	// than the job would replace, and only the names that are live.
	if len(about.Names) != 1 || about.Names[0] != "live/taken" {
		t.Errorf("the question does not name exactly what is live: %+v", about.Names)
	}
	if !strings.Contains(about.Versioning, "none") {
		t.Errorf("the question does not state the observed versioning: %q", about.Versioning)
	}
}

// ADR 0081 decision 4: the answer authorises the plan it was asked about.
// A connection that moves while the operator is answering invalidates it.
func TestAcknowledgedUploadRefusesWhenTheAuthorityMovesUnderTheAnswer(t *testing.T) {
	f := newOverwriteFixture(t)
	confirmer := &recordingOverwriteConfirmer{yes: true}
	// Connecting to another account inside the dialog moves the endpoint
	// the answer was given under, which is the authority half of
	// decision 4. Re-saving the bookmark would move its generation, but
	// the job's pin already refuses that, so this is the half that is
	// actually reachable here.
	second, err := f.h.bookmarks.Save(context.Background(), BookmarkDTO{
		DisplayName: "elsewhere", AuthURL: f.h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	confirmer.before = func() {
		if _, err := f.h.browse.Connect(context.Background(), second.ID); err != nil {
			t.Errorf("connecting elsewhere: %v", err)
		}
	}
	f.h.transfer.setOverwriteConfirmer(confirmer)
	_, startErr := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if startErr == nil || !strings.Contains(startErr.Error(), "changed while you were answering") {
		t.Fatalf("an answer given under another authority was carried: %v", startErr)
	}
	// The read-back needs the connection the object belongs to, which the
	// fixture moved deliberately.
	if _, err := f.h.browse.Connect(context.Background(), f.bookmark); err != nil {
		t.Fatal(err)
	}
	if got := f.liveTag(t); got != f.tag {
		t.Fatalf("the live object changed: %q, was %q", got, f.tag)
	}
}

// Acknowledged with nothing live to replace: there is nothing to ask
// about, so nothing is asked and every name still publishes conditionally.
func TestAcknowledgedUploadAsksNothingWhenNothingIsLive(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.installEngine(nil)
	h.mock.AddContainer("empty-destination", "standard")
	path := filepath.Join(t.TempDir(), "fresh")
	if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(fakePicker{files: []string{path}})
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{
		BookmarkID: bookmark, Container: "empty-destination"})
	if err != nil {
		t.Fatal(err)
	}
	confirmer := &recordingOverwriteConfirmer{yes: true}
	h.transfer.setOverwriteConfirmer(confirmer)
	id, err := h.transfer.StartUpload(t.Context(), UploadRequestDTO{BookmarkID: bookmark,
		Container: "empty-destination", SelectionToken: picked.Token, AcknowledgeOverwrite: true})
	if err != nil {
		t.Fatalf("an acknowledged start with nothing live was refused: %v", err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" {
		t.Fatalf("an acknowledged upload of a free name did not publish: %s %+v", job.State, job.Counters)
	}
	// Acknowledged, but nothing was replaced, so nothing is counted as one.
	if job.Counters.Overwrites != 0 {
		t.Fatalf("an upload that replaced nothing reports %d replacements", job.Counters.Overwrites)
	}
	if asked := confirmer.questions(); len(asked) != 0 {
		t.Fatalf("a confirmation was raised with nothing to replace: %+v", asked)
	}
}

// ADR 0081 decision 8: a container whose versioning mode this build cannot
// reason about is refused for overwrite, as ADR 0054 refuses editing it,
// and the refusal comes before the operator is asked to authorise
// something the question could not describe.
func TestAcknowledgedUploadRefusesAVersioningModeItCannotDescribe(t *testing.T) {
	f := newOverwriteFixture(t)
	confirmer := &recordingOverwriteConfirmer{yes: true}
	f.h.transfer.setOverwriteConfirmer(confirmer)
	// Modern object versioning, which ADR 0054 refuses to edit.
	f.h.mock.EnableModernVersioning("replaceable")
	_, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err == nil || !strings.Contains(err.Error(), "overwrite is refused") {
		t.Fatalf("an overwrite onto a mode this build cannot describe was admitted: %v", err)
	}
	if asked := confirmer.questions(); len(asked) != 0 {
		t.Fatalf("the operator was asked to authorise a mode the question could not describe: %+v", asked)
	}
	if got := f.liveTag(t); got != f.tag {
		t.Fatalf("the live object changed: %q, was %q", got, f.tag)
	}
}

// ADR 0081 decision 7: the durable trail names what the commit saw at the
// name a moment before publishing over it, and names it as what was seen
// rather than as what was replaced, because this contract admits a racing
// writer.
func TestAcknowledgedUploadRecordsWhatItSawBeforeReplacing(t *testing.T) {
	f := newOverwriteFixture(t)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})
	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	events, err := f.h.auditSvc.audit.List(t.Context(), audit.Query{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var found *audit.Event
	for i, event := range events {
		if event.Operation == "object.upload.replace" {
			if found != nil {
				t.Fatalf("one replacement produced more than one record: %+v", events)
			}
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("a replacement left no record of what it replaced: %+v", events)
	}
	if found.Resource != "replaceable/live/taken" {
		t.Errorf("the record names the wrong resource: %q", found.Resource)
	}
	if found.Result != audit.ResultObserved {
		t.Errorf("the record claims a write outcome rather than an observation: %q", found.Result)
	}
	// The entity tag of what was there, not of what replaced it.
	for _, want := range []string{f.tag, "observed", "may not be that one"} {
		if !strings.Contains(found.Summary, want) {
			t.Errorf("the record does not say %q: %q", want, found.Summary)
		}
	}
	if strings.Contains(found.Summary, sentTagForFixture) {
		t.Errorf("the record names the bytes that replaced rather than the ones replaced: %q", found.Summary)
	}
}
