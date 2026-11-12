package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ImportOutcome is what a restore did with one backed-up bookmark.
type ImportOutcome int

const (
	// ImportSkipped: an identical configuration was already present.
	ImportSkipped ImportOutcome = iota
	// ImportCreated: the bookmark was not present and was created.
	ImportCreated
	// ImportUnresolved: the insert may or may not have committed because the
	// commit reply was lost; local state must be reloaded to settle it.
	ImportUnresolved
)

// ImportBookmark creates a credential-less bookmark from a backup's complete
// configuration (ADR 0098 restore), but only when no existing bookmark
// already has the identical configuration. It never overwrites one, so a
// restore onto a populated instance is additive and a repeat import of the
// same file adds nothing; two bookmarks that differ in any restorable field,
// such as the storage override or the trusted CA, are kept as distinct
// connections. The duplicate check and the insert share one write
// transaction, so two concurrent imports cannot both insert the same
// configuration, and a lost commit reply is reconciled through the same
// publication path Save uses rather than reported as a plain failure.
func (st *Store) ImportBookmark(ctx context.Context, b Bookmark, caPEM []byte) (ImportOutcome, error) {
	// Normalised first, as Save does, so a legacy backup with no version
	// imports as v1, a v3 backup with blank domains imports with Default,
	// validate judges the stored form, and the identity below matches a
	// saved bookmark's (ADR 0100).
	NormalizeAuth(&b)
	if err := validate(b); err != nil {
		return ImportSkipped, err
	}
	if b.TLSMode == swiftcontrol.TLSModeCustomCA {
		if len(caPEM) == 0 {
			return ImportSkipped, errors.New("custom CA bookmark has no certificate")
		}
		// A backup file is operator-editable, so trust nothing in it: accept
		// only bare certificates and store that canonical form, never a key or
		// other material a hand-edited bundle might carry.
		canonical, err := CanonicalCACertificates(caPEM)
		if err != nil {
			return ImportSkipped, err
		}
		caPEM = canonical
	} else {
		caPEM = nil
	}
	if b.SegmentPlacement == "" {
		b.SegmentPlacement = PlacementRcloneDefault
	}
	if b.PerformanceProfile == "" {
		b.PerformanceProfile = "inherit"
	}
	if b.TLSMode == "" {
		b.TLSMode = swiftcontrol.TLSModeSystem
	}
	want := identityOf(b, caPEM)

	var proposed record
	outcome := ImportSkipped
	published := false

	// Hold the store lock across the write and its reconciliation, as Save
	// does, so publicationResult can settle st.unresolved without a race.
	st.mu.Lock()
	commitPossible, err := st.writePublication(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, selectColumns)
		if err != nil {
			return err
		}
		var existing []record
		for rows.Next() {
			r, err := scanRecord(rows.Scan)
			if err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, r := range existing {
			var existingCA []byte
			if r.ca.Valid {
				existingCA = []byte(r.ca.String)
			}
			if identityOf(r.Bookmark, existingCA) == want {
				return nil // an identical configuration is already present
			}
		}
		now := time.Now().UTC().Truncate(time.Second)
		b.ID = newID()
		b.CreatedAt = now
		b.UpdatedAt = now
		if caPEM != nil {
			b.TLSCAID = newID()
		}
		proposed.Bookmark = b
		proposed.generation, proposed.revision = 1, 1
		if err := publishRecord(ctx, tx, record{}, proposed, caPEM, true); err != nil {
			return err
		}
		published = true
		outcome = ImportCreated
		return nil
	})
	if err != nil {
		// A lost commit reply after the insert was issued may still have
		// committed the row; reconcile it to a confirmed create or an explicit
		// unresolved outcome rather than reporting a plain failure.
		if published && commitPossible {
			err = st.publicationResult(ctx, record{}, proposed, err, true)
		}
		st.mu.Unlock()
		if err != nil {
			if errors.Is(err, ErrPublicationUnresolved) {
				return ImportUnresolved, err
			}
			return ImportSkipped, err
		}
		return ImportCreated, nil
	}
	st.mu.Unlock()
	return outcome, nil
}

// identity is a bookmark's complete restorable configuration as a single
// comparable value, so a restore can tell an identical connection from a
// distinct one. It names every field a restore writes and the trusted
// certificates in their canonical form; the credential is not part of it
// because a restore never carries one. A struct rather than a joined string
// keeps field boundaries exact, so two bookmarks cannot collide by moving
// text across fields.
type identity struct {
	displayName, authURL, username, storageURLOverride string
	authVersion                                        int
	userDomain, projectName, projectDomain, region     string
	tlsMode, performanceProfile, segmentPlacement      string
	chunkSizeBytes                                     int64
	largeObjects                                       string
	caCertificates                                     string
}

// identityOf reduces caPEM to its canonical certificates so trust is compared
// consistently however the stored or exported bundle was encoded.
func identityOf(b Bookmark, caPEM []byte) identity {
	return identity{
		displayName:        b.DisplayName,
		authURL:            b.AuthURL,
		username:           b.Username,
		storageURLOverride: b.StorageURLOverride,
		authVersion:        b.AuthVersion,
		userDomain:         b.UserDomain,
		projectName:        b.ProjectName,
		projectDomain:      b.ProjectDomain,
		region:             b.Region,
		tlsMode:            string(b.TLSMode),
		performanceProfile: b.PerformanceProfile,
		segmentPlacement:   string(b.SegmentPlacement),
		chunkSizeBytes:     b.ChunkSizeBytes,
		largeObjects:       b.LargeObjects,
		caCertificates:     string(CertificatesOnly(caPEM)),
	}
}

// Validate reports whether a bookmark's fields are acceptable, so a restore
// can check an entire backup before it applies any of it. It judges the
// normalised form, as the store does, so a backup that omits the version
// validates as the v1 bookmark a restore would store.
func Validate(b Bookmark) error {
	NormalizeAuth(&b)
	return validate(b)
}
