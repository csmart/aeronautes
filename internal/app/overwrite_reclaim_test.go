package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

// wireReclamation gives the service its settings with the reclamation
// option in the named state, and returns the service so a case can move
// it again.
func wireReclamation(t *testing.T, h *harness, reclaim bool) *SettingsService {
	t.Helper()
	settings := NewSettingsService(h.db, h.transfer.runner)
	current, err := settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current.ReclaimReplacedFragments = reclaim
	if _, err := settings.Set(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	WireSettings(h.transfer, settings)
	return settings
}

// reclaimEvent is the single reclamation record for a run, or nil.
func reclaimEvent(t *testing.T, h *harness) *audit.Event {
	t.Helper()
	events, err := h.auditSvc.audit.List(t.Context(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found *audit.Event
	for i, event := range events {
		if event.Operation != "object.upload.reclaim" {
			continue
		}
		if found != nil {
			t.Fatalf("one replacement produced more than one reclamation record: %+v", events)
		}
		found = &events[i]
	}
	return found
}

// stillThere asks the store about one name, through the inspector rather
// than through this feature and by identity rather than through a listing,
// because a container listing lags a deletion (DESIGN §15.3) and would
// answer a question this case is not asking.
func stillThere(t *testing.T, h *harness, bookmark, container, name string) bool {
	t.Helper()
	if _, err := h.inspector.Object(t.Context(), bookmark, container, name); err == nil {
		return true
	} else if !strings.Contains(err.Error(), "not_found") && !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("reading %s/%s back: %v", container, name, err)
	}
	return false
}

// survivors is how many of these names the store still holds.
func survivors(t *testing.T, h *harness, bookmark, container string, names []string) int {
	t.Helper()
	count := 0
	for _, name := range names {
		if stillThere(t, h, bookmark, container, name) {
			count++
		}
	}
	return count
}

// partNames is the object names of the recorded parts.
func partNames(parts []jobs.ItemFragment) []string {
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		names = append(names, part.Object)
	}
	return names
}

// ADR 0081 decision 5: with the option on, a verified replacement removes
// the segment objects the replaced manifest named, and says so.
func TestReclamationRemovesTheRecordedSegments(t *testing.T) {
	f := newOverwriteFixture(t)
	parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != 0 {
		t.Fatalf("%d of the replaced manifest's %d segments are still there: %+v", left, len(parts), parts)
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("a reclamation left no record")
	}
	if event.Result != audit.ResultApplied || event.Resource != "replaceable/live/taken" {
		t.Errorf("the record does not report a complete removal of that name: %+v", event)
	}
	// The count is stated, because a record that says only "removed
	// segments" cannot be checked against the store.
	if !strings.Contains(event.Summary, "reported 2 segment objects the replaced manifest named as gone") {
		t.Errorf("the record does not say how many went: %q", event.Summary)
	}
}

// The option's other direction: nothing is removed, and the record says
// why rather than reporting a removal of none.
func TestReclamationLeavesThemWhenTheOptionIsOff(t *testing.T) {
	f := newOverwriteFixture(t)
	parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, false)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
		t.Fatalf("the option was off and %d of %d segments went", len(parts)-left, len(parts))
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("a declined reclamation left no record")
	}
	if event.Result != audit.ResultObserved {
		t.Errorf("a reclamation that removed nothing claims a write outcome: %q", event.Result)
	}
	// The record quotes the promise it kept, and that sentence is the one
	// the operator read, so the reason travels with it.
	for _, want := range []string{"removed none", "did not say these segments would be removed", "turned off"} {
		if !strings.Contains(event.Summary, want) {
			t.Errorf("the record does not say %q: %q", want, event.Summary)
		}
	}
}

