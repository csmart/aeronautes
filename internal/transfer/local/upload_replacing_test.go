package local

import (
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// ADR 0081 decision 1: an overwrite happens only when the caller asked for
// one, and asking means naming a different entry point rather than passing
// a value. The two reports are taken over the same selection against the
// same destination, so the only difference between them is which function
// was called.
func TestPreflightUploadReplacingAdmitsALiveNameOnlyWhenNamed(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "live/taken", "the bytes already there")
	seed(mock, "outside", "keep", "outside sentinel")
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	dir := t.TempDir()
	taken := writeFile(t, dir, "taken", "the bytes this job would send")
	free := writeFile(t, dir, "free", "nothing is at this name")
	paths := []string{taken, free}

	refusing, err := PreflightUpload(t.Context(), s, "media", "live/", paths)
	if err != nil {
		t.Fatal(err)
	}
	if !refusing.Rejected() || len(refusing.Items) != 1 || refusing.Items[0].Name != "live/free" ||
		refusing.Items[0].Replaces {
		t.Fatalf("the ordinary preflight admitted a live name: rejected=%v items=%+v", refusing.Rejected(), refusing.Items)
	}
	if len(refusing.Problems) != 1 || refusing.Problems[0].Kind != "exists" || !refusing.Problems[0].Reject {
		t.Fatalf("the ordinary preflight did not refuse the collision: %+v", refusing.Problems)
	}

	replacing, err := PreflightUploadReplacing(t.Context(), s, "media", "live/", paths)
	if err != nil {
		t.Fatal(err)
	}
	if replacing.Rejected() {
		t.Fatalf("the replacing preflight still refuses: %+v", replacing.Problems)
	}
	// The collision is admitted AND still reported: it is the thing the
	// operator acknowledged, not a reason to stop and not a silence.
	if len(replacing.Problems) != 1 || replacing.Problems[0].Kind != "exists" || replacing.Problems[0].Reject ||
		replacing.Problems[0].Name != "live/taken" {
		t.Fatalf("the replacing preflight lost or refused the collision report: %+v", replacing.Problems)
	}
	byName := map[string]UploadItem{}
	for _, item := range replacing.Items {
		byName[item.Name] = item
	}
	if len(byName) != 2 {
		t.Fatalf("the replacing preflight planned %d items: %+v", len(byName), replacing.Items)
	}
	if !byName["live/taken"].Replaces {
		t.Errorf("the live name is not marked as replacing: %+v", byName["live/taken"])
	}
	if byName["live/free"].Replaces {
		t.Errorf("a name that was free is marked as replacing: %+v", byName["live/free"])
	}
	if replacing.TotalBytes != byName["live/taken"].Size+byName["live/free"].Size {
		t.Errorf("the replacing preflight's byte total omits the replaced name: %+v", replacing)
	}

	// Neither report writes anything: a preflight is a read.
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Fatalf("a preflight changed the account: %s", diff)
	}
}

// ADR 0081 decision 6: a replacement drops the conditional that makes a
// create race-free, because the name existing is the point, and keeps the
// declared entity tag, so bytes that did not survive the wire are refused
// by the store with the prior object intact. That is the whole of what an
// acknowledged overwrite changes about the write.
func TestNativeReplacementKeepsItsContentBinding(t *testing.T) {
	const prior = "the bytes already there"
	for _, tc := range []struct {
		name    string
		altered bool
	}{
		{"refuses an altered replacement and leaves the prior object", true},
		{"publishes an unaltered replacement", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			seed(mock, "media", "docs/object", prior)
			// The local package's proxy always alters, so the unaltered
			// control speaks to the store directly.
			var server *httptest.Server
			if tc.altered {
				server = alteringProxy(t, mock, "/v1/AUTH_test/media/docs/object")
			} else {
				server = httptest.NewServer(mock)
				t.Cleanup(server.Close)
			}
			session := bindingSession(t, server)
			path := writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
			// The replacing entry point is what admits a live name, and
			// what marks the item, since this test is the route rather
			// than the service.
			preflight, err := PreflightUploadReplacing(t.Context(), session, "media", "docs/", []string{path})
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 {
				t.Fatalf("the replacing preflight did not admit the live name: %+v %v", preflight, err)
			}
			item := preflight.Items[0]
			item.Route = "native"
			if !item.Replaces {
				t.Fatal("the replacing preflight did not mark the item")
			}

			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			result := nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))

			live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatalf("the name is gone after a replacement attempt: %v", err)
			}
			if tc.altered {
				if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved ||
					result.Diagnostic.Stage != transfercopy.StageCommit || result.Diagnostic.Code != transfercopy.CodeUnverified {
					t.Fatalf("an altered replacement was not refused as an unverified commit: %+v", result.Observation)
				}
				if !strings.EqualFold(strings.Trim(live.ETag, `"`), md5hex(prior)) {
					t.Fatalf("the prior object did not survive a refused replacement: %+v", live)
				}
				return
			}
			if result.Result != transfercopy.ResultCopied {
				t.Fatalf("an unaltered replacement was refused: %+v", result.Observation)
			}
			if !strings.EqualFold(strings.Trim(live.ETag, `"`), md5hex("the bytes this job planned to send")) {
				t.Fatalf("the replacement did not take: %+v", live)
			}
		})
	}
}

