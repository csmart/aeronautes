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
	if about.Folder != "" {
		// A download replaces files on the operator's own disk, and like the
		// engine used alone it keeps nothing it replaces (ADR 0122 decision 12).
		return fmt.Sprintf("Replace up to %d local file%s?", total, plural(total, "", "s")),
			[]string{
				fmt.Sprintf("%d file%s already in the folder %s may be REPLACED by the objects of %s in %s.",
					total, plural(total, "", "s"), shortenText(titleSafe(about.Folder), 160), shortenText(target, 120),
					bindingText(about.SourceBookmark, about.SourceAccount, 80)),
				"Replacing — " + overwriteNames(about.Names),
				"Compared — " + compareRule,
				"A replaced file is gone: it is not kept or moved to the trash.",
				"This question comes from the application, not from the page.",
			}
	}
	replaceLine := fmt.Sprintf("%d object%s already in %s may be REPLACED in %s.",
		total, plural(total, "", "s"), shortenText(target, 120),
		bindingText(about.Bookmark, about.Account, 80))
	title := fmt.Sprintf("Replace up to %d object%s?", total, plural(total, "", "s"))
	if about.SourceAccount != "" {
		// A copy reads one account and writes another, so the sentence names
		// both: the destination it replaces in, and the source the bytes come
		// from. The account is what tells two bindings that read alike apart,
		// which is why it is kept in its own field and stated for each side.
		replaceLine = fmt.Sprintf("%d object%s already in %s in %s will be REPLACED by a copy from %s.",
			total, plural(total, "", "s"), shortenText(target, 120),
			bindingText(about.Bookmark, about.Account, 80),
			bindingText(about.SourceBookmark, about.SourceAccount, 80))
		title = fmt.Sprintf("Replace %d object%s?", total, plural(total, "", "s"))
	}
	lines := []string{replaceLine, "Replacing — " + overwriteNames(about.Names)}
	if about.SourceAccount == "" {
		// A copy has already found its names different from the listings
		// (ADR 0122 decision 4) and replaces each one; an upload's engine
		// compares (decision 12).
		lines = append(lines, "Compared — "+compareRule)
	}
	return title,
		append(lines,
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
			"Versioning observed — "+titleText(about.Versioning, 800),
			"Segments — "+titleText(about.Fragments, 800),
			"This question comes from the application, not from the page.",
		)
}

// compareRule is how a confirmed upload or download decides what it
// replaces: the engine's own comparison, as measured on the pinned engine
// (ADR 0122 decision 12).
const compareRule = "each one is replaced only when it differs, as rclone copy decides: one of another size is replaced; " +
	"one of the same size and time is left as it is; one of the same size with only another time keeps its bytes and " +
	"takes the source's time when its MD5 matches, and is replaced when it does not or when there is no MD5 to compare, " +
	"as for a large object."

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
