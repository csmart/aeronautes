// Package plan computes copy plans: the §15.2 bounded-memory streaming
// merge-join over two name-sorted deep listings, classifying each name
// with §28.4 semantics (equality is proven, not assumed — large objects
// and possible manifests compare conservatively) and routing each
// transfer down the rclone or native path per the measured §14.3
// classification. The planner never loads a namespace into memory and
// never decides from an execution engine's own comparison.
package plan

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// Verdict classifies one name in the joined namespace.
type Verdict string

const (
	// VerdictNew is a source object with no destination counterpart.
	VerdictNew Verdict = "new"
	// VerdictSame is proven equality (§28.4 EqualStrong): plain
	// size+ETag, or identical SLO segmentation from the listing.
	VerdictSame Verdict = "same"
	// VerdictChanged is proven difference between two plain objects.
	VerdictChanged Verdict = "changed"
	// VerdictUnknown is §28.4's conservative bucket: equality cannot be
	// proven from listings alone. HEAD resolution may prove equality or
	// a missing destination; unequal existing destinations are refused.
	VerdictUnknown Verdict = "unknown"
	// VerdictDestOnly is a destination object with no source
	// counterpart — Copy ignores it; Mirror preview compares the scopes.
	VerdictDestOnly Verdict = "dest_only"
)

// Route says which engine carries a transferred name.
type Route string

const (
	// RouteNone: nothing to transfer (Same, DestOnly).
	RouteNone Route = ""
	// RouteRclone: safe for the pinned rclone (§14.3 measured set).
	RouteRclone Route = "rclone"
	// RouteNative: rclone silently drops or cannot list this identity —
	// the byte-preserving native path is mandatory.
	RouteNative Route = "native"
)

// Reason qualifies a conservative or specially-handled item.
type Reason string

const (
	ReasonNone Reason = ""
	// ReasonLargeObject: an slo_etag hint on either side without
	// listing-provable equality.
	ReasonLargeObject Reason = "large_object"
	// ReasonPossibleManifest: a zero-byte listing row may be a DLO
	// manifest (indistinguishable from an empty object without a HEAD),
	// so neither equality nor a naive byte copy is safe — the executor
	// must HEAD before acting (§28.4 "treat DLOs conservatively").
	ReasonPossibleManifest Reason = "possible_manifest"
	// ReasonUnsafeName: the name routed native purely because of the
	// §14.3 classification.
	ReasonUnsafeName Reason = "unsafe_name"
)

// Item is one joined name. Name is relative to the job prefixes; "" is
// legal and names the prefix object itself (an object named exactly the
// source prefix, e.g. a trailing-slash folder marker at the job root).
type Item struct {
	Name        string
	Verdict     Verdict
	Route       Route
	Reason      Reason
	SourceBytes int64
	SourceETag  string
	DestBytes   int64
	// DestETag preserves the observed comparison evidence. It cannot
	// authorise an overwrite or exclude a later external writer.
	DestETag string
}

// Summary is the §65.4 preflight surface: honest conservative counts
// (§28.4's preview contract), existing-destination candidate counts, and
// candidate routing. Versioned-destination disclosure is the
// caller's job — it comes from the destination container's headers, not
// the join.
type Summary struct {
	New      int64 `json:"new"`
	Same     int64 `json:"same"`
	Changed  int64 `json:"changed"`
	Unknown  int64 `json:"unknown"`
	DestOnly int64 `json:"destOnly"`
	// These legacy field names cover New + Changed + Unknown comparison
	// candidates, including refused objects. They are not transfer estimates.
	TransferObjects int64 `json:"transferObjects"`
	TransferBytes   int64 `json:"transferBytes"`
	// Overwrites counts Changed + Unknown existing-destination candidates.
	// Changed is refused; Unknown needs resolution before a decision.
	Overwrites int64 `json:"overwrites"`
	// NativeRouted counts candidates requiring native handling if eligible.
	NativeRouted  int64 `json:"nativeRouted"`
	DestOnlyBytes int64 `json:"destOnlyBytes"`
}

