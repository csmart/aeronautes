package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxRecordedFragments is how many entries one item's record holds.
//
// A dynamic manifest's prefix can hold an arbitrary number of objects and
// recording one replacement must not size its memory or its storage to a
// remote prefix (K-PERF-001), so a record whose Counted exceeds its Named
// is admitted and says so rather than reporting the names it holds as the
// whole of the set. That case is a LISTED prefix, which stays listable
// after the replacement; the bound is above what a static manifest this
// build will read can contain, so a static list is recorded whole or its
// inventory reports itself unread. Nothing here truncates a set that
// cannot be read again.
const MaxRecordedFragments = 10000

// ItemPredecessor is what was live at an item's destination immediately
// before the item was published over it, as the commit read it, with the
// segment objects a replaced manifest named (ADR 0081 decisions 5 and 7).
//
// It is an observation and never an outcome: the contract admits a racing
// writer, so the object actually replaced may not be this one. Nothing in
// it authorises removing anything.
type ItemPredecessor struct {
	// Present is false when the name held nothing at the moment of
	// publishing although the preflight found it live.
	Present      bool
	ETag         string
	Length       int64
	LastModified time.Time
	Class        string
	// Prefix is the dynamic manifest's container and prefix, still listable
	// after the replacement, empty for anything else.
	Prefix string
	// Fragments are the segment objects named, in the order they were read.
	// A page of items leaves them out and carries Named instead, because a
	// page is bounded by its item limit and one item can name thousands.
	Fragments []ItemFragment
	// Named is how many segment names the record holds, at most
	// MaxRecordedFragments.
	Named int
	// Counted is how many segment entries were read, which exceeds Named
	// when the cap stopped naming them.
	Counted int
	// Inline, Ranged and Nested count the static entries that are not
	// simple whole-object remote parts.
	Inline, Ranged, Nested int
	// Exact is true only when the entries read are the whole set the
	// manifest named.
	Exact bool
	// Unread is why no inventory could be taken, empty when one was.
	Unread    string
	CreatedAt time.Time
}

// ItemFragment is one entry a replaced manifest named, in its own position.
// An inline or unrecognised entry names no remote object and holds its
// position with no name, because where it sat is part of what the manifest
// named.
type ItemFragment struct {
	Container string
	Object    string
	Shape     FragmentShape
}

// FragmentShape is what a manifest entry referenced. The values are the
// durable ones; internal/transfer/local names the same set.
type FragmentShape string

const (
	FragmentWhole        FragmentShape = "whole"
	FragmentRanged       FragmentShape = "ranged"
	FragmentNested       FragmentShape = "nested"
	FragmentRangedNested FragmentShape = "ranged_nested"
	FragmentInline       FragmentShape = "inline"
	FragmentUnknown      FragmentShape = "unknown"
)

// named says the shape references a remote object.
func (s FragmentShape) named() bool {
	switch s {
	case FragmentWhole, FragmentRanged, FragmentNested, FragmentRangedNested:
		return true
	case FragmentInline, FragmentUnknown:
		return false
	}
	return false
}

func (s FragmentShape) valid() bool {
	switch s {
	case FragmentWhole, FragmentRanged, FragmentNested, FragmentRangedNested, FragmentInline, FragmentUnknown:
		return true
	}
	return false
}

// Truncated says the record names fewer segments than were read.
func (p ItemPredecessor) Truncated() bool { return p.Counted > p.Named }

func (f ItemFragment) valid() bool {
	if !f.Shape.valid() || !utf8.ValidString(f.Container) || !utf8.ValidString(f.Object) {
		return false
	}
	if !f.Shape.named() {
		return f.Container == "" && f.Object == ""
	}
	return f.Container != "" && f.Object != "" && !strings.ContainsRune(f.Container, '/')
}

func (p ItemPredecessor) valid() bool {
	if p.Length < 0 || p.Counted < 0 || p.Named < 0 || p.Inline < 0 || p.Ranged < 0 || p.Nested < 0 {
		return false
	}
	if p.Named > MaxRecordedFragments || p.Counted < p.Named || p.Named < len(p.Fragments) {
		return false
	}
	for _, text := range []string{p.ETag, p.Class, p.Prefix, p.Unread} {
		if !utf8.ValidString(text) {
			return false
		}
	}
	for _, fragment := range p.Fragments {
		if !fragment.valid() {
			return false
		}
	}
	// Nothing observed is nothing to describe: a name that went away
	// between the preflight and the publication has no facts of its own.
	if !p.Present {
		return p.ETag == "" && p.Length == 0 && p.Class == "" && p.Counted == 0 && p.Named == 0 &&
			p.Inline == 0 && p.Ranged == 0 && p.Nested == 0 && p.Prefix == "" && len(p.Fragments) == 0
	}
	return true
}