// The other half of decision 6, kept honest: an item the admission did NOT
// mark still carries the conditional, so a create onto a name that became
// live is a conflict rather than a silent replacement.
func TestNativeCreateStillRefusesALiveName(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "the bytes already there")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	path := writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
	// An item as the ordinary preflight would never produce it for a live
	// name: unmarked, so the publication keeps its conditional.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	item := UploadItem{Path: path, Name: "docs/object", Size: info.Size(), Route: "native"}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))
	if result.Result != transfercopy.ResultConflict {
		t.Fatalf("a create onto a live name was not a conflict: %+v", result.Observation)
	}
	live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil || !strings.EqualFold(strings.Trim(live.ETag, `"`), md5hex("the bytes already there")) {
		t.Fatalf("a refused create changed the live object: %+v %v", live, err)
	}
}

// A replacement is counted where its publication is witnessed, not where
// it was planned: a marked item whose publication failed has replaced
// nothing, and a job reporting it as a replacement would be claiming an
// effect that did not happen.
func TestReplacementsAreCountedOnlyWhenWitnessed(t *testing.T) {
	marked := UploadItem{Path: "/local/object", Name: "docs/object", Size: 5, Route: "native", Replaces: true}
	free := UploadItem{Path: "/local/other", Name: "docs/other", Size: 5, Route: "native"}
	copied := uploadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5}}
	for _, tc := range []struct {
		name     string
		item     UploadItem
		result   uploadResult
		replaced int64
		done     int64
	}{
		{"a marked item that published", marked, copied, 1, 1},
		{"a marked item whose publication failed", marked,
			uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnverified, "native-put",
				errors.New("the store refused it")), 0, 0},
		{"a marked item refused as a conflict", marked,
			uploadConflict("native-put", errors.New("the name appeared")), 0, 0},
		{"an unmarked item that published", free, copied, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &uploadRun{report: &Report{}}
			if err := run.observe(tc.item, tc.result); err != nil {
				t.Fatalf("observing: %v", err)
			}
			if run.report.Replaced != tc.replaced || run.report.Done != tc.done {
				t.Fatalf("replaced=%d done=%d, wanted %d and %d",
					run.report.Replaced, run.report.Done, tc.replaced, tc.done)
			}
		})
	}
}

// Publishing over an object without saying what was there is the outcome
// decision 7 exists to prevent, so a replacing item with nowhere to record
// it is refused rather than published unrecorded. A recorder that never
// replaces needs no such hook, which is why it is optional rather than
// required of every caller.
func TestReplacingRefusesWithNowhereToRecord(t *testing.T) {
	marked := UploadItem{Path: "/local/object", Name: "docs/object", Size: 5, Route: "native", Replaces: true}
	free := UploadItem{Path: "/local/other", Name: "docs/other", Size: 5, Route: "native"}
	observed := ObservedPredecessor{ETag: md5hex("gone"), Length: 4, Class: "plain"}

	for _, tc := range []struct {
		name     string
		item     UploadItem
		recorder *UploadRecorder
		refuses  bool
	}{
		{"a replacing item with no recorder at all", marked, nil, true},
		{"a replacing item with a recorder that cannot take it", marked,
			&UploadRecorder{Admit: func(UploadAdmission) error { return nil }}, true},
		{"a replacing item with somewhere to record", marked,
			&UploadRecorder{Replacing: func(UploadItem, ObservedPredecessor) error { return nil }}, false},
		{"an item that replaces nothing, with no recorder", free, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &uploadRun{report: &Report{}, recorder: tc.recorder}
			err := run.replacing(tc.item, observed)
			if tc.refuses {
				if err == nil || !strings.Contains(err.Error(), "nowhere to record") {
					t.Fatalf("a replacement with nowhere to record was admitted: %v", err)
				}
				if run.stopped == nil {
					t.Fatal("the run was not stopped")
				}
				return
			}
			if err != nil || run.stopped != nil {
				t.Fatalf("a recordable replacement was refused: %v %v", err, run.stopped)
			}
		})
	}
}