// RcloneUnsafe reports whether the pinned rclone (docs/rclone.md) would
// violate this full object name's identity: leading "/", trailing "/",
// and "//" components are silently dropped (exit 0); "." or ".." path
// segments are silently skipped by --files-from-raw and silently
// RENAMED to fullwidth dots (U+FF0E) by traversal — both measured on
// v1.74.3 against real Swift; a newline cannot ride any rclone list
// (--files-from0 does not exist); and a name containing a rune the
// engine reserves as a substitute or a quote is decoded to another
// spelling, so a raw list entry matches nothing and is silently not
// copied (exit 0), measured on v1.74.3 against real Swift and the mock
// (2026-09-11 name encoding experiment). Evaluate full names — a prefix
// can make a safe relative name unsafe.
func RcloneUnsafe(name string) bool {
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.Contains(name, "//") || strings.Contains(name, "\n") {
		return true
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return strings.ContainsFunc(name, engineReservedRune)
}

// engineReservedRune reports a rune the pinned engine (v1.74.3) uses as
// an encoding substitute or quote on any supported platform, pinned to
// its frozen encoder source: the quote rune, the control pictures it
// substitutes for NUL, the C0 range and DEL, the space picture it uses
// for a trailing space, and the full-width forms it uses for the slash,
// the backslash, the full stop, and the seven characters Windows
// forbids. One set applies on every platform, so a name that would be
// respelled on one target is carried natively on all of them.
func engineReservedRune(r rune) bool {
	switch r {
	case '\u201b', // ‛ quote rune
		'\u2420', // ␠ trailing space substitute
		'\uff0f', // ／ slash
		'\uff3c', // ＼ backslash
		'\uff0e', // ． full stop
		'\uff1a', // ： colon
		'\uff1f', // ？ question mark
		'\uff02', // ＂ quotation mark
		'\uff0a', // ＊ asterisk
		'\uff1c', // ＜ less than
		'\uff1e', // ＞ greater than
		'\uff5c': // ｜ pipe
		return true
	}
	// ␀ through ␟ for NUL and the C0 range, and ␡ for DEL.
	return r >= '\u2400' && r <= '\u241f' || r == '\u2421'
}

// Copy merge-joins two deep listing streams (source and destination,
// both listed with the given prefixes) and emits one Item per name,
// streaming: nothing is accumulated beyond the Summary. Both streams
// must be deep (no PrefixEntry rows) and name-sorted as Swift returns
// them; violations are errors, not silent misplans. The emit callback
// may be nil when only the Summary is wanted; a non-nil emit error
// aborts the join.
func Copy(ctx context.Context, source, dest iter.Seq2[swiftcontrol.ListingEntry, error],
	srcPrefix, dstPrefix string, emit func(Item) error) (Summary, error) {

	var summary Summary
	srcNext, srcStop := iter.Pull2(source)
	defer srcStop()
	dstNext, dstStop := iter.Pull2(dest)
	defer dstStop()

	src := &side{next: srcNext, prefix: srcPrefix, label: "source"}
	dst := &side{next: dstNext, prefix: dstPrefix, label: "destination"}
	if err := src.advance(); err != nil {
		return summary, err
	}
	if err := dst.advance(); err != nil {
		return summary, err
	}

	for src.ok || dst.ok {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		var item Item
		var advanceSource, advanceDest bool
		switch {
		case !dst.ok || (src.ok && src.rel < dst.rel):
			item = classifyNew(src.row, src.rel, srcPrefix, dstPrefix)
			advanceSource = true
		case !src.ok || dst.rel < src.rel:
			item = Item{Name: dst.rel, Verdict: VerdictDestOnly, DestBytes: dst.row.Bytes, DestETag: dst.row.ETag}
			advanceDest = true
		default:
			item = classifyPair(src.row, dst.row, src.rel, srcPrefix, dstPrefix)
			advanceSource, advanceDest = true, true
		}
		summary.add(item)
		if emit != nil {
			if err := emit(item); err != nil {
				return summary, err
			}
		}
		// A checked consumer must retain this result before another read
		// can fail or begin work past a refused acknowledgement.
		if advanceSource {
			if err := src.advance(); err != nil {
				return summary, err
			}
		}
		if advanceDest {
			if err := dst.advance(); err != nil {
				return summary, err
			}
		}
	}
	return summary, nil
}

// side is one pull-iterator with prefix stripping and order/type
// enforcement.
type side struct {
	next   func() (swiftcontrol.ListingEntry, error, bool)
	prefix string
	label  string

	row  swiftcontrol.ObjectEntry
	rel  string
	last string
	ok   bool
}

func (s *side) advance() error {
	entry, err, ok := s.next()
	if !ok {
		s.ok = false
		return nil
	}
	if err != nil {
		s.ok = false
		return fmt.Errorf("%s listing: %w", s.label, err)
	}
	object, isObject := entry.(swiftcontrol.ObjectEntry)
	if !isObject {
		return fmt.Errorf("%s listing: got a prefix row — the join needs deep listings", s.label)
	}
	if !strings.HasPrefix(object.Name, s.prefix) {
		return fmt.Errorf("%s listing: %q outside prefix %q", s.label, object.Name, s.prefix)
	}
	// Swift orders listings by raw name bytes; a regression means a
	// paging bug and would silently corrupt the join.
	if s.last != "" && object.Name <= s.last {
		return fmt.Errorf("%s listing out of order: %q after %q", s.label, object.Name, s.last)
	}
	s.last = object.Name
	s.row = object
	s.rel = object.Name[len(s.prefix):]
	s.ok = true
	return nil
}

func classifyNew(s swiftcontrol.ObjectEntry, rel, srcPrefix, dstPrefix string) Item {
	item := Item{
		Name: rel, Verdict: VerdictNew,
		SourceBytes: s.Bytes, SourceETag: s.ETag,
	}
	// A new zero-byte or SLO-listed row still needs executor HEAD
	// resolution before a native byte copy (a DLO manifest's logical
	// bytes are not its listed bytes).
	switch {
	case s.SLOListed:
		item.Reason = ReasonLargeObject
	case s.Bytes == 0:
		item.Reason = ReasonPossibleManifest
	}
	item.Route = route(rel, srcPrefix, dstPrefix, &item)
	return item
}

func classifyPair(s, d swiftcontrol.ObjectEntry, rel, srcPrefix, dstPrefix string) Item {
	item := Item{
		Name:        rel,
		SourceBytes: s.Bytes, SourceETag: s.ETag,
		DestBytes: d.Bytes, DestETag: d.ETag,
	}
	switch {
	case s.SLOListed || d.SLOListed:
		// Matching slo_etag (MD5 of concatenated segment ETags) plus
		// matching logical size proves identical segmentation. Anything
		// less is Unknown — an unequal manifest does not prove different
		// content (§28.4: the destination may legitimately re-segment).
		if s.SLOListed && d.SLOListed && s.SLOETag == d.SLOETag && s.Bytes == d.Bytes {
			item.Verdict = VerdictSame
			return item
		}
		item.Verdict, item.Reason = VerdictUnknown, ReasonLargeObject
	case s.Bytes == 0 || d.Bytes == 0:
		// A zero-byte row may be a DLO manifest whose listing says
		// nothing about its logical content — even "0 == 0, ETags match"
		// proves nothing. HEAD resolution decides (§28.4).
		item.Verdict, item.Reason = VerdictUnknown, ReasonPossibleManifest
	case s.Bytes != d.Bytes:
		item.Verdict = VerdictChanged
	case etag(s.ETag) == etag(d.ETag):
		item.Verdict = VerdictSame
		return item
	default:
		// Same size, differing MD5, both provably plain (non-zero, no
		// slo_etag on a cluster running the SLO middleware).
		item.Verdict = VerdictChanged
	}
	item.Route = route(rel, srcPrefix, dstPrefix, &item)
	return item
}

// route picks the engine for a transferred name: native whenever either
// full name (source or destination) is rclone-unsafe — a safe relative
// name under an unsafe prefix still gets dropped.
func route(rel, srcPrefix, dstPrefix string, item *Item) Route {
	if RcloneUnsafe(srcPrefix+rel) || RcloneUnsafe(dstPrefix+rel) {
		if item.Reason == ReasonNone {
			item.Reason = ReasonUnsafeName
		}
		return RouteNative
	}
	return RouteRclone
}

func etag(e string) string { return strings.Trim(e, `"`) }

func (s *Summary) add(item Item) {
	switch item.Verdict {
	case VerdictNew:
		s.New++
	case VerdictSame:
		s.Same++
	case VerdictChanged:
		s.Changed++
	case VerdictUnknown:
		s.Unknown++
	case VerdictDestOnly:
		s.DestOnly++
		s.DestOnlyBytes += item.DestBytes
	}
	switch item.Verdict {
	case VerdictNew, VerdictChanged, VerdictUnknown:
		s.TransferObjects++
		s.TransferBytes += item.SourceBytes
		if item.Verdict != VerdictNew {
			s.Overwrites++
		}
		if item.Route == RouteNative {
			s.NativeRouted++
		}
	}
}