// Validate permits refusal before a write, on the same derivation the write
// performs, so a caller can refuse an impossible record without latching
// the store on it.
func (p ItemPredecessor) Validate() error {
	p.Named = len(p.Fragments)
	if !p.valid() {
		return ErrInvalidItemRecord
	}
	return nil
}

// RecordPredecessor saves what was seen at a name before publishing over
// it, with the names it found, and refuses a second, different reading of
// the same item: an observation of what was there cannot be revised,
// because a later reading would be of something else.
//
// It is called before the publication it precedes, so a record that cannot
// be saved stops the replacement rather than letting it land unrecorded.
func (s *Store) RecordPredecessor(ctx context.Context, jobID string, identity ItemIdentity, observed ItemPredecessor) (err error) {
	defer s.itemWriteFailed(&err)
	// Named is derived from what is handed over, so a caller cannot record
	// a number of names it did not supply.
	observed.Named = len(observed.Fragments)
	if !identity.valid() || !observed.valid() {
		return ErrInvalidItemRecord
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		if err := itemJob(ctx, tx, jobID, true); err != nil {
			return err
		}
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, identity.Key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if item.ItemIdentity != identity {
			return ErrItemRecordConflict
		}
		prior, err := readPredecessor(ctx, tx, item.ID)
		if err == nil {
			// The entries too, because a second reading that named other
			// objects is another observation and not this one again.
			prior.Fragments, err = readFragments(ctx, tx, item.ID)
			if err != nil {
				return err
			}
			if prior.sameAs(observed) {
				return nil
			}
			return ErrItemRecordConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_item_predecessors
			(item_id, present, etag, length, last_modified, class, prefix, counted,
			 inline_entries, ranged_entries, nested_entries, exact, unread, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, observed.Present, observed.ETag, observed.Length, formatPredecessorTime(observed.LastModified),
			observed.Class, observed.Prefix, observed.Counted, observed.Inline, observed.Ranged,
			observed.Nested, observed.Exact, observed.Unread, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		for position, fragment := range observed.Fragments {
			if _, err := tx.ExecContext(ctx, `INSERT INTO job_item_predecessor_fragments
				(item_id, position, shape, container, object) VALUES (?, ?, ?, ?, ?)`,
				item.ID, position, string(fragment.Shape), fragment.Container, fragment.Object); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReadPredecessor is the whole record for one item, names included, which
// is what an explicit cleanup would have to read to name what it is about
// to clear. An item that was never admitted is ErrItemNotFound; an
// admitted item with no record read is a zero value with ok false.
func (s *Store) ReadPredecessor(ctx context.Context, jobID, key string) (ItemPredecessor, bool, error) {
	var observed ItemPredecessor
	var missing, found bool
	err := s.readItems(ctx, jobID, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM job_items WHERE job_id=? AND item_key=?`, jobID, key).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			missing = true
			return nil
		}
		if err != nil {
			return err
		}
		observed, err = readPredecessor(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		observed.Fragments, err = readFragments(ctx, tx, id)
		if err != nil {
			return err
		}
		// The header states how many names it holds, so a record whose rows
		// do not match it is not a record of anything.
		if observed.Named != len(observed.Fragments) || !observed.valid() {
			return ErrInvalidItemRecord
		}
		found = true
		return nil
	})
	switch {
	case err != nil:
		return ItemPredecessor{}, false, err
	case missing:
		return ItemPredecessor{}, false, ErrItemNotFound
	}
	return observed, found, nil
}

const selectPredecessor = `SELECT present, etag, length, last_modified, class, prefix, counted,
	inline_entries, ranged_entries, nested_entries, exact, unread, created_at,
	(SELECT COUNT(*) FROM job_item_predecessor_fragments f WHERE f.item_id=job_item_predecessors.item_id)
	FROM job_item_predecessors`

// readPredecessor reads one item's header facts without the names.
func readPredecessor(ctx context.Context, tx *sql.Tx, itemID int64) (ItemPredecessor, error) {
	var observed ItemPredecessor
	var modified, created string
	err := tx.QueryRowContext(ctx, selectPredecessor+` WHERE item_id=?`, itemID).Scan(&observed.Present,
		&observed.ETag, &observed.Length, &modified, &observed.Class, &observed.Prefix, &observed.Counted,
		&observed.Inline, &observed.Ranged, &observed.Nested, &observed.Exact, &observed.Unread, &created,
		&observed.Named)
	if err != nil {
		return ItemPredecessor{}, err
	}
	if err := observed.parseTimes(modified, created); err != nil {
		return ItemPredecessor{}, err
	}
	return observed, nil
}

func (p *ItemPredecessor) parseTimes(modified, created string) error {
	if modified != "" {
		at, err := time.Parse(time.RFC3339Nano, modified)
		if err != nil {
			return err
		}
		p.LastModified = at
	}
	at, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return err
	}
	p.CreatedAt = at
	return nil
}

// readFragments is one item's recorded entries in their own order.
func readFragments(ctx context.Context, tx *sql.Tx, itemID int64) ([]ItemFragment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT shape, container, object FROM job_item_predecessor_fragments
		WHERE item_id=? ORDER BY position`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fragments []ItemFragment
	for rows.Next() {
		var fragment ItemFragment
		if err := rows.Scan(&fragment.Shape, &fragment.Container, &fragment.Object); err != nil {
			return nil, err
		}
		fragments = append(fragments, fragment)
	}
	return fragments, rows.Err()
}

// sameAs compares two readings by their own facts, ignoring when each was
// written, so a repeated write of the identical observation is idempotent
// and a different one is a conflict.
//
// The entries are part of the comparison, in order. A header comparison
// alone accepted a second reading that named different objects, or the same
// objects in another order, and reported it as the same fact while the
// store still held the first names — which is the one thing the refusal of
// a revised observation exists to prevent.
func (p ItemPredecessor) sameAs(other ItemPredecessor) bool {
	if p.Present != other.Present || p.ETag != other.ETag || p.Length != other.Length ||
		!p.LastModified.Equal(other.LastModified) || p.Class != other.Class || p.Prefix != other.Prefix ||
		p.Counted != other.Counted || p.Named != other.Named || p.Inline != other.Inline ||
		p.Ranged != other.Ranged || p.Nested != other.Nested || p.Exact != other.Exact || p.Unread != other.Unread {
		return false
	}
	if len(p.Fragments) != len(other.Fragments) {
		return false
	}
	for i := range p.Fragments {
		if p.Fragments[i] != other.Fragments[i] {
			return false
		}
	}
	return true
}

func formatPredecessorTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

// readPredecessors fills the header facts for a page of items. The names
// are left out on purpose: a page is bounded by its item limit, and one
// item can name thousands of segments, so the names are read per item.
func readPredecessors(ctx context.Context, tx *sql.Tx, jobID string, cursor int64, items []ItemRecord) error {
	byID := make(map[int64]*ItemRecord, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.item_id, p.present, p.etag, p.length, p.last_modified,
		p.class, p.prefix, p.counted, p.inline_entries, p.ranged_entries, p.nested_entries, p.exact,
		p.unread, p.created_at, (SELECT COUNT(*) FROM job_item_predecessor_fragments f WHERE f.item_id=p.item_id)
		FROM job_item_predecessors p JOIN job_items i ON i.id=p.item_id
		WHERE i.job_id=? AND i.id>? AND i.id<=? ORDER BY i.id`, jobID, cursor, items[len(items)-1].ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var observed ItemPredecessor
		var modified, created string
		if err := rows.Scan(&id, &observed.Present, &observed.ETag, &observed.Length, &modified,
			&observed.Class, &observed.Prefix, &observed.Counted, &observed.Inline, &observed.Ranged,
			&observed.Nested, &observed.Exact, &observed.Unread, &created, &observed.Named); err != nil {
			return err
		}
		if err := observed.parseTimes(modified, created); err != nil {
			return err
		}
		item := byID[id]
		if item == nil || !observed.valid() {
			return ErrInvalidItemRecord
		}
		item.Predecessor = &observed
	}
	return rows.Err()
}