// ADR 0081 decision 5, and the reason it retains rather than reclaims: an
// outside manifest can reference the very segments a replaced predecessor
// named, because the prefix is published in the manifest's own header. Both
// outside objects must still assemble their own bytes after the
// replacement, and every segment must still be there.
func TestReplacingALargeObjectPreservesOutsideReferences(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "subject/run/0001", "01234567")
	seed(mock, "media_segments", "subject/run/0002", "89abcdef")
	// The predecessor: a dynamic manifest over that prefix.
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/subject/run/"})
	// Two outside objects nobody in this job selected, over exactly the
	// segments the predecessor named. A reader of the predecessor could
	// have made either, because the prefix is published in the
	// predecessor's own header; an alias over its own private copies would
	// survive the deletion of every one of the predecessor's parts and
	// prove nothing.
	mock.PutObject("media", "outside/dlo", swifttest.Object{Manifest: "media_segments/subject/run/"})
	aliasStatic(t, mock, "media", "outside/slo",
		segmentRef{"media_segments", "subject/run/0001", "01234567"},
		segmentRef{"media_segments", "subject/run/0002", "89abcdef"})

	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	path := writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
	preflight, err := PreflightUploadReplacing(t.Context(), s, "media", "docs/", []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || !preflight.Items[0].Replaces {
		t.Fatalf("the replacing preflight did not admit the live manifest: %+v %v", preflight, err)
	}
	item := preflight.Items[0]
	item.Route = "native"

	var recorded []ObservedPredecessor
	run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{
		Replacing: func(_ UploadItem, observed ObservedPredecessor) error {
			recorded = append(recorded, observed)
			return nil
		}}}
	present, err := run.observePredecessor(t.Context(), s, "media", item)
	if err != nil {
		t.Fatalf("observing the predecessor: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if result := nativeUpload(t.Context(), s, "media", item, file, present, comparesAgainstLive(t, s, "media", item)); result.Result != transfercopy.ResultCopied {
		t.Fatalf("the replacement was refused: %+v", result.Observation)
	}

	// The inventory was taken, and it names the prefix rather than
	// claiming to be the whole of it.
	if len(recorded) != 1 || len(recorded[0].Fragments.Names) != 2 ||
		recorded[0].Fragments.Prefix != "media_segments/subject/run/" || recorded[0].Fragments.Complete {
		t.Fatalf("the fragment inventory is wrong: %+v", recorded)
	}

	// Nothing was removed. Both outside objects still assemble their own
	// bytes, which is the property that makes retention the safe answer.
	// Reported rather than fatal, so one deletion shows every promise it
	// broke instead of only the first.
	for _, name := range []string{"outside/dlo", "outside/slo"} {
		body, _, err := s.OpenObject(t.Context(), "media", name)
		if err != nil {
			t.Errorf("an outside object stopped being readable: %s: %v", name, err)
			continue
		}
		bytes, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil || string(bytes) != "0123456789abcdef" {
			t.Errorf("an outside object lost its backing data: %s: %q %v", name, bytes, readErr)
		}
	}
	// And every leaf the predecessor named is still its own object. This
	// mock assembles a static manifest's bytes when the manifest is written
	// and serves them from that copy, so on this fixture the byte check
	// above cannot see a deleted segment behind the static alias; the
	// leaves are what can, and on real Swift the bytes are too.
	if len(recorded[0].Fragments.Names) != 2 {
		t.Fatalf("the inventory did not name the segments to check: %+v", recorded[0].Fragments)
	}
	for _, fragment := range recorded[0].Fragments.Names {
		leaf, _, err := s.OpenObject(t.Context(), fragment.Container, fragment.Name)
		if err != nil {
			t.Errorf("a named segment stopped being an object: %s/%s: %v", fragment.Container, fragment.Name, err)
			continue
		}
		_ = leaf.Close()
	}
	after := uploadSnapshot(t, witness)
	// The replaced name is the only thing that changed.
	delete(after.Containers["media"].Objects, "docs/object")
	delete(before.Containers["media"].Objects, "docs/object")
	if diff := oracle.Diff(before, after); !diff.Empty() {
		t.Fatalf("replacing a manifest disturbed something else: %s", diff)
	}
}

