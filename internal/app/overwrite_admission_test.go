package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
	// dir holds the selected files, so a case can take one away.
	dir string
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
	f := &overwriteFixture{h: h, bookmark: bookmark, token: picked.Token, dir: dir}
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

// A cancel that lands in the fixed-point read is the job stopping, not a
// finding about the container: the hook records nothing, where a container
// that could not be read is recorded as the conflict it is.
func TestVersioningReadCancelledRecordsNothing(t *testing.T) {
	f := newOverwriteFixture(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	refusal, err := f.h.transfer.versioningUnchanged(cancelled, t.Context(), f.bookmark, "replaceable",
		swiftcontrol.ContainerVersioning{}, "object.upload.replace", "replaceable/live/taken")
	if err != nil || !strings.Contains(refusal, "cancelled") {
		t.Fatalf("hook = %q, %v; want the cancellation and no record failure", refusal, err)
	}
	events, err := f.h.auditSvc.audit.List(t.Context(), audit.Query{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Result == audit.ResultConflict {
			t.Errorf("a cancelled read left a conflict in the trail: %+v", event)
		}
	}
}

// ADR 0081 decision 3: a question that cannot name the account it is about
// is not answerable, because a label may belong to either of two accounts.
// The rule is checked where it lives, because the wiring that reaches the
// confirmation has already resolved a session by then and cannot produce
// the case: an unanswerable question must still be refused rather than
// asked, and this is what keeps that true if the ordering ever changes.
func TestOverwriteQuestionRefusesWhatItCannotName(t *testing.T) {
	const (
		label      = "primary"
		account    = "AUTH_test"
		container  = "replaceable"
		versioning = "none observed"
	)
	names := []string{"live/taken"}
	for _, tc := range []struct {
		name                                  string
		label, account, container, versioning string
		names                                 []string
		refusal                               string
	}{
		{name: "no account", label: label, container: container, versioning: versioning, names: names,
			refusal: "account could not be identified"},
		{name: "no container", label: label, account: account, versioning: versioning, names: names,
			refusal: "container could not be identified"},
		{name: "no versioning", label: label, account: account, container: container, names: names,
			refusal: "versioning could not be stated"},
		{name: "nothing live", label: label, account: account, container: container, versioning: versioning,
			refusal: "nothing live to ask about"},
		{name: "no label", account: account, container: container, versioning: versioning, names: names},
		{name: "everything named", label: label, account: account, container: container,
			versioning: versioning, names: names},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const segments = "what happens to a replaced large object's old segments"
			question, err := overwriteQuestion(tc.label, tc.account, tc.container, "live/", tc.versioning, tc.names, segments)
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("the question was asked anyway: %+v %v", question, err)
				}
				if !reflect.DeepEqual(question, OverwriteConfirmation{}) {
					t.Errorf("a refused question carries a statement: %+v", question)
				}
				return
			}
			if err != nil {
				t.Fatalf("a complete question was refused: %v", err)
			}
			// A missing label is not a refusal: the account is what has to be
			// named, and a bookmark with no display name is ordinary.
			if question.Account != tc.account || question.Bookmark != tc.label ||
				question.Container != tc.container || question.Prefix != "live/" ||
				question.Versioning != tc.versioning || len(question.Names) != 1 {
				t.Fatalf("the question does not state the plan it authorises: %+v", question)
			}
			if question.Fragments != segments {
				t.Errorf("the question lost the sentence about segments: %q", question.Fragments)
			}
		})
	}
}
