package jobs

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrItemRecordsNotCaptured = errors.New("item records were not captured for this job")
	ErrItemRecordsUnavailable = errors.New("a transfer item record could not be saved; changes may already have occurred. Job status is unavailable and new jobs are blocked for this session. Inspect retained data before retrying")
	ErrInvalidItemRecord      = errors.New("invalid transfer item record")
	ErrItemRecordConflict     = errors.New("transfer item record conflicts with an earlier fact")
	ErrItemJobNotFound        = errors.New("transfer item job not found")
	ErrItemNotFound           = errors.New("transfer item was not admitted")
	ErrItemJobTerminal        = errors.New("terminal transfer item records cannot change")
	ErrItemCompletionRequired = errors.New("recorded transfers require item-derived completion")
	ErrIncompleteItemRecords  = errors.New("transfer item records do not permit complete")
	ErrInvalidItemPage        = errors.New("invalid transfer item page or cursor")
	ErrItemStorage            = errors.New("transfer item storage failed")
)

type ItemLocationKind string

const (
	ItemSwift ItemLocationKind = "swift"
	ItemLocal ItemLocationKind = "local"
)

// ItemLocation captures a logical location, never remote-version or retry authority.
type ItemLocation struct {
	Kind       ItemLocationKind `json:"kind"`
	BookmarkID string           `json:"bookmarkId,omitempty"`
	Container  string           `json:"container,omitempty"`
	Object     string           `json:"object,omitempty"`
	LocalPath  string           `json:"localPath,omitempty"`
}

type ItemIdentity struct {
	Key         string
	Source      ItemLocation
	Destination ItemLocation
}

type ItemIntent struct {
	ItemIdentity
	Staging     *ItemLocation
	Planned     bool
	Admitted    bool
	ListedBytes int64
	// ListedETag and ListedSLO are the facts a deletion member was listed
	// with beside its bytes (ADR 0093 decision 2): the baseline rule reads
	// them and the record keeps what the operator saw. Empty for a transfer.
	ListedETag          string
	ListedSLO           bool
	PublicationRequired bool
}

type ItemResult string

const (
	ItemCopied       ItemResult = "copied"
	ItemFailed       ItemResult = "failed"
	ItemConflict     ItemResult = "conflict"
	ItemRefused      ItemResult = "refused"
	ItemSame         ItemResult = "same"
	ItemNotAttempted ItemResult = "not_attempted"
	ItemVerified     ItemResult = "verified"
	// ItemDeleted and ItemUnverified are a deletion member's outcomes (ADR 0093
	// decision 4): the DELETE was accepted and the name no longer held what
	// was shown, or the DELETE was sent and nothing proves what it did.
	ItemDeleted    ItemResult = "deleted"
	ItemUnverified ItemResult = "unverified"
)

type ItemEffect string

const (
	EffectNotAttempted    ItemEffect = "not_attempted"
	EffectReportedApplied ItemEffect = "reported_applied"
	EffectUnresolved      ItemEffect = "unresolved"
)

type ItemStage string

const (
	StagePlanning     ItemStage = "planning"
	StageSourceRead   ItemStage = "source_read"
	StageEngine       ItemStage = "engine"
	StageStaging      ItemStage = "staging"
	StageCommit       ItemStage = "commit"
	StageMetadata     ItemStage = "metadata"
	StageVerification ItemStage = "verification"
	StageLocalRead    ItemStage = "local_read"
	StagePlacement    ItemStage = "local_placement"
	StageCleanup      ItemStage = "cleanup"
	StageRecording    ItemStage = "recording"
	StageUnknown      ItemStage = "unknown"
)

type ItemCode string

const (
	CodeCancelled      ItemCode = "cancelled"
	CodeTimeout        ItemCode = "timeout"
	CodeAuthentication ItemCode = "authentication"
	CodeForbidden      ItemCode = "forbidden"
	CodeMissing        ItemCode = "missing"
	CodeConflict       ItemCode = "conflict"
	// CodeLink says the name held a symbolic link, in either representation,
	// where a deletion member's read that fixes its baseline looked (ADR 0093
	// decision 4): refused with its kind, because the identity listed is the
	// target's and a DELETE there would remove the link instead.
	CodeLink        ItemCode = "link"
	CodeUnsupported ItemCode = "unsupported"
	CodeIO          ItemCode = "io"
	CodeIncomplete  ItemCode = "incomplete"
	CodeUnverified  ItemCode = "unverified"
	CodeUnknown     ItemCode = "unknown"
)

// ItemDiagnostic cannot carry raw provider text, credentials, headers or URLs.
type ItemDiagnostic struct {
	Stage      ItemStage
	Code       ItemCode
	HTTPStatus int
}

type ItemObservation struct {
	Result      ItemResult
	Effect      ItemEffect
	Diagnostic  ItemDiagnostic
	CopiedBytes int64
}