// Rule 2: a container that keeps versions keeps the replaced version too,
// and its segments are that version's. The option does not override it,
// and the mode is read at reclamation time rather than taken from the
// sentence the operator was shown.
func TestReclamationSkipsAContainerThatKeepsVersions(t *testing.T) {
	for _, mode := range []struct {
		name    string
		history bool
	}{{name: "versions-location"}, {name: "history-location", history: true}} {
		t.Run(mode.name, func(t *testing.T) {
			f := newOverwriteFixture(t)
			parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
			f.tag = f.liveTag(t)
			f.h.mock.AddContainer("replaceable_archive", "standard")
			f.h.mock.EnableVersionArchive("replaceable", "replaceable_archive", mode.history)
			wireReclamation(t, f.h, true)
			confirmer := &recordingOverwriteConfirmer{yes: true}
			f.h.transfer.setOverwriteConfirmer(confirmer)

			id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
			if err != nil {
				t.Fatal(err)
			}
			if job := awaitTerminal(t, f.h, id); job.State != "complete" {
				t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
			}
			if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
				t.Fatalf("a versioned container lost %d of %d segments the archive needs", len(parts)-left, len(parts))
			}
			event := reclaimEvent(t, f.h)
			if event == nil || !strings.Contains(event.Summary, "keeps versions") {
				t.Fatalf("the record does not say the archive stopped it: %+v", event)
			}
			if !strings.Contains(event.Summary, "did not say these segments would be removed") {
				t.Errorf("the record does not name the answer it kept: %q", event.Summary)
			}
			// And the operator was told the same thing before answering.
			asked := confirmer.questions()
			if len(asked) != 1 || !strings.Contains(asked[0].Fragments, "none will be removed, because the container keeps versions") {
				t.Fatalf("the question did not say the segments would be kept: %+v", asked)
			}
		})
	}
}

// Rule 3 and the excluded shapes: a ranged entry names a containing
// object and a nested entry names another manifest, so neither is this
// operation's to remove. The whole part beside them is.
func TestReclamationLeavesRangedAndNestedEntriesAlone(t *testing.T) {
	f := newOverwriteFixture(t)
	f.h.mock.AddContainer("replaceable_segments", "standard")
	seedObject(f.h.mock, "replaceable_segments", "live/taken/whole", "01234567", nil)
	seedObject(f.h.mock, "replaceable_segments", "live/taken/container", "89abcdef", nil)
	seedObject(f.h.mock, "replaceable_segments", "live/taken/nested", "0123456789abcdef", nil)
	f.h.mock.PutObject("replaceable", "live/taken", swifttest.Object{
		SLO: true, Bytes: 16, ETag: `"` + md5of(md5of("01234567")+md5of("89abcdef")) + `"`,
		Data: []byte("0123456789abcdef"),
		SLOManifestJSON: `[` +
			`{"name":"/replaceable_segments/live/taken/whole","hash":"` + md5of("01234567") + `","bytes":8},` +
			`{"name":"/replaceable_segments/live/taken/container","hash":"` + md5of("89abcdef") + `","bytes":8,"range":"0-3"},` +
			`{"name":"/replaceable_segments/live/taken/nested","hash":"` + md5of("0123456789abcdef") + `","bytes":16,"sub_slo":true}]`,
	})
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	for _, name := range []string{"live/taken/container", "live/taken/nested"} {
		if !stillThere(t, f.h, f.bookmark, "replaceable_segments", name) {
			t.Errorf("%s was removed although it is not this operation's to remove", name)
		}
	}
	if stillThere(t, f.h, f.bookmark, "replaceable_segments", "live/taken/whole") {
		t.Error("the whole part beside them was not reclaimed")
	}
	event := reclaimEvent(t, f.h)
	if event == nil || !strings.Contains(event.Summary, "reported 1 segment object the replaced manifest named as gone") {
		t.Fatalf("the record does not say only the whole part went: %+v", event)
	}
	// And it says what it left alone, because leaving a shape is an outcome
	// of this operation rather than an absence (db:756 finding 4).
	for _, want := range []string{"1 ranged, 1 nested", "never removes", "left alone"} {
		if !strings.Contains(event.Summary, want) {
			t.Errorf("the record does not say %q: %q", want, event.Summary)
		}
	}
}