// ADR 0081 decision 5: a static manifest's entries are exact, and the
// record says which of them name something other than a whole remote
// object. A top-level list is not a backing-object graph.
func TestReplacingAStaticManifestInventoriesItsShapes(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	seed(mock, "media_segments", "parts/0002", "89abcdef")
	// A manifest naming a whole object, an inline entry, a ranged entry and
	// a nested manifest: four entries, only one of which is a whole remote
	// part this inventory can name.
	mock.PutObject("media", "docs/object", swifttest.Object{
		SLO: true, Bytes: 16, ETag: `"` + md5hex(md5hex("01234567")+md5hex("89abcdef")) + `"`,
		Data: []byte("0123456789abcdef"),
		SLOManifestJSON: `[` +
			`{"name":"/media_segments/parts/0001","hash":"` + md5hex("01234567") + `","bytes":8},` +
			`{"data":"aGVsbG8="},` +
			`{"name":"/media_segments/parts/0002","hash":"` + md5hex("89abcdef") + `","bytes":8,"range":"0-3"},` +
			`{"name":"/media_segments/parts/0002","hash":"` + md5hex("89abcdef") + `","bytes":8,"sub_slo":true}]`,
	})
	detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil {
		t.Fatal(err)
	}
	inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
	if inventory.Unread != "" {
		t.Fatalf("the static manifest was not inventoried: %q", inventory.Unread)
	}
	if !inventory.Complete {
		t.Error("a static manifest's own list is exact and should say so")
	}
	if inventory.Prefix != "" {
		t.Errorf("a static manifest has no prefix to list: %q", inventory.Prefix)
	}
	// Every entry in its own position, with what it referenced, because
	// after the replacement this record is the only thing that can say
	// which name was a containing object and which was a manifest.
	want := []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole},
		{Shape: FragmentInline},
		{Container: "media_segments", Name: "parts/0002", Shape: FragmentRanged},
		{Container: "media_segments", Name: "parts/0002", Shape: FragmentNested},
	}
	if !slices.Equal(inventory.Names, want) {
		t.Fatalf("the inventory does not record the entries it read: %+v, want %+v", inventory.Names, want)
	}
	if inventory.Counted != 4 || inventory.Inline != 1 || inventory.Ranged != 1 || inventory.Nested != 1 {
		t.Fatalf("the inventory's totals do not match its entries: %+v", inventory)
	}
}

