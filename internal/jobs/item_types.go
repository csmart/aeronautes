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
	Staging             *ItemLocation
	Planned             bool
	Admitted            bool
	ListedBytes         int64
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
	CodeUnsupported    ItemCode = "unsupported"
	CodeIO             ItemCode = "io"
	CodeIncomplete     ItemCode = "incomplete"
	CodeUnverified     ItemCode = "unverified"
	CodeUnknown        ItemCode = "unknown"
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
	return utf8.ValidString(i.Key) && i.Source.valid() && i.Destination.valid()
}

func (i ItemIntent) valid() bool {
	if !i.ItemIdentity.valid() || i.ListedBytes < 0 || i.Staging != nil && !i.Staging.valid() {
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
	case CodeCancelled, CodeTimeout, CodeAuthentication, CodeForbidden, CodeMissing, CodeConflict,
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
	case ItemSame, ItemRefused, ItemNotAttempted:
		return o.Effect == EffectNotAttempted
	case ItemCopied:
		return admitted && (o.Effect == EffectReportedApplied || o.Effect == EffectUnresolved)
	case ItemFailed, ItemConflict:
		return admitted && (o.Effect == EffectNotAttempted || o.Effect == EffectUnresolved)
	default:
		return false
	}
}