// Rule 3's bound: a predecessor whose fragments were never inventoried
// removes nothing, because what is held is not known to be all of it and a
// partial removal of a manifest's parts is the worst of both answers.
func TestReclamationDeclinesAnUninventoriedPredecessor(t *testing.T) {
	f := newOverwriteFixture(t)
	parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	// The manifest read comes back as a valid-looking prefix of the list: a
	// lenient reader would take it for the whole thing. It is armed inside
	// the dialog, so the read it spoils is the RECORD's, taken a moment
	// before the publication, rather than the count the question stated.
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		f.h.mock.SetManifestReadFault("replaceable", "live/taken", "partial")
	}})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
		t.Fatalf("an uninventoried predecessor lost %d of %d segments", len(parts)-left, len(parts))
	}
	event := reclaimEvent(t, f.h)
	if event == nil || !strings.Contains(event.Summary, "never inventoried") {
		record, ok, readErr := f.h.transfer.jobs.ReadPredecessor(t.Context(), id, "live/taken")
		t.Fatalf("the record does not say the inventory was missing: %+v; predecessor ok=%v err=%v %+v",
			event, ok, readErr, record)
	}
	if event.Result != audit.ResultObserved {
		t.Errorf("a declined reclamation claims a write outcome: %q", event.Result)
	}
}

// Decision 2 with decision 5: the question states what the answer would
// remove, so an operator who says yes has been told the consequence. A
// plain object has no segments and the sentence says that instead.
func TestOverwriteQuestionStatesTheFragmentConsequence(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		reclaim bool
		large   bool
		want    []string
	}{
		{name: "large and reclaiming", reclaim: true, large: true,
			want: []string{"2 segment objects", "will be REMOVED once the replacement is verified"}},
		{name: "large and retaining", reclaim: false, large: true,
			want: []string{"2 segment objects", "will be KEPT, because reclaiming is turned off"}},
		{name: "plain object", reclaim: true,
			want: []string{"no large object is being replaced"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			f := newOverwriteFixture(t)
			var parts []jobs.ItemFragment
			if fixture.large {
				parts, _ = seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
				f.tag = f.liveTag(t)
			}
			wireReclamation(t, f.h, fixture.reclaim)
			// No is enough: the question's own text is what this case reads.
			confirmer := &recordingOverwriteConfirmer{}
			f.h.transfer.setOverwriteConfirmer(confirmer)
			if _, err := f.h.transfer.StartUpload(t.Context(), f.request(true)); err == nil {
				t.Fatal("a declined confirmation started the upload")
			}
			asked := confirmer.questions()
			if len(asked) != 1 {
				t.Fatalf("the operator was asked %d times", len(asked))
			}
			for _, want := range fixture.want {
				if !strings.Contains(asked[0].Fragments, want) {
					t.Errorf("the question does not say %q: %q", want, asked[0].Fragments)
				}
			}
			if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
				t.Errorf("a refused answer removed %d of %d segments", len(parts)-left, len(parts))
			}
			if event := reclaimEvent(t, f.h); event != nil {
				t.Errorf("a refused answer recorded a reclamation: %+v", event)
			}
		})
	}
}

// Rule 7: a dynamic manifest's inventory is bounded by what one record
// holds, so reclaiming what it names leaves the rest where they are. The
// trail says that rather than reporting a complete removal, and the prefix
// the record kept is how the remainder can still be found.
func TestReclamationSaysWhatABoundedInventoryLeftBehind(t *testing.T) {
	f := newOverwriteFixture(t)
	f.h.mock.AddContainer("replaceable_segments", "standard")
	total := local.MaxListedFragments + 3
	for i := range total {
		seedObject(f.h.mock, "replaceable_segments", fmt.Sprintf("live/taken/%06d", i), "x", nil)
	}
	f.h.mock.PutObject("replaceable", "live/taken", swifttest.Object{
		Manifest: "replaceable_segments/live/taken/"})
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	// The first of what the record named went; the ones past the cap stayed.
	if stillThere(t, f.h, f.bookmark, "replaceable_segments", "live/taken/000000") {
		t.Error("a recorded fragment was not reclaimed")
	}
	if !stillThere(t, f.h, f.bookmark, "replaceable_segments", fmt.Sprintf("live/taken/%06d", total-1)) {
		t.Error("a fragment the record never named was removed anyway")
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("a bounded reclamation left no record")
	}
	if !strings.Contains(event.Summary, "left where they are") ||
		!strings.Contains(event.Summary, fmt.Sprintf("named %d segments", total)) {
		t.Errorf("the record does not say what was left behind: %q", event.Summary)
	}
	// And it is not reported as a complete removal.
	if event.Result == audit.ResultApplied {
		t.Errorf("a partial removal is recorded as applied: %q", event.Result)
	}
}