// Finding F4 of db:746: two manifests that name the same objects and produce
// the same totals, differing only in WHICH entry was ranged, which was
// nested, and where the inline entry sat. A record of totals cannot tell
// them apart, and after the replacement nothing else can either.
func TestReplacingAStaticManifestRecordsWhichEntryWasWhich(t *testing.T) {
	inventories := map[string]FragmentInventory{}
	for _, tc := range []struct {
		name     string
		manifest string
		want     []Fragment
	}{
		{name: "a ranged first and the inline second", want: []Fragment{
			{Container: "media_segments", Name: "parts/a", Shape: FragmentRanged},
			{Shape: FragmentInline},
			{Container: "media_segments", Name: "parts/b", Shape: FragmentNested},
		}, manifest: `[` +
			`{"name":"/media_segments/parts/a","hash":"HA","bytes":8,"range":"0-3"},` +
			`{"data":"aGVsbG8="},` +
			`{"name":"/media_segments/parts/b","hash":"HB","bytes":8,"sub_slo":true}]`},
		{name: "a nested first and the inline last", want: []Fragment{
			{Container: "media_segments", Name: "parts/a", Shape: FragmentNested},
			{Container: "media_segments", Name: "parts/b", Shape: FragmentRanged},
			{Shape: FragmentInline},
		}, manifest: `[` +
			`{"name":"/media_segments/parts/a","hash":"HA","bytes":8,"sub_slo":true},` +
			`{"name":"/media_segments/parts/b","hash":"HB","bytes":8,"range":"0-3"},` +
			`{"data":"aGVsbG8="}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			mock.AddContainer("media_segments", "standard")
			seed(mock, "media_segments", "parts/a", "01234567")
			seed(mock, "media_segments", "parts/b", "89abcdef")
			manifest := strings.ReplaceAll(tc.manifest, "HA", md5hex("01234567"))
			manifest = strings.ReplaceAll(manifest, "HB", md5hex("89abcdef"))
			mock.PutObject("media", "docs/object", swifttest.Object{
				SLO: true, Bytes: 16, ETag: `"` + md5hex(md5hex("01234567")+md5hex("89abcdef")) + `"`,
				Data: []byte("0123456789abcdef"), SLOManifestJSON: manifest,
			})
			detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatal(err)
			}
			inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
			if inventory.Unread != "" {
				t.Fatalf("the manifest was not inventoried: %q", inventory.Unread)
			}
			if !slices.Equal(inventory.Names, tc.want) {
				t.Fatalf("the record does not say which entry was which: %+v, want %+v", inventory.Names, tc.want)
			}
			inventories[tc.name] = inventory
		})
	}
	// The pair is the point: same names, same totals, different records.
	if len(inventories) != 2 {
		t.Fatal("both manifests are needed to make the comparison")
	}
	var each []FragmentInventory
	for _, inventory := range inventories {
		each = append(each, inventory)
	}
	first, second := each[0], each[1]
	if first.Counted != second.Counted || first.Inline != second.Inline ||
		first.Ranged != second.Ranged || first.Nested != second.Nested {
		t.Fatalf("the fixture did not produce the same totals: %+v and %+v", first, second)
	}
	if slices.Equal(first.Names, second.Names) {
		t.Fatalf("two different manifests produced the same record: %+v", first.Names)
	}
}

// Finding F1 of db:746: a static list is recorded whole or not at all. The
// cap that a listed prefix needs must never truncate a list that cannot be
// read again, so a manifest past this build's bound is reported as one it
// could not inventory rather than as a shorter exact list.
func TestReplacingAStaticManifestIsWholeOrUnread(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries int
		unread  bool
	}{
		{name: "at the bound", entries: MaxManifestEntries},
		{name: "one past the bound", entries: MaxManifestEntries + 1, unread: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			mock.AddContainer("media_segments", "standard")
			seed(mock, "media_segments", "part", "01234567")
			digest := md5hex("01234567")
			var manifest strings.Builder
			manifest.WriteString("[")
			for i := range tc.entries {
				if i > 0 {
					manifest.WriteString(",")
				}
				fmt.Fprintf(&manifest, `{"name":"/media_segments/part","hash":%q,"bytes":8}`, digest)
			}
			manifest.WriteString("]")
			mock.PutObject("media", "docs/object", swifttest.Object{
				SLO: true, Bytes: 8, ETag: `"` + md5hex(digest) + `"`,
				Data: []byte("01234567"), SLOManifestJSON: manifest.String(),
			})
			detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatal(err)
			}
			inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
			if tc.unread {
				if inventory.Unread == "" || inventory.Complete || len(inventory.Names) != 0 || inventory.Counted != 0 {
					t.Fatalf("a manifest past the bound was credited as an inventory: %+v", inventory)
				}
				return
			}
			if inventory.Unread != "" {
				t.Fatalf("a manifest at the bound was not read: %q", inventory.Unread)
			}
			// Whole: every entry held, and the count agreeing with them.
			if len(inventory.Names) != tc.entries || inventory.Counted != tc.entries ||
				inventory.Truncated() || !inventory.Complete {
				t.Fatalf("the list was not recorded whole: named=%d counted=%d truncated=%v complete=%v",
					len(inventory.Names), inventory.Counted, inventory.Truncated(), inventory.Complete)
			}
		})
	}
}

// ADR 0081 decision 7's narrower promise: an unchanged predecessor is
// promised only where the replacement was not attempted or is proven
// refused. Where it landed, or where its outcome is unknown, the record
// reports that and claims no rollback.
//
// Driven through the native route rather than asserted about constructed
// values: a store that takes the write and then never answers is the shape
// an unresolved publication actually has. The staged route's half of the
// same promise runs through the public service (TestOverwrite*).
func TestReplacementOutcomesAreNotRollbackClaims(t *testing.T) {
	for _, tc := range []struct {
		name string
		// arm produces the failure and says what the route must report.
		arm    func(t *testing.T, mock *swifttest.Server, file *os.File)
		effect transfercopy.Effect
		stage  transfercopy.Stage
		// landed says the store really holds the new bytes afterwards.
		landed bool
	}{
		{name: "a publication whose answer was lost", effect: transfercopy.EffectUnresolved,
			stage: transfercopy.StageCommit, landed: true,
			arm: func(_ *testing.T, mock *swifttest.Server, _ *os.File) {
				mock.DropNextObjectPUTResponse("media", "docs/object")
			}},
		{name: "a publication that never left", effect: transfercopy.EffectNotAttempted,
			stage: transfercopy.StageSourceRead, landed: false,
			arm: func(t *testing.T, _ *swifttest.Server, file *os.File) {
				// A source that cannot be read is the one case where nothing
				// reaches the store, so the predecessor is genuinely untouched
				// and saying so is the only time that claim is earned.
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "docs/object", "the bytes already there")
			priorTag := objectTag(t, s, "media", "docs/object")
			path := writeFile(t, t.TempDir(), "object", "the bytes this job sent")
			preflight, err := PreflightUploadReplacing(t.Context(), s, "media", "docs/", []string{path})
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || !preflight.Items[0].Replaces {
				t.Fatalf("the replacing preflight did not admit the live name: %+v %v", preflight, err)
			}
			item := preflight.Items[0]
			item.Route = "native"
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.arm(t, mock, file)
			result := nativeUpload(t.Context(), s, "media", item, file, item.Replaces, comparesAgainstLive(t, s, "media", item))
			if result.Result == transfercopy.ResultCopied {
				t.Fatalf("the fixture did not produce a failure: %+v", result.Observation)
			}
			if result.Effect != tc.effect {
				t.Errorf("the outcome is %q, not %q: %+v", result.Effect, tc.effect, result.Observation)
			}
			if result.Diagnostic.Stage != tc.stage {
				t.Errorf("the outcome became unknown at %q, not %q", result.Diagnostic.Stage, tc.stage)
			}
			// What the store holds, read back rather than assumed. An
			// unresolved outcome means exactly that nothing may claim the
			// predecessor survived: here it did not.
			if landed := objectTag(t, s, "media", "docs/object") != priorTag; landed != tc.landed {
				t.Errorf("the store holds a changed object = %v, want %v", landed, tc.landed)
			}
			// Either way nothing is counted as a replacement, because none
			// was witnessed.
			run := &uploadRun{report: &Report{}}
			if err := run.observe(item, result); err != nil {
				t.Fatalf("observing: %v", err)
			}
			if run.report.Replaced != 0 {
				t.Fatalf("an unwitnessed publication was counted as a replacement: %+v", run.report)
			}
		})
	}
}

// objectTag is what identifies the object at a name now.
func objectTag(t *testing.T, s *swiftcontrol.Session, container, name string) string {
	t.Helper()
	detail, err := s.ObjectDetail(t.Context(), container, name)
	if err != nil {
		t.Fatalf("reading %s/%s: %v", container, name, err)
	}
	return strings.Trim(detail.ETag, `"`)
}

// Finding: a dynamic manifest's segments may sit below child paths, and the
// default delimiter view groups everything under a child prefix into one
// pseudo-directory row that names no object. Listed that way, a manifest
// whose parts are nested is inventoried as holding nothing at all, which
// would report space as accounted for that nothing had accounted for.
func TestReplacingADynamicManifestInventoriesNestedParts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
	}{
		{name: "flat", parts: []string{"subject/run/0001", "subject/run/0002"}},
		{name: "nested", parts: []string{"subject/run/a/0001", "subject/run/b/0002"}},
		{name: "mixed", parts: []string{"subject/run/0001", "subject/run/a/0002", "subject/run/a/b/0003"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			mock.AddContainer("media_segments", "standard")
			for _, part := range tc.parts {
				seed(mock, "media_segments", part, "01234567")
			}
			mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/subject/run/"})
			detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatal(err)
			}
			inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
			if inventory.Unread != "" {
				t.Fatalf("the prefix was not listed: %q", inventory.Unread)
			}
			var named []string
			for _, fragment := range inventory.Names {
				if fragment.Container != "media_segments" {
					t.Errorf("a segment was named in the wrong container: %+v", fragment)
				}
				named = append(named, fragment.Name)
			}
			slices.Sort(named)
			want := slices.Clone(tc.parts)
			slices.Sort(want)
			if !slices.Equal(named, want) {
				t.Fatalf("the inventory named %v, not %v", named, want)
			}
			if inventory.Counted != len(tc.parts) || inventory.Truncated() {
				t.Errorf("the count does not match the names: %+v", inventory)
			}
			// A listing is a lower bound whatever shape it has: an
			// eventually consistent store may omit a segment that exists.
			if inventory.Complete {
				t.Error("a listed prefix was reported as the exact set")
			}
			if inventory.Prefix != "media_segments/subject/run/" {
				t.Errorf("the record lost the prefix that stays listable: %q", inventory.Prefix)
			}
		})
	}
}

