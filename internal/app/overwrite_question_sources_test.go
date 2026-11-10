package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The regressions for db:837's P1, taken through the REAL producers rather
// than through a hand-built statement.
//
// TestOverwriteQuestionRefusesToLetAStoredNameGarbleIt fed hostile text in as
// the container, prefix and object names and passed, because those were the
// parts the renderer sanitised. It could not fail: its fixture composed an
// OverwriteConfirmation directly, so the two sentences arrived benign. The
// sentences are the hole — each interpolates stored data of its own, the
// archive name a container reported and the object name inside a reason that
// says what could not be read — and only a case that drives the producers can
// show it. So these two go through public StartUpload, record what the service
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

// Path two: the object name, which the fragment reading interpolates into the
// reason it could not say what backs a replacement.
func TestAnObjectNameCannotAddALineToTheQuestion(t *testing.T) {
	f := newHostileFixture(t, "taken"+injected)
	seedStaticLargeObject(t, f.h, "replaceable", f.name)
	// Armed before the start, so the reading the QUESTION rests on is the one
	// that cannot inventory this name.
	f.h.mock.SetManifestReadFault("replaceable", f.name, "partial")

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	if !strings.Contains(lines[1], "taken?untrusted-extra-line") {
		t.Errorf("the replaced name was not stated safely: %q", lines[1])
	}
	segments := lines[3]
	if !strings.Contains(segments, "taken?untrusted-extra-line") {
		t.Errorf("the reason does not name the object it is about, safely: %q", segments)
	}
	// The sentence still refuses in its own words: a question that cannot say
	// what a replacement would remove must say so and authorise nothing.
	if !strings.Contains(segments, "nothing will be removed") {
		t.Errorf("the segments sentence lost its refusal: %q", segments)
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

// The other half of the Unknown reason's bound, and the catching case for
// note's own contract: it keeps the FIRST reason and counts the rest. It
// appended every one until db:837, which is not what its comment said and
// not what a bounded sentence is — a selection where many names cannot be
// read would have carried one reason per name onto a native dialog.
func TestManyUnreadableNamesCannotGrowTheQuestion(t *testing.T) {
	bases := []string{"one", "two", "three", "four", "five"}
	f := newHostileFixture(t, bases...)
	for _, name := range f.names {
		seedStaticLargeObject(t, f.h, "replaceable", name)
		f.h.mock.SetManifestReadFault("replaceable", name, "partial")
	}

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	segments := lines[3]
	// One reason, then a count. Five reasons joined would be about five times
	// this, and a thousand names would be a thousand reasons.
	if !strings.Contains(segments, "and 4 more names could not be read") {
		t.Errorf("the reason does not count the names it left out: %q", segments)
	}
	for _, base := range bases[1:] {
		if strings.Contains(segments, "/"+base) {
			t.Errorf("the reason names %q as well as the first, so it grows with the selection: %q", base, segments)
		}
	}
	// The naming assertions above are the exact statement that it does not
	// grow. This is the presentation bound beside it: the sentence's own fixed
	// words are about 190 runes and the reason inside it is bounded at 240, so
	// anything past that is the reason growing.
	if len([]rune(segments)) > 440 {
		t.Errorf("the segments line is %d runes, past its fixed words plus the reason's bound: %q",
			len([]rune(segments)), segments)
	}
	// It still refuses in its own words: a question that cannot say what a
	// replacement would remove authorises nothing.
	if !strings.Contains(segments, "nothing will be removed") {
		t.Errorf("the segments sentence lost its refusal: %q", segments)
	}
}

// And one reason can be long on its own, because an object name can be: a
// filesystem allows 255 bytes in a basename, and the reason quotes the whole
// name plus what the store said. This is the catching case for the bound on
// the reason itself, as the many-name case above is for note's count.
func TestOneLongNameCannotGrowTheQuestionEither(t *testing.T) {
	f := newHostileFixture(t, strings.Repeat("n", 240))
	seedStaticLargeObject(t, f.h, "replaceable", f.name)
	f.h.mock.SetManifestReadFault("replaceable", f.name, "partial")

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	segments := lines[3]
	if len([]rune(segments)) > 440 {
		t.Errorf("the segments line is %d runes, so one long name grows it: %q",
			len([]rune(segments)), segments)
	}
	if !strings.Contains(segments, "nothing will be removed") {
		t.Errorf("the segments sentence lost its refusal: %q", segments)
	}
}

// db:838's advisory, as two cases. The reason kept and the count of the rest
// are now held apart, because the kept reason QUOTES A STORED NAME and the
// count was being found by splitting on its own suffix. A name is arbitrary
// text: it can contain that suffix, and it can be long enough to fill the
// budget the count needs.
func TestAStoredNameCannotShortenTheReasonItAppearsIn(t *testing.T) {
	// A name carrying the exact shape of the suffix note appends.
	decoy := "taken; and 9 more names could not be read-decoy"
	f := newHostileFixture(t, decoy, "second")
	for _, name := range f.names {
		seedStaticLargeObject(t, f.h, "replaceable", name)
		f.h.mock.SetManifestReadFault("replaceable", name, "partial")
	}

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	segments := lines[3]
	// The count is this helper's, about the names it left out, and says one.
	if !strings.Contains(segments, "and 1 more name could not be read") {
		t.Errorf("the count was lost or miscounted: %q", segments)
	}
	// And the kept reason was not cut at the suffix inside the name.
	if !strings.Contains(segments, "decoy") {
		t.Errorf("a stored name shortened the reason it appears in: %q", segments)
	}
}

func TestALongReasonCannotPushOutItsOwnCount(t *testing.T) {
	f := newHostileFixture(t, strings.Repeat("n", 240), "second")
	for _, name := range f.names {
		seedStaticLargeObject(t, f.h, "replaceable", name)
		f.h.mock.SetManifestReadFault("replaceable", name, "partial")
	}

	lines := f.askedQuestion(t)
	assertCannotAddALine(t, lines)
	segments := lines[3]
	if !strings.Contains(segments, "and 1 more name could not be read") {
		t.Errorf("a long first reason pushed its own count out of the sentence: %q", segments)
	}
	if !strings.Contains(segments, "…") {
		t.Errorf("the reason was cut without saying so: %q", segments)
	}
	if !strings.Contains(segments, "nothing will be removed") {
		t.Errorf("the segments sentence lost its refusal: %q", segments)
	}
}
