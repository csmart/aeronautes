package jobs

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// ADR 0085: the acknowledged baseline is what the operator was shown at a name
// when they accepted replacing it, kept so the publication can be compared
// against it at the fixed point. It is a second record beside the predecessor
// rather than a replacement for it.

func acknowledgedBaseline() ItemBaseline {
	return ItemBaseline{Class: "dlo", Length: 3_000_000_000, ETag: "ac9c542684f271e7215c00369bbb793e"}
}

func TestAcknowledgedBaselineIsWrittenOnceAndReadBack(t *testing.T) {
	s, bookmark, _ := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/taken")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}

	// Before anything is recorded, the item exists and the baseline does not.
	// ADR 0085 makes that absence a refusal to overwrite rather than a licence,
	// so the two have to be distinguishable from a missing item.
	if read, ok, err := s.ReadAcknowledgedBaseline(t.Context(), job.ID, intent.Key); err != nil || ok || read != (ItemBaseline{}) {
		t.Fatalf("an unrecorded baseline did not read as absent: %+v ok=%v %v", read, ok, err)
	}
	if _, _, err := s.ReadAcknowledgedBaseline(t.Context(), job.ID, "never/admitted"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("an item that was never admitted = %v, want ErrItemNotFound", err)
	}

	baseline := acknowledgedBaseline()
	if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, intent.ItemIdentity, baseline); err != nil {
		t.Fatal(err)
	}
	// The same acknowledgement seeing the same object is the same fact.
	if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, intent.ItemIdentity, baseline); err != nil {
		t.Fatalf("an identical baseline was refused: %v", err)
	}
	read, ok, err := s.ReadAcknowledgedBaseline(t.Context(), job.ID, intent.Key)
	if err != nil || !ok || read != baseline {
		t.Fatalf("the baseline did not stand: %+v ok=%v %v", read, ok, err)
	}

	// It does not displace the predecessor record, which is the other half of
	// the trail: what the operator accepted, and what the route found.
	observed := replacedManifest(ItemFragment{Container: "segments", Object: "part", Shape: FragmentWhole})
	if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, observed); err != nil {
		t.Fatal(err)
	}
	if p, ok, err := s.ReadPredecessor(t.Context(), job.ID, intent.Key); err != nil || !ok || p.ETag != "ffff" {
		t.Fatalf("the predecessor record did not survive the baseline: %+v ok=%v %v", p, ok, err)
	}
	if again, ok, err := s.ReadAcknowledgedBaseline(t.Context(), job.ID, intent.Key); err != nil || !ok || again != baseline {
		t.Fatalf("the baseline did not survive the predecessor: %+v ok=%v %v", again, ok, err)
	}

	// And the row cannot be edited underneath the store. The API refuses a
	// differing re-record, but that guard is only reached through the API, and a
	// comparison at the fixed point would otherwise match against something the
	// operator may never have been shown. A review reproduced exactly this.
	err = s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE job_item_acknowledged_baselines SET etag='0000'`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatalf("an acknowledged baseline was editable: %v", err)
	}
	if stillThere, ok, readErr := s.ReadAcknowledgedBaseline(t.Context(), job.ID, intent.Key); readErr != nil || !ok || stillThere != baseline {
		t.Fatalf("the refused edit disturbed the baseline: %+v ok=%v %v", stillThere, ok, readErr)
	}
}

// The identity guard binds a baseline to the item that was actually admitted.
// Every other case here writes with the originally admitted identity, so
// removing that guard would leave them all passing while letting a baseline for
// the same key but a different destination attach itself to the admitted item.
func TestAcknowledgedBaselineRefusesAnotherItemsIdentity(t *testing.T) {
	s, bookmark, _ := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/taken")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	// The same key, otherwise valid, naming a different destination object.
	elsewhere := intent.ItemIdentity
	elsewhere.Destination.Object = "somewhere/else"
	if elsewhere == intent.ItemIdentity {
		t.Fatal("the fixture did not change the identity")
	}
	if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, elsewhere, acknowledgedBaseline()); !errors.Is(err, ErrItemRecordConflict) {
		t.Fatalf("a baseline for another item's identity = %v, want ErrItemRecordConflict", err)
	}
	// Nothing was attached to the admitted item. The write latched the store, so
	// the table is read directly.
	var rows int
	if err := s.db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM job_item_acknowledged_baselines`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a baseline was stored for an identity that was not admitted: %d rows", rows)
	}
}