// K-PERF-001: one replacement must not size its memory to a remote prefix.
// Past the cap the entries are counted and not named, and the record says
// so rather than reporting the shorter list as the whole of it. The prefix
// is what keeps the truncated case recoverable: it is still listable after
// the replacement, which is why this is the only case the cap can truncate.
func TestReplacingADynamicManifestBoundsItsInventory(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	// More parts than the cap, and more than one server page, so the bound
	// is proven against a listing that pages rather than one that fits.
	parts := MaxListedFragments + 37
	for i := range parts {
		mock.PutObject("media_segments", fmt.Sprintf("subject/run/%06d", i), swifttest.Object{Bytes: 0})
	}
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/subject/run/"})
	detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil {
		t.Fatal(err)
	}
	inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
	if inventory.Unread != "" {
		t.Fatalf("the prefix was not listed: %q", inventory.Unread)
	}
	if len(inventory.Names) != MaxListedFragments {
		t.Fatalf("the inventory held %d names, not the cap of %d", len(inventory.Names), MaxListedFragments)
	}
	if inventory.Counted != parts || !inventory.Truncated() {
		t.Fatalf("the count is not the whole of what was read: counted=%d of %d", inventory.Counted, parts)
	}
	// The names it does hold are the first ones it read, in order, so the
	// record is a prefix of the listing and not an arbitrary sample.
	for i, fragment := range inventory.Names {
		if want := fmt.Sprintf("subject/run/%06d", i); fragment.Name != want {
			t.Fatalf("name %d is %q, not %q", i, fragment.Name, want)
		}
	}
	if inventory.Prefix != "media_segments/subject/run/" {
		t.Errorf("a truncated record must keep the prefix it can be listed from: %q", inventory.Prefix)
	}
}