// The question and the deletion read the same option at different moments.
// Where the question was computed from a reading that worked and the reading
// at reclamation time fails, nothing is removed: the operator answered a
// sentence, and removing objects after being told the answer would keep them
// is what that sentence exists to prevent.
func TestReclamationDeclinesWhenTheSettingCannotBeRead(t *testing.T) {
	f := newOverwriteFixture(t)
	parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
	f.tag = f.liveTag(t)
	settings := wireReclamation(t, f.h, true)
	// The question is asked with the option ON, so it states that the
	// segments WILL be removed and the answer authorises exactly that. Then
	// the store stops answering: an unreadable setting is not permission,
	// even where the answer would have allowed the removal.
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		err := f.h.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, execErr := tx.ExecContext(context.Background(),
				`ALTER TABLE app_settings RENAME TO app_settings_unreachable`)
			return execErr
		})
		if err != nil {
			t.Errorf("hiding the settings table: %v", err)
		}
	}})
	if _, err := settings.Get(t.Context()); err != nil {
		t.Fatalf("the setting was not readable before the run: %v", err)
	}

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	if _, err := settings.Get(t.Context()); err == nil {
		t.Fatal("the fixture did not make the setting unreadable")
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
		t.Fatalf("an unreadable setting removed %d of %d segments", len(parts)-left, len(parts))
	}
	event := reclaimEvent(t, f.h)
	if event == nil || !strings.Contains(event.Summary, "setting could not be read") {
		t.Fatalf("the record does not say the setting could not be read: %+v", event)
	}
	if event.Result != audit.ResultObserved {
		t.Errorf("a declined reclamation claims a write outcome: %q", event.Result)
	}
}