type RecordedObservation struct {
	ItemObservation
	CreatedAt time.Time
}

type ItemRecord struct {
	ItemIntent
	ID           int64
	JobID        string
	CreatedAt    time.Time
	Execution    *RecordedObservation
	Verification *RecordedObservation
	Publication  *RecordedObservation
	// Predecessor is what was live at the destination immediately before
	// this item was published over it, read without its segment names
	// (ADR 0081 decision 7). Nil when nothing was recorded, which is every
	// item that did not replace anything.
	Predecessor *ItemPredecessor
	// IssuedAt is when a deletion member's issued mark was written, before its
	// final HEAD (ADR 0039, ADR 0093 decision 4); zero when it never was. A
	// member issued with no outcome may have been deleted.
	IssuedAt time.Time
	// Successor is what a deletion member's read found at the name: the
	// verifying read's after the DELETE, or the comparison HEAD's when the
	// member was found changed before any DELETE; nil when nothing was
	// recorded.
	Successor *ItemSuccessor
	// Baseline is the identity the member's comparison was made against
	// (ADR 0093 decision 3), written with its outcome; nil when the member
	// never reached a comparison.
	Baseline *MemberBaseline
}

type ItemPage struct {
	Items      []ItemRecord
	NextCursor int64
}

// Facets overlap: a failed item can also have unresolved effects or verification.
type ItemTotals struct {
	Items      int64
	Planned    int64
	Admitted   int64
	Failed     int64
	Conflicts  int64
	Refused    int64
	Unresolved int64
	Unverified int64
	Problems   int64
	// Issued and Deleted are a deletion job's: how many members were marked
	// issued, and how many were deleted (ADR 0093 decision 4). Unsettled is
	// how many were issued and have no outcome yet, each of which may have
	// been requested; it is a part of Unresolved that overlaps nothing else.
	Issued    int64
	Deleted   int64
	Unsettled int64
}

func (l ItemLocation) valid() bool {
	for _, value := range []string{l.BookmarkID, l.Container, l.Object, l.LocalPath} {
		if !utf8.ValidString(value) {
			return false
		}
	}
	switch l.Kind {
	case ItemSwift:
		return l.BookmarkID != "" && l.Container != "" && l.Object != "" && l.LocalPath == ""
	case ItemLocal:
		return filepath.IsAbs(l.LocalPath) && l.BookmarkID == "" && l.Container == "" && l.Object == ""
	default:
		return false
	}
}

func (i ItemIdentity) valid() bool {
	return utf8.ValidString(i.Key) && i.Source.valid() && (i.Destination.valid() || i.Destination == ItemLocation{})
}

// deletion says the item is a member of a deletion: its identity is its
// source alone, with no destination (ADR 0093 decision 4). The write that
// admits an item checks this against the job's type, because the identity
// cannot see it.
func (i ItemIdentity) deletion() bool {
	return i.Destination == ItemLocation{}
}

func (i ItemIntent) valid() bool {
	if !i.ItemIdentity.valid() || i.ListedBytes < 0 || !utf8.ValidString(i.ListedETag) || i.Staging != nil && !i.Staging.valid() {
		return false
	}
	return !i.PublicationRequired || i.Planned && i.Admitted && i.Source.Kind == ItemSwift &&
		!strings.HasSuffix(i.Source.Object, "/") &&
		i.Destination.Kind == ItemLocal && i.Staging != nil && i.Staging.Kind == ItemLocal &&
		i.Staging.LocalPath != i.Destination.LocalPath
}

func (o ItemObservation) validPublication() bool {
	if !o.Diagnostic.valid() || o.CopiedBytes != 0 {
		return false
	}
	return o.Result == ItemVerified && o.Effect == EffectNotAttempted && o.Diagnostic == (ItemDiagnostic{}) ||
		o.Result == ItemFailed && o.Effect == EffectUnresolved && o.Diagnostic.Stage == StageVerification && o.Diagnostic.Code != ""
}

// validDeletion is the one observation a deletion member records, when its
// verifying read settles (ADR 0093 decision 4). A deleted member had its
// effect reported; an unverified one was sent a DELETE that nothing proves;
// a refused, conflicting or failed member had nothing deleted and says why;
// a member not attempted was never requested.
func (o ItemObservation) validDeletion(phase string, admitted bool) bool {
	if phase != "execution" || !admitted || !o.Diagnostic.valid() || o.CopiedBytes != 0 {
		return false
	}
	switch o.Result {
	case ItemDeleted:
		return o.Effect == EffectReportedApplied && o.Diagnostic == (ItemDiagnostic{})
	case ItemUnverified:
		return o.Effect == EffectUnresolved && o.Diagnostic.Code != ""
	case ItemRefused, ItemConflict, ItemFailed:
		return o.Effect == EffectNotAttempted && o.Diagnostic != (ItemDiagnostic{})
	case ItemNotAttempted:
		return o.Effect == EffectNotAttempted && o.Diagnostic == (ItemDiagnostic{})
	default:
		return false
	}
}