// segmentRef is one existing segment object an alias manifest names.
type segmentRef struct{ container, name, content string }

// aliasStatic writes a static manifest over segments that ALREADY exist,
// so the alias shares the identities it names rather than private copies of
// their bytes. stageStatic seeds its own parts, which is right for staging
// and wrong for an alias: an alias over its own parts survives the deletion
// of everything it was supposed to be sharing.
func aliasStatic(t *testing.T, mock *swifttest.Server, container, name string, segments ...segmentRef) {
	t.Helper()
	var data []byte
	var etags, manifest strings.Builder
	manifest.WriteString("[")
	for i, segment := range segments {
		if i > 0 {
			manifest.WriteString(",")
		}
		data = append(data, segment.content...)
		etags.WriteString(md5hex(segment.content))
		fmt.Fprintf(&manifest, `{"name":"/%s/%s","hash":%q,"bytes":%d}`,
			segment.container, segment.name, md5hex(segment.content), len(segment.content))
	}
	manifest.WriteString("]")
	tag := md5hex(etags.String())
	mock.PutObject(container, name, swifttest.Object{
		Bytes: int64(len(data)), Data: data, SLO: true, ETag: `"` + tag + `"`,
		SLOEtag: tag, SLOManifestJSON: manifest.String(), ContentType: "application/octet-stream",
	})
}