// nativeReplacementFixture is one acknowledged native replacement of a static
// large object, ready to start. The name carries a fullwidth solidus, which
// the engine cannot be trusted with, so the preflight routes it natively and
// this package owns the publication; the predecessor is a manifest with two
// whole segments and one inline entry (db:756's probe fixture).
func nativeReplacementFixture(t *testing.T, reclaim bool) (*harness, UploadRequestDTO, []jobs.ItemFragment, *SettingsService) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("replaceable", "standard")
	base := "taken／"
	parts, _ := seedStaticLargeObject(t, h, "replaceable", "live/"+base)
	path := filepath.Join(t.TempDir(), base)
	if err := os.WriteFile(path, []byte("the bytes this job would send"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(fakePicker{files: []string{path}})
	bookmark := h.transfer.connections.ActiveBookmarkID()
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{
		BookmarkID: bookmark, Container: "replaceable", Prefix: "live/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	req := UploadRequestDTO{BookmarkID: bookmark, Container: "replaceable", Prefix: "live/",
		SelectionToken: picked.Token, AcknowledgeOverwrite: true}
	preflight, err := h.transfer.PreflightUpload(t.Context(), req)
	if err != nil || preflight == nil || len(preflight.Items) != 1 ||
		preflight.Items[0].Route != "native" || !preflight.Items[0].Replaces {
		t.Fatalf("the fixture did not admit exactly one native replacement: %+v %v", preflight, err)
	}
	return h, req, parts, wireReclamation(t, h, reclaim)
}

// awaitNativeReplacement runs the job to its end and returns how many of the
// predecessor's segments the store still holds.
func awaitNativeReplacement(t *testing.T, h *harness, req UploadRequestDTO, id string, parts []jobs.ItemFragment) int {
	t.Helper()
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.NativeRouted != 1 ||
		job.Counters.ObjectsVerified != 1 || job.Counters.Overwrites != 1 {
		t.Fatalf("the native replacement did not complete and verify: %+v", job.Counters)
	}
	detail, err := h.inspector.Object(t.Context(), req.BookmarkID, req.Container, "live/taken／")
	if err != nil || strings.Trim(detail.ETag, `"`) != sentTagForFixture {
		t.Fatalf("the replacement is not the bytes this job sent: %+v %v", detail, err)
	}
	return survivors(t, h, req.BookmarkID, "replaceable_segments", partNames(parts))
}

// db:756 finding 1: the answer is a CEILING. A question that promised the
// segments would be kept cannot be turned into a removal by a setting that
// moves after it was answered — the operator is not there to be asked again.
func TestReclamationCannotExpandBeyondTheAnsweredQuestion(t *testing.T) {
	h, req, parts, settings := nativeReplacementFixture(t, false)
	confirmer := &recordingOverwriteConfirmer{yes: true, before: func() {
		current, err := settings.Get(context.Background())
		if err != nil {
			t.Errorf("reading the setting inside the dialog: %v", err)
			return
		}
		current.ReclaimReplacedFragments = true
		if _, err := settings.Set(context.Background(), current); err != nil {
			t.Errorf("moving the setting inside the dialog: %v", err)
		}
	}}
	h.transfer.setOverwriteConfirmer(confirmer)
	id, err := h.transfer.StartUpload(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	asked := confirmer.questions()
	if len(asked) != 1 || !strings.Contains(asked[0].Fragments, "will be KEPT") {
		t.Fatalf("the fixture did not capture a retaining question: %+v", asked)
	}
	if left := awaitNativeReplacement(t, h, req, id, parts); left != len(parts) {
		t.Fatalf("the question promised retention and %d of %d segments went", len(parts)-left, len(parts))
	}
	event := reclaimEvent(t, h)
	if event == nil || !strings.Contains(event.Summary, "did not say these segments would be removed") {
		t.Fatalf("the record does not say the answer is what kept them: %+v", event)
	}
	if event.Result != audit.ResultObserved {
		t.Errorf("a reclamation that wrote nothing claims a write outcome: %q", event.Result)
	}
}

// db:756 finding 2: an inventory this build could not read is not an object
// with no segments. The question has to say which of the two it is, and an
// answer given to a question that could say nothing authorises no removal.
func TestAnUnreadInventoryIsNotNoLargeObject(t *testing.T) {
	h, req, parts, _ := nativeReplacementFixture(t, true)
	// Spoiled while the question is composed, sound again by the time the
	// record is taken: a transient read, not an outside writer.
	h.mock.SetManifestReadFault(req.Container, "live/taken／", "partial")
	confirmer := &recordingOverwriteConfirmer{yes: true, before: func() {
		h.mock.SetManifestReadFault(req.Container, "live/taken／", "")
	}}
	h.transfer.setOverwriteConfirmer(confirmer)
	id, err := h.transfer.StartUpload(t.Context(), req)
	if err != nil {
		if left := survivors(t, h, req.BookmarkID, "replaceable_segments", partNames(parts)); left != len(parts) {
			t.Fatalf("a refused start removed %d of %d segments: %v", len(parts)-left, len(parts), err)
		}
		return
	}
	asked := confirmer.questions()
	if len(asked) != 1 {
		t.Fatalf("the operator was asked %d times", len(asked))
	}
	if strings.Contains(asked[0].Fragments, "no large object is being replaced") {
		t.Errorf("an unread inventory was described as no large object: %q", asked[0].Fragments)
	}
	if !strings.Contains(asked[0].Fragments, "could not read what backs") {
		t.Errorf("the question does not say it could not read the inventory: %q", asked[0].Fragments)
	}
	if left := awaitNativeReplacement(t, h, req, id, parts); left != len(parts) {
		t.Fatalf("a question that announced no removal was followed by %d of %d going", len(parts)-left, len(parts))
	}
}

// db:756 finding 3: one list held both a store's refusal and a name in a
// reply that could not be read, and the summary called all of them "not
// removed". A refusal is knowledge; an unreadable reply is not. The sentence
// and the audit result have to tell them apart, including in one mixed
// outcome, so this reads the sentence builder directly with each shape.
func TestReclaimReportingSeparatesRefusalsFromUnknowns(t *testing.T) {
	one := local.Fragment{Container: "seg", Name: "a", Shape: local.FragmentWhole}
	two := local.Fragment{Container: "seg", Name: "b", Shape: local.FragmentWhole}
	for _, fixture := range []struct {
		name    string
		outcome local.ReclaimOutcome
		want    []string
		absent  []string
		result  audit.Result
	}{
		{name: "all gone",
			outcome: local.ReclaimOutcome{Removed: []local.Fragment{one, two}},
			want:    []string{"reported 2 segment objects"},
			absent:  []string{"refused", "unknown"},
			result:  audit.ResultApplied},
		{name: "one refused",
			outcome: local.ReclaimOutcome{Removed: []local.Fragment{one},
				Failed: []local.ReclaimFailure{{Fragment: two, Detail: "500 Internal Server Error", Resolved: true}}},
			want:   []string{"reported 1 segment object", "the store refused 1 and kept it", "seg/b (500 Internal Server Error)"},
			absent: []string{"is unknown"},
			result: audit.ResultVerifyMismatch},
		{name: "one unknown",
			outcome: local.ReclaimOutcome{
				Failed:    []local.ReclaimFailure{{Fragment: one, Detail: "the reply could not be trusted"}},
				Untrusted: "a row names something this request did not send"},
			want:   []string{"what the store did with 1 further name is unknown", "seg/a", "could not be read as a disposition"},
			absent: []string{"the store refused"},
			result: audit.ResultUnverified},
		{name: "mixed",
			outcome: local.ReclaimOutcome{Removed: []local.Fragment{one},
				Failed: []local.ReclaimFailure{
					{Fragment: two, Detail: "409 Conflict", Resolved: true},
					{Fragment: local.Fragment{Container: "other", Name: "c"}, Detail: "unreadable"}},
				Untrusted: "the store's answer could not be read"},
			want: []string{"reported 1 segment object", "the store refused 1 and kept it", "seg/b (409 Conflict)",
				"what the store did with 1 further name is unknown", "other/c"},
			// An unknown effect anywhere in the request makes the whole
			// event unverified rather than a partial write.
			result: audit.ResultUnverified},
		{name: "retained shapes",
			outcome: local.ReclaimOutcome{Removed: []local.Fragment{one},
				Retained: "1 ranged of the entries it named are shapes this operation never removes, so they were left alone"},
			want:   []string{"reported 1 segment object", "1 ranged"},
			result: audit.ResultApplied},
		{name: "declined states what it kept",
			outcome: local.ReclaimOutcome{Declined: "reclaiming replaced fragments is turned off",
				Retained: "1 nested of the entries it named are shapes this operation never removes, so they were left alone"},
			want:   []string{"removed none", "turned off", "1 nested"},
			result: audit.ResultObserved},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			summary := reclaimSummary(fixture.outcome)
			for _, want := range fixture.want {
				if !strings.Contains(summary, want) {
					t.Errorf("the summary does not say %q: %q", want, summary)
				}
			}
			for _, absent := range fixture.absent {
				if strings.Contains(summary, absent) {
					t.Errorf("the summary claims %q: %q", absent, summary)
				}
			}
			if got := reclaimResult(fixture.outcome); got != fixture.result {
				t.Errorf("result = %q, want %q for %q", got, fixture.result, summary)
			}
		})
	}
}