// The read that observed a successor: the literal verifying read after the
// member's DELETE, or the comparison HEAD that found the member changed
// since it was listed before any DELETE was issued.
const (
	SuccessorAfter      = "after"
	SuccessorComparison = "comparison"
)

// ItemSuccessor is what a read observed at a deletion member's name (ADR
// 0093 decision 3): nothing, or the object there by class, length and entity
// tag, and which read saw it. It is an observation and never an
// identification of where that object came from.
type ItemSuccessor struct {
	Present   bool
	Class     string
	Length    int64
	ETag      string
	Role      string
	CreatedAt time.Time
}

func (s ItemSuccessor) valid() bool {
	if !utf8.ValidString(s.Class) || !utf8.ValidString(s.ETag) || s.Length < 0 {
		return false
	}
	switch s.role() {
	case SuccessorAfter:
	case SuccessorComparison:
		// The comparison refused the member because something stood there.
		if !s.Present {
			return false
		}
	default:
		return false
	}
	if !s.Present {
		return s.Class == "" && s.Length == 0 && s.ETag == ""
	}
	return s.Class != ""
}

// role defaults the verifying read, the one every deleted member has.
func (s ItemSuccessor) role() string {
	if s.Role == "" {
		return SuccessorAfter
	}
	return s.Role
}

// sameAs compares two readings by their own facts, ignoring when each was
// written, so the identical observation is idempotent and another is a conflict.
func (s ItemSuccessor) sameAs(other ItemSuccessor) bool {
	return s.Present == other.Present && s.Class == other.Class && s.Length == other.Length && s.ETag == other.ETag && s.role() == other.role()
}

// MemberBaseline is the identity a deletion member's comparison was made
// against (ADR 0093 decision 3): the listed row as a plain object for an
// ordinary member, or the HEAD taken first for a member listed as zero bytes
// or as a static large object. It is written with the member's outcome and
// is immutable, so a conflict keeps the identity it expected beside the one
// it found. (ItemBaseline, elsewhere, is a transfer's acknowledged state.)
type MemberBaseline struct {
	Class  string
	Length int64
	ETag   string
}

func (b MemberBaseline) valid() bool {
	return b.Class != "" && utf8.ValidString(b.Class) && utf8.ValidString(b.ETag) && b.Length >= 0
}

// Validate permits refusal before a write; invalid writes still latch the store.
func (i ItemIdentity) Validate() error {
	if !i.valid() {
		return ErrInvalidItemRecord
	}
	return nil
}

func (i ItemIntent) Validate() error {
	if !i.valid() {
		return ErrInvalidItemRecord
	}
	return nil
}

func (d ItemDiagnostic) valid() bool {
	if d == (ItemDiagnostic{}) {
		return true
	}
	switch d.Stage {
	case StagePlanning, StageSourceRead, StageEngine, StageStaging, StageCommit, StageMetadata,
		StageVerification, StageLocalRead, StagePlacement, StageCleanup, StageRecording, StageUnknown:
	default:
		return false
	}
	switch d.Code {
	case CodeCancelled, CodeTimeout, CodeAuthentication, CodeForbidden, CodeMissing, CodeConflict, CodeLink,
		CodeUnsupported, CodeIO, CodeIncomplete, CodeUnverified, CodeUnknown:
	default:
		return false
	}
	return d.HTTPStatus == 0 || (d.HTTPStatus >= 100 && d.HTTPStatus <= 599)
}

func (o ItemObservation) valid(phase string, admitted bool) bool {
	if !o.Diagnostic.valid() || o.CopiedBytes < 0 || (o.Result != ItemCopied && o.CopiedBytes != 0) {
		return false
	}
	if (o.Result == ItemFailed || o.Result == ItemConflict || o.Result == ItemRefused) && o.Diagnostic == (ItemDiagnostic{}) {
		return false
	}
	if phase == "verification" {
		return (o.Result == ItemVerified || o.Result == ItemFailed) && o.Effect == EffectNotAttempted
	}
	if phase != "execution" {
		return false
	}
	switch o.Result {
	case ItemSame, ItemNotAttempted:
		return o.Effect == EffectNotAttempted
	case ItemRefused:
		// A staged overwrite refused at the fixed point leaves the engine's
		// staged bytes unresolved (ADR 0030), so an admitted refusal may carry
		// the unresolved effect; an immediate (native) or planning-time refusal
		// attempts nothing and stays not-attempted.
		return o.Effect == EffectNotAttempted || (admitted && o.Effect == EffectUnresolved)
	case ItemCopied:
		return admitted && (o.Effect == EffectReportedApplied || o.Effect == EffectUnresolved)
	case ItemFailed, ItemConflict:
		return admitted && (o.Effect == EffectNotAttempted || o.Effect == EffectUnresolved)
	default:
		return false
	}
}