// ADR 0081 decision 5 and its retained mutant: the predecessor is read and
// recorded BEFORE the name is published over, because afterwards there is
// nothing left to read. The order is observed at the store rather than
// inferred from the code, so moving the reading after the publication fails
// here whatever the reading then returns.
func TestReplacementRecordsThePredecessorBeforePublishing(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "subject/run/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/subject/run/"})
	var order []string
	mock.AfterRequest = func(method, path string) {
		if method == "PUT" && strings.HasSuffix(path, "/media/docs/object") {
			order = append(order, "published")
		}
	}
	path := writeFile(t, t.TempDir(), "object", "the bytes this job sent")
	preflight, err := PreflightUploadReplacing(t.Context(), s, "media", "docs/", []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || !preflight.Items[0].Replaces {
		t.Fatalf("the replacing preflight did not admit the live manifest: %+v %v", preflight, err)
	}
	// The native route, so the publication is this process's own PUT and
	// the order is the one this contract fixes.
	preflight.Items[0].Route = "native"
	recorder := &UploadRecorder{
		Admit:   func(UploadAdmission) error { return nil },
		Execute: func(UploadItem, transfercopy.Observation) error { return nil },
		Verify:  func(UploadItem, transfercopy.Observation) error { return nil },
		Replacing: func(_ UploadItem, observed ObservedPredecessor) error {
			// Recorded with the names it could still read, which is the
			// whole reason the order matters.
			if len(observed.Fragments.Names) != 1 {
				t.Errorf("the record was taken with nothing left to read: %+v", observed.Fragments)
			}
			order = append(order, "recorded")
			return nil
		},
		// The run now asks what the acknowledgement was shown, so that
		// ADR 0085's comparison has something to compare against. This
		// answers with what is live, which is what an acknowledgement taken
		// at that moment would have captured.
		Acknowledged: acknowledgedNow(t, s, "media", preflight.Items[0]),
	}
	report, err := Upload(t.Context(), s, nil, "aeronautes_dst:media", "media", "docs/", "ordering", preflight, nil, nil, recorder)
	if err != nil || report == nil || report.Done != 1 {
		t.Fatalf("the replacement did not publish: %+v %v", report, err)
	}
	if len(order) != 2 || order[0] != "recorded" || order[1] != "published" {
		t.Fatalf("the predecessor was not recorded before the publication: %v", order)
	}
}

// Finding F2 of db:746: the display manifest reader decodes one array
// without checking the status, the absence of a Content-Range, a clean end
// or any byte bound, so a partial or malformed response parses as a
// manifest. Credited here it would become the durable, exact inventory of
// something this operation then replaces. Every spoiled shape must be
// reported as an inventory that could not be taken.
func TestReplacingAStaticManifestRefusesAnIncompleteResponse(t *testing.T) {
	for _, fault := range []string{"", "partial", "ranged", "trailing", "truncated"} {
		name := fault
		if name == "" {
			name = "a complete response"
		}
		t.Run(name, func(t *testing.T) {
			s, mock := session(t)
			mock.AddContainer("media_segments", "standard")
			seed(mock, "media_segments", "parts/0001", "01234567")
			digest := md5hex("01234567")
			mock.PutObject("media", "docs/object", swifttest.Object{
				SLO: true, Bytes: 8, ETag: `"` + md5hex(digest) + `"`, Data: []byte("01234567"),
				SLOManifestJSON: `[{"name":"/media_segments/parts/0001","hash":"` + digest + `","bytes":8}]`,
			})
			if fault != "" {
				mock.ManifestReadFault = map[string]string{"media/docs/object": fault}
			}
			detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatal(err)
			}
			inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
			if fault == "" {
				if inventory.Unread != "" || !inventory.Complete || len(inventory.Names) != 1 {
					t.Fatalf("a complete response was not read: %+v", inventory)
				}
				return
			}
			if inventory.Unread == "" || inventory.Complete || len(inventory.Names) != 0 || inventory.Counted != 0 {
				t.Fatalf("a %s response was credited as an inventory: %+v", fault, inventory)
			}
		})
	}
}
