package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The regressions for a review's P1, taken through the REAL producers rather
// than through a hand-built statement.
//
// TestOverwriteQuestionRefusesToLetAStoredNameGarbleIt fed hostile text in as
// the container, prefix and object names and passed, because those were the
// parts the renderer sanitised. It could not fail: its fixture composed an
// OverwriteConfirmation directly, so the two sentences arrived benign. The
// sentences are the hole: the versioning sentence interpolates the archive
// name a container reported, and only a case that drives the producers can
// show it. So these go through public StartUpload, record what the service
// actually asked, and render that.
//
// U+2028 is the character that matters here: a line separator survives in a
// Go string, and on the native surface it ends a line. Stored data that can
// end a line can add a line of application-looking text to the one surface
// whose value is that the page cannot draw it.
const injected = " untrusted-extra-line"

// hostileFixture is newOverwriteFixture with a caller-chosen colliding name,
// so a case can put injected text where the producers will pick it up.
type hostileFixture struct {
	h        *harness
	bookmark string
	token    string
	// name is the first colliding name, which the single-name cases use.
	name  string
	names []string
}

func newHostileFixture(t *testing.T, collidingBases ...string) *hostileFixture {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.installEngine(nil)
	h.mock.AddContainer("replaceable", "standard")
	dir := t.TempDir()
	var paths, live []string
	for _, base := range collidingBases {
		path := filepath.Join(dir, base)
		if err := os.WriteFile(path, []byte("the bytes this job would send"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		live = append(live, "live/"+base)
	}
	h.transfer.setFilePicker(fakePicker{files: paths})
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{
		BookmarkID: bookmark, Container: "replaceable", Prefix: "live/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	return &hostileFixture{h: h, bookmark: bookmark, token: picked.Token, name: live[0], names: live}
}

func (f *hostileFixture) request() UploadRequestDTO {
	return UploadRequestDTO{BookmarkID: f.bookmark, Container: "replaceable", Prefix: "live/",
		SelectionToken: f.token, AcknowledgeOverwrite: true}
}

// askedQuestion drives the admission and returns the lines the shell would
// have been given. The confirmer refuses, so nothing is written: what is
// under test is the text the shell is handed, not an effect.
func (f *hostileFixture) askedQuestion(t *testing.T) []string {
	t.Helper()
	confirmer := &recordingOverwriteConfirmer{yes: false}
	f.h.transfer.setOverwriteConfirmer(confirmer)
	if _, err := f.h.transfer.StartUpload(t.Context(), f.request()); err == nil {
		t.Fatal("a declined replacement was admitted")
	}
	asked := confirmer.questions()
	if len(asked) != 1 {
		t.Fatalf("the confirmation was asked %d times, want once", len(asked))
	}
	_, lines := OverwriteQuestion(asked[0])
	return lines
}

// assertCannotAddALine is the property both cases share: whatever the store
// said, the question is the five lines this application composed.
func assertCannotAddALine(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) != 5 {
		t.Fatalf("the question is %d lines, want the 5 this application composes: %#v", len(lines), lines)
	}
	for i, line := range lines {
		if strings.ContainsAny(line, "\n\r  ") {
			t.Errorf("line %d can end itself, so stored data can add a line to the question: %q", i, line)
		}
	}
	if last := lines[4]; last != "This question comes from the application, not from the page." {
		t.Errorf("the question's own last word is not last: %q", last)
	}
}

// Path one: the archive name, which versioningStatement interpolates straight
// from the container's headers.
func TestTheArchiveNameCannotAddALineToTheQuestion(t *testing.T) {
	f := newHostileFixture(t, "taken")
	seedObject(f.h.mock, "replaceable", f.name, "the bytes already there", nil)
	f.h.mock.EnableVersionArchive("replaceable", "retained-versions"+injected, false)

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	versioning := lines[2]
	if !strings.Contains(versioning, "retained-versions?untrusted-extra-line") {
		t.Errorf("the archive name was not stated safely: %q", versioning)
	}
	// Bounded at the interpolation, so the sentence keeps the words after it.
	// A bound that cut the tail would take away the one part of this line that
	// says what the mode does NOT promise.
	if !strings.HasSuffix(versioning, "no archived version has been read back by anything here") {
		t.Errorf("the versioning sentence lost its own consequence: %q", versioning)
	}
}

// A long archive name cannot push the rest of the statement out of view
// either, which is the other half of "bounded": the mode and the consequence
// are what the operator needs and they come after it.
func TestALongArchiveNameCannotCrowdOutTheQuestion(t *testing.T) {
	f := newHostileFixture(t, "taken")
	seedObject(f.h.mock, "replaceable", f.name, "the bytes already there", nil)
	f.h.mock.EnableVersionArchive("replaceable", strings.Repeat("a", 4000), false)

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	versioning := lines[2]
	if len([]rune(versioning)) > 240 {
		t.Errorf("the versioning line is %d runes, long enough to crowd out the rest: %q",
			len([]rune(versioning)), versioning)
	}
	if !strings.HasSuffix(versioning, "no archived version has been read back by anything here") {
		t.Errorf("the versioning sentence lost its own consequence: %q", versioning)
	}
}
