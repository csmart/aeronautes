package app

import (
	"fmt"
	"strings"
)

// overwriteNameSample bounds how many replaced names the question lists.
// The COUNT it states is always the whole count: OverwriteConfirmation's own
// contract forbids asking about fewer names than the job will replace, so the
// sample never stands in for the total and the line that carries the total
// comes first.
const overwriteNameSample = 8

// OverwriteQuestion renders one OverwriteConfirmation for the native surface
// that asks it: a title, and the message's lines in reading order.
//
// The wording lives here rather than in main.go so that what the operator is
// told can be ASSERTED rather than described. The precedent is
// StartupCapabilityLines, where ADR 0082's proof list makes the same point: a
// promise that the operator will be told is worth no more than the wording
// nobody checked. main.go renders these lines and holds no wording of its own.
//
// Everything that comes from stored data — the container, the prefix and the
// object names — goes through titleSafe here, because a stored name holding
// control characters would otherwise garble the one surface this feature
// claims is trustworthy (ADR 0073). The bookmark and the account arrive
// already shortened by overwriteQuestion and are stated in their own right,
// because the account is what tells two bookmarks that read alike apart.
//
// It is only reachable with a validated statement: overwriteQuestion refuses
// to compose one whose account, container, versioning or name set is missing,
// so no caller can ask this to render a question that authorises nothing.
func OverwriteQuestion(about OverwriteConfirmation) (string, []string) {
	total := len(about.Names)
	target := titleSafe(about.Container)
	if about.Prefix != "" {
		target += "/" + titleSafe(about.Prefix)
	}
	return fmt.Sprintf("Replace %d object%s?", total, plural(total, "", "s")),
		[]string{
			fmt.Sprintf("%d object%s already in %s will be REPLACED in %s.",
				total, plural(total, "", "s"), shortenText(target, 120),
				bindingText(about.Bookmark, about.Account, 80)),
			"Replacing — " + overwriteNames(about.Names),
			// Composed in this package, and still not trusted here: both
			// sentences INTERPOLATE stored data — the archive name the
			// container reported, and the object name in a reason that says
			// what could not be read. Each of those is bounded where it is
			// interpolated so these sentences keep their fixed words, and
			// each is sanitised again here because this is the function that
			// draws, and a surface whose whole value is that the page cannot
			// draw it must not be assembled from text it took on trust.
			// The bound is a stop against the pathological rather than a
			// presentation choice: it is far above any composition these
			// sentences can legitimately produce, and a cut is visible.
			"Versioning observed — " + titleText(about.Versioning, 800),
			"Segments — " + titleText(about.Fragments, 800),
			"This question comes from the application, not from the page.",
		}
}

// overwriteNames lists the sample and says how many it did not list, so a
// reader is never left believing the sample is the whole set.
func overwriteNames(names []string) string {
	shown := names
	if len(shown) > overwriteNameSample {
		shown = shown[:overwriteNameSample]
	}
	safe := make([]string, 0, len(shown))
	for _, name := range shown {
		safe = append(safe, titleText(name, 120))
	}
	listed := strings.Join(safe, ", ")
	if rest := len(names) - len(shown); rest > 0 {
		return fmt.Sprintf("%s and %d more", listed, rest)
	}
	return listed
}