// db:756 finding 4: an inventory whose every entry is a shape this operation
// never removes is not a removal of none. No request is sent, the record says
// which shapes it left, and the event is an observation rather than a write.
func TestReclamationDeclinesWhenNothingRecordedIsAWholeObject(t *testing.T) {
	f := newOverwriteFixture(t)
	f.h.mock.AddContainer("replaceable_segments", "standard")
	seedObject(f.h.mock, "replaceable_segments", "live/taken/container", "89abcdef", nil)
	seedObject(f.h.mock, "replaceable_segments", "live/taken/nested", "0123456789abcdef", nil)
	// A ranged entry, a nested manifest and inline data: three entries, no
	// private whole object among them.
	f.h.mock.PutObject("replaceable", "live/taken", swifttest.Object{
		SLO: true, Bytes: 16, ETag: `"` + md5of(md5of("01234567")+md5of("89abcdef")) + `"`,
		Data: []byte("0123456789abcdef"),
		SLOManifestJSON: `[` +
			`{"name":"/replaceable_segments/live/taken/container","hash":"` + md5of("89abcdef") + `","bytes":8,"range":"0-3"},` +
			`{"data":"aGVsbG8="},` +
			`{"name":"/replaceable_segments/live/taken/nested","hash":"` + md5of("0123456789abcdef") + `","bytes":16,"sub_slo":true}]`,
	})
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	for _, name := range []string{"live/taken/container", "live/taken/nested"} {
		if !stillThere(t, f.h, f.bookmark, "replaceable_segments", name) {
			t.Errorf("%s was removed although no entry naming it is this operation's to remove", name)
		}
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("an all-excluded inventory left no record")
	}
	if event.Result != audit.ResultApplied {
		// Stated as the expectation rather than inverted, so the message
		// names what it wanted.
		if event.Result != audit.ResultObserved {
			t.Errorf("result = %q, want observed", event.Result)
		}
	} else {
		t.Errorf("a reclamation that wrote nothing is recorded as applied: %q", event.Summary)
	}
	// The question itself could authorise nothing, so the record quotes that
	// sentence, and that sentence states the shapes it will not touch.
	for _, want := range []string{"removed none", "did not say these segments would be removed",
		"nothing that backs what is being replaced can be removed", "never removes"} {
		if !strings.Contains(event.Summary, want) {
			t.Errorf("the record does not say %q: %q", want, event.Summary)
		}
	}
}

