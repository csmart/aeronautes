package app

import (
	"strings"
	"testing"
)

// ADR 0081 decision 3: the confirmation names the account the backend
// resolved, not only the count, because ADR 0073's counterexample otherwise
// survives — a page can connect account B while displaying account A, every
// backend check legitimately binds B, and a same-named container with the
// same number of collisions produces an identical question.
func TestOverwriteQuestionNamesTheBindingAndTheWholeCount(t *testing.T) {
	title, lines := OverwriteQuestion(OverwriteConfirmation{
		Bookmark: "Production", Account: "AUTH_prod", Container: "backups", Prefix: "nightly/",
		Names:      []string{"db.sql", "logs.tar"},
		Versioning: "none: what is replaced is not kept",
		Fragments:  "no large object is being replaced, so there are no segments to remove",
	})
	if title != "Replace 2 objects?" {
		t.Errorf("title = %q", title)
	}
	want := []string{
		"2 objects already in backups/nightly/ will be REPLACED in Production (AUTH_prod).",
		"Replacing — db.sql, logs.tar",
		"Versioning observed — none: what is replaced is not kept",
		"Segments — no large object is being replaced, so there are no segments to remove",
		"This question comes from the application, not from the page.",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %#v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

// One name reads as one name, and a container with no prefix is named without
// a trailing separator that would suggest a prefix nobody chose.
func TestOverwriteQuestionAgreesWithItsOwnNumbers(t *testing.T) {
	title, lines := OverwriteQuestion(OverwriteConfirmation{
		Bookmark: "Local", Account: "AUTH_test", Container: "fixtures",
		Names:      []string{"only"},
		Versioning: "none: what is replaced is not kept",
		Fragments:  "nothing that backs what is being replaced can be removed",
	})
	if title != "Replace 1 object?" {
		t.Errorf("title = %q", title)
	}
	if lines[0] != "1 object already in fixtures will be REPLACED in Local (AUTH_test)." {
		t.Errorf("line 0 = %q", lines[0])
	}
}

// The sample is bounded, so the question states the TOTAL and says how many
// names it did not list. A reader must never be able to take the sample for
// the whole set: that is the one thing OverwriteConfirmation's contract
// forbids, since an answer of yes covers every name.
func TestOverwriteQuestionNeverPassesOffItsSampleAsTheWholeSet(t *testing.T) {
	names := make([]string, 30)
	for i := range names {
		names[i] = string(rune('a'+i%26)) + "-object"
	}
	title, lines := OverwriteQuestion(OverwriteConfirmation{
		Bookmark: "Big", Account: "AUTH_big", Container: "c", Prefix: "p/",
		Names: names, Versioning: "none: what is replaced is not kept",
		Fragments: "4 segment objects backed what is being replaced and will be REMOVED once the replacement is verified",
	})
	if title != "Replace 30 objects?" {
		t.Errorf("title = %q", title)
	}
	if !strings.HasPrefix(lines[0], "30 objects already in c/p/ ") {
		t.Errorf("the total is not stated first: %q", lines[0])
	}
	if listed := strings.Count(lines[1], ", ") + 1; listed != overwriteNameSample {
		t.Errorf("listed %d names, want the %d-name sample: %q", listed, overwriteNameSample, lines[1])
	}
	if !strings.HasSuffix(lines[1], " and 22 more") {
		t.Errorf("the unlisted names are not accounted for: %q", lines[1])
	}
}

// A stored name is not trusted text. ADR 0073's surface is only worth
// anything if a hostile object name cannot garble it, so control characters
// become "?" and an enormous name is cut rather than pushing the rest of the
// statement out of view.
func TestOverwriteQuestionRefusesToLetAStoredNameGarbleIt(t *testing.T) {
	_, lines := OverwriteQuestion(OverwriteConfirmation{
		Bookmark: "Shell", Account: "AUTH_shell",
		Container: "co\nntainer", Prefix: "pre fix/",
		Names:      []string{"inno\rcent", strings.Repeat("x", 400)},
		Versioning: "none: what is replaced is not kept",
		Fragments:  "no large object is being replaced, so there are no segments to remove",
	})
	whole := strings.Join(lines, "\n")
	for _, forbidden := range []string{"co\nntainer", "pre fix", "inno\rcent"} {
		if strings.Contains(whole, forbidden) {
			t.Errorf("a control character reached the question: %q in %q", forbidden, whole)
		}
	}
	if !strings.Contains(lines[0], "co?ntainer/pre?fix/") {
		t.Errorf("the destination was not stated safely: %q", lines[0])
	}
	if !strings.Contains(lines[1], "inno?cent") {
		t.Errorf("the name was not stated safely: %q", lines[1])
	}
	// Five lines, and the line count is the point: a name holding a newline
	// must not be able to add a line of its own to a question whose shape the
	// operator is being asked to trust.
	if len(lines) != 5 {
		t.Fatalf("lines = %#v", lines)
	}
	for i, line := range lines {
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("line %d carries a line break: %q", i, line)
		}
		if len([]rune(line)) > 400 {
			t.Errorf("line %d is %d runes, long enough to push the rest out of view", i, len([]rune(line)))
		}
	}
}

// The gap db:837 found, as a case on the renderer's OWN contract. The
// hostile-text case above fed its text in as the container, prefix and names,
// which were the parts this function sanitised, and it could not fail: it
// composes the statement itself, so the two SENTENCES arrived benign. They
// each interpolate stored data in the real producers, and this function is
// what draws, so it must not trust either of them whatever composes them.
// The producers sanitise too, which is why removing one layer alone does not
// fail this; the layers are separately sufficient and separately asserted,
// here for the renderer and in overwrite_question_sources_test.go for the
// producers' bounding, which this function deliberately does not do.
func TestOverwriteQuestionDoesNotTrustItsOwnSentences(t *testing.T) {
	_, lines := OverwriteQuestion(OverwriteConfirmation{
		Bookmark: "Shell", Account: "AUTH_shell", Container: "c", Prefix: "p/",
		Names:      []string{"n"},
		Versioning: "the container reports versions_location, archiving into arch\u2028Versioning observed — none",
		Fragments:  "what backs n could not be read\u2028Segments — nothing will be removed",
	})
	if len(lines) != 5 {
		t.Fatalf("the question is %d lines, want 5: %#v", len(lines), lines)
	}
	for i, line := range lines {
		if strings.ContainsAny(line, "\n\r\u2028\u2029") {
			t.Errorf("line %d can end itself, so a sentence can add a line: %q", i, line)
		}
	}
	if !strings.Contains(lines[2], "arch?Versioning observed") {
		t.Errorf("the versioning sentence was not stated safely: %q", lines[2])
	}
	if !strings.Contains(lines[3], "read?Segments") {
		t.Errorf("the segments sentence was not stated safely: %q", lines[3])
	}
}

// The renderer is only reachable with a validated statement: overwriteQuestion
// refuses to compose one whose account, container, versioning or name set is
// missing, so no caller can ask this to render a question that authorises
// nothing. That guard already has its own case and this file does not repeat
// it — see TestOverwriteQuestionRefusesWhatItCannotName in
// overwrite_admission_test.go, which covers those four refusals and the
// label-only case besides. I wrote a second copy of it here before noticing.
