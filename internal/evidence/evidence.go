// Package evidence is the §24 segment-evidence engine: observations of
// manifest references recorded per bookmark in segment_evidence, and the
// classification the container modes (§52) consume. Heuristics are
// applied at query time, never persisted, and — the critical safety rule
// — nothing here ever authorizes destruction (K-SEG-002): Confirmed
// proves *referenced*, not *unshared* (§24.1, §28.6b).
package evidence

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// Confidence is the §24 evidence level.
type Confidence string

const (
	Confirmed Confidence = "confirmed"
	Declared  Confidence = "declared"
	Suspected Confidence = "suspected"
)

// Reason records where a piece of evidence came from.
type Reason string

const (
	ObservedDLO     Reason = "observed_dlo"
	ObservedSLO     Reason = "observed_slo"
	AppUpload       Reason = "app_upload"
	UserPin         Reason = "user_pin"
	NamingHeuristic Reason = "naming_heuristic"
)

// Observation is one piece of segment evidence to record.
type Observation struct {
	Container string
	// Prefix is the referenced segment prefix inside the container, ""
	// for container-level evidence.
	Prefix       string
	Confidence   Confidence
	Reason       Reason
	SourceObject string
}

// Store persists and queries segment evidence.
type Store struct {
	db *db.DB
}

// NewStore builds a Store over the state database.
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// Record upserts one observation for a bookmark: a repeat of the same
// (container, prefix, reason, source) bumps last_seen instead of piling
// up rows — §51's retention worry starts with not writing duplicates.
func (s *Store) Record(ctx context.Context, bookmarkID string, obs Observation) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE segment_evidence SET last_seen=?, confidence=?
			WHERE bookmark_id=? AND container=? AND prefix=? AND reason=?
				AND COALESCE(source_object,'')=?`,
			now, string(obs.Confidence),
			bookmarkID, obs.Container, obs.Prefix, string(obs.Reason), obs.SourceObject)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		var source *string
		if obs.SourceObject != "" {
			source = &obs.SourceObject
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO segment_evidence
				(bookmark_id, container, prefix, confidence, reason, source_object, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			bookmarkID, obs.Container, obs.Prefix, string(obs.Confidence),
			string(obs.Reason), source, now, now)
		return err
	})
}

// FromObjectDetail derives observations from an authoritative HEAD: a
// DLO manifest confirms its segment container+prefix as referenced.
func FromObjectDetail(d *swiftcontrol.ObjectDetail) []Observation {
	if d.Class != swiftcontrol.ClassDLO || d.Manifest == "" {
		return nil
	}
	container, prefix, ok := swiftcontrol.DynamicSegments(d.Manifest)
	if !ok {
		return nil
	}
	return []Observation{{
		Container:    container,
		Prefix:       prefix,
		Confidence:   Confirmed,
		Reason:       ObservedDLO,
		SourceObject: d.Name,
	}}
}

// FromSLOManifest derives observations from an exact SLO manifest: one
// per distinct (container, directory-prefix) a segment lives under.
// Inline-data and unknown entries reference nothing.
func FromSLOManifest(manifestObject string, m *swiftcontrol.SLOManifest) []Observation {
	seen := map[[2]string]bool{}
	var out []Observation
	for _, entry := range m.Entries {
		segment, ok := entry.(swiftcontrol.ObjectSegment)
		if !ok {
			continue
		}
		prefix := ""
		if slash := strings.LastIndex(segment.Path.Object, "/"); slash >= 0 {
			prefix = segment.Path.Object[:slash+1]
		}
		key := [2]string{segment.Path.Container, prefix}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Observation{
			Container:    segment.Path.Container,
			Prefix:       prefix,
			Confidence:   Confirmed,
			Reason:       ObservedSLO,
			SourceObject: manifestObject,
		})
	}
	return out
}

// Class is a container's backstage classification for the §52 modes.
type Class string

const (
	ClassNormal    Class = "normal"
	ClassSuspected Class = "suspected"
	ClassDeclared  Class = "declared"
	ClassConfirmed Class = "confirmed"
)

// SuspectedName is the §24.3 naming heuristic — display grouping only,
// never authorization for anything.
func SuspectedName(name string) bool {
	return strings.HasSuffix(name, "_segments") ||
		strings.HasSuffix(name, "-segments") ||
		name == "segments" ||
		name == ".segments"
}

// ClassifyContainers returns each named container's classification:
// recorded evidence wins (confirmed > declared > suspected), then the
// naming heuristic, then normal. Heuristic results are computed here,
// not written to the database.
func (s *Store) ClassifyContainers(ctx context.Context, bookmarkID string, names []string) (map[string]Class, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
		SELECT container, confidence FROM segment_evidence WHERE bookmark_id=?`,
		bookmarkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rank := map[Class]int{ClassNormal: 0, ClassSuspected: 1, ClassDeclared: 2, ClassConfirmed: 3}
	recorded := map[string]Class{}
	for rows.Next() {
		var container, confidence string
		if err := rows.Scan(&container, &confidence); err != nil {
			return nil, err
		}
		class := ClassSuspected
		switch Confidence(confidence) {
		case Confirmed:
			class = ClassConfirmed
		case Declared:
			class = ClassDeclared
		}
		if rank[class] > rank[recorded[container]] {
			recorded[container] = class
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]Class, len(names))
	for _, name := range names {
		switch {
		case recorded[name] != "":
			out[name] = recorded[name]
		case SuspectedName(name):
			out[name] = ClassSuspected
		default:
			out[name] = ClassNormal
		}
	}
	return out, nil
}