// The other half of finding 4: the question counted removable segments, and
// by the time the record was taken the live object named only shapes this
// operation never removes. The answer authorised a removal, so the ceiling
// does not stop it; the record does, and it says so rather than reporting a
// removal of none as applied.
func TestReclamationDeclinesWhenTheRecordNamesNoWholeObject(t *testing.T) {
	f := newOverwriteFixture(t)
	parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	// Inside the dialog the live object's ENTRIES become one ranged entry over
	// a segment that stays where it is, while its class, length and entity tag
	// stay exactly what the question was composed from. So the question
	// authorises removal, the record taken before publishing names no whole
	// object, and reclamation has to decline — which is this case's subject.
	//
	// The identity is deliberately left unchanged. ADR 0085's comparison now
	// refuses a replacement whose name no longer holds what was acknowledged,
	// and a fixture that changed the identity here would be refused before
	// reclamation was reached, testing that decision instead of this one. The
	// refusal has its own case,
	// TestAcknowledgedUploadRefusesAWriterThatArrivedDuringTheDialog.
	confirmer := &recordingOverwriteConfirmer{yes: true, before: func() {
		f.h.mock.PutObject("replaceable", "live/taken", swifttest.Object{
			SLO: true, Bytes: 16, ETag: `"` + f.tag + `"`,
			Data: []byte("0123456789abcdef"),
			SLOManifestJSON: `[{"name":"/replaceable_segments/` + parts[0].Object +
				`","hash":"` + md5of("01234567") + `","bytes":8,"range":"0-3"}]`,
		})
	}}
	f.h.transfer.setOverwriteConfirmer(confirmer)

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	if asked := confirmer.questions(); len(asked) != 1 || !strings.Contains(asked[0].Fragments, "will be REMOVED") {
		t.Fatalf("the fixture did not capture a removing question: %+v", asked)
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
		t.Fatalf("a record naming no whole object still lost %d of %d segments", len(parts)-left, len(parts))
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("the run left no reclamation record")
	}
	if !strings.Contains(event.Summary, "nothing the predecessor's record named is a private whole object") {
		t.Errorf("the record does not say why it removed nothing: %q", event.Summary)
	}
	if !strings.Contains(event.Summary, "1 ranged") || !strings.Contains(event.Summary, "left alone") {
		t.Errorf("the record does not name the shape it left: %q", event.Summary)
	}
	if event.Result != audit.ResultObserved {
		t.Errorf("a reclamation that wrote nothing is recorded as %q", event.Result)
	}
}