// A zero-length object is a real object and its baseline is valid: the length
// check is >= 0 deliberately, and every other accepted fixture here is a three
// gigabyte manifest, which would not protect that boundary.
func TestAcknowledgedBaselineAcceptsAnEmptyObject(t *testing.T) {
	s, bookmark, _ := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/empty")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	empty := ItemBaseline{Class: "plain", Length: 0, ETag: "d41d8cd98f00b204e9800998ecf8427e"}
	if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, intent.ItemIdentity, empty); err != nil {
		t.Fatalf("an empty object's baseline was refused: %v", err)
	}
	read, ok, err := s.ReadAcknowledgedBaseline(t.Context(), job.ID, intent.Key)
	if err != nil || !ok || read != empty {
		t.Fatalf("the empty object's baseline did not stand: %+v ok=%v %v", read, ok, err)
	}
}

// A different baseline is a conflict. Two acknowledgements that saw different
// objects are two decisions, and keeping either silently would leave the
// comparison matching against something the operator may never have been shown.
// Each refusal latches the store like any other item write, so each shape of
// difference gets its own store.
func TestAcknowledgedBaselineRefusesARevision(t *testing.T) {
	for _, c := range []struct {
		name  string
		amend func(ItemBaseline) ItemBaseline
	}{
		{"a different entity tag", func(b ItemBaseline) ItemBaseline { b.ETag = "0000"; return b }},
		{"a different length", func(b ItemBaseline) ItemBaseline { b.Length++; return b }},
		{"a different class", func(b ItemBaseline) ItemBaseline { b.Class = "slo"; return b }},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, bookmark, _ := newStoreAt(t)
			job := recordedJob(t, s, TypeUpload)
			intent := uploadIntent(bookmark, "live/taken")
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			baseline := acknowledgedBaseline()
			if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, intent.ItemIdentity, baseline); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, intent.ItemIdentity, c.amend(baseline)); !errors.Is(err, ErrItemRecordConflict) {
				t.Fatalf("a revised baseline = %v, want ErrItemRecordConflict", err)
			}
			// The first one stands, and the revision did not land. A refused
			// item write latches the store, as it does for the predecessor
			// record, so the row is read directly rather than through it.
			var stored ItemBaseline
			if err := s.db.Reader().QueryRowContext(t.Context(),
				`SELECT class, length, etag FROM job_item_acknowledged_baselines`).
				Scan(&stored.Class, &stored.Length, &stored.ETag); err != nil {
				t.Fatal(err)
			}
			if stored != baseline {
				t.Fatalf("the recorded baseline did not stand: %+v", stored)
			}
		})
	}
}

// What cannot be recorded at all. An incomplete identity is the case ADR 0085
// cares about: a listing row supplies no class, so a baseline built from one
// could not be stored even if a caller tried.
func TestAcknowledgedBaselineRefusesWhatCannotIdentify(t *testing.T) {
	s, bookmark, _ := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/taken")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	// The first refusal latches the store, and every later one carries that
	// latch joined to its own reason, which is why these assert on the reason
	// rather than on the whole error.
	for _, c := range []struct {
		name     string
		baseline ItemBaseline
	}{
		{"no class, as a listing row would give", ItemBaseline{Length: 10, ETag: "ffff"}},
		{"no entity tag", ItemBaseline{Class: "plain", Length: 10}},
		{"a negative length", ItemBaseline{Class: "plain", Length: -1, ETag: "ffff"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := s.RecordAcknowledgedBaseline(t.Context(), job.ID, intent.ItemIdentity, c.baseline); !errors.Is(err, ErrInvalidItemRecord) {
				t.Fatalf("err = %v, want ErrInvalidItemRecord", err)
			}
		})
	}
	// Each refusal latches the store, as any refused item write does, so what
	// was NOT stored is read from the table rather than through the API.
	var stored int
	if err := s.db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM job_item_acknowledged_baselines`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("a baseline that cannot identify anything was stored: %d rows", stored)
	}
}