// Finding 3 at the public boundary: the store's reply names a path this
// request never sent, so it cannot be read as a disposition for anything in
// it. The objects may well be gone — this fixture's store did remove them —
// and the record must say the effect is unknown rather than claiming either.
func TestReclamationRecordsAnUnreadableReplyAsUnknown(t *testing.T) {
	f := newOverwriteFixture(t)
	parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
	f.tag = f.liveTag(t)
	f.h.mock.BulkDeleteExtraErrorRow = "/replaceable_segments/somebody/elses"
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("an unreadable reply failed the replacement: %s %+v", job.State, job.Counters)
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("the run left no reclamation record")
	}
	if event.Result != audit.ResultUnverified {
		t.Errorf("an unreadable reply is recorded as %q, not unverified", event.Result)
	}
	for _, want := range []string{
		fmt.Sprintf("what the store did with %d further names is unknown", len(parts)),
		"could not be read as a disposition", "somebody/elses"} {
		if !strings.Contains(event.Summary, want) {
			t.Errorf("the record does not say %q: %q", want, event.Summary)
		}
	}
	// And it claims neither outcome for those names.
	for _, forbidden := range []string{"the store refused", "as gone or already absent; the store refused"} {
		if strings.Contains(event.Summary, forbidden) {
			t.Errorf("the record claims %q about names nobody settled: %q", forbidden, event.Summary)
		}
	}
	if !strings.Contains(event.Summary, "reported 0 segment objects") {
		t.Errorf("the record credits a removal from a reply it could not read: %q", event.Summary)
	}
}

// The restricting direction, which the answered ceiling allows and which the
// two cases above no longer reach now that a question stating retention
// declines before these guards run: the question authorised the removal, and
// what is true at reclamation time is what stops it.
func TestReclamationObeysWhatIsTrueWhenItRuns(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		change func(*overwriteFixture, *SettingsService)
		reason string
	}{
		{name: "the container starts keeping versions",
			change: func(f *overwriteFixture, _ *SettingsService) {
				f.h.mock.AddContainer("replaceable_archive", "standard")
				f.h.mock.EnableVersionArchive("replaceable", "replaceable_archive", false)
			},
			reason: "the container keeps versions"},
		{name: "the option is turned off",
			change: func(f *overwriteFixture, settings *SettingsService) {
				current, err := settings.Get(context.Background())
				if err != nil {
					t.Errorf("reading the setting inside the dialog: %v", err)
					return
				}
				current.ReclaimReplacedFragments = false
				if _, err := settings.Set(context.Background(), current); err != nil {
					t.Errorf("moving the setting inside the dialog: %v", err)
				}
			},
			reason: "reclaiming replaced fragments is turned off"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			f := newOverwriteFixture(t)
			parts, _ := seedStaticLargeObject(t, f.h, "replaceable", "live/taken")
			f.tag = f.liveTag(t)
			settings := wireReclamation(t, f.h, true)
			// The question is composed first, with nothing yet in the way, so
			// it states that the segments WILL be removed and the answer
			// authorises exactly that. The change lands inside the dialog.
			confirmer := &recordingOverwriteConfirmer{yes: true}
			confirmer.before = func() { fixture.change(f, settings) }
			f.h.transfer.setOverwriteConfirmer(confirmer)

			id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
			if err != nil {
				t.Fatal(err)
			}
			if job := awaitTerminal(t, f.h, id); job.State != "complete" {
				t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
			}
			asked := confirmer.questions()
			if len(asked) != 1 || !strings.Contains(asked[0].Fragments, "will be REMOVED") {
				t.Fatalf("the fixture did not capture a removing question: %+v", asked)
			}
			if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
				t.Fatalf("%d of %d segments went although %s at the time it ran", len(parts)-left, len(parts), fixture.reason)
			}
			event := reclaimEvent(t, f.h)
			if event == nil || !strings.Contains(event.Summary, fixture.reason) {
				t.Fatalf("the record does not say %q stopped it: %+v", fixture.reason, event)
			}
			if event.Result != audit.ResultObserved {
				t.Errorf("a reclamation that wrote nothing is recorded as %q", event.Result)
			}
		})
	}
}
