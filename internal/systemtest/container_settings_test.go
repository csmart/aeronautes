package systemtest

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// ADR 0071: a container's access and feature settings are facts one HEAD
// reported, derived once, held under the connection generation they were
// observed in, folded into the container rows without any HEAD on listing
// (DESIGN 14.2), and refreshed by a saved edit. These cases seed one
// container per setting on both fixtures and walk the three sources.
func containerSettingsCases(t *testing.T, h *Harness) {
	ctx := context.Background()
	seed := func(label string, headers map[string]string) string {
		name := h.Container(h.Primary, label)
		h.rawPut(h.Primary, "/"+name, nil, headers)
		return name
	}
	public := seed("cs-public", map[string]string{"X-Container-Read": ".r:*"})
	listing := seed("cs-listing", map[string]string{"X-Container-Read": ".r:*,.rlistings"})
	referrer := seed("cs-referrer", map[string]string{"X-Container-Read": ".r:.example.com"})
	denial := seed("cs-denial", map[string]string{"X-Container-Read": ".r:-.example.com"})
	shared := seed("cs-shared", map[string]string{"X-Container-Write": h.Secondary.user})
	private := seed("cs-private", nil)
	names := []string{public, listing, referrer, denial, shared, private}
	// listingCost counts the mock's requests for one account listing; the
	// listing pages are the whole cost, before and after facts exist, so a
	// HEAD per row would show as a difference (DESIGN 14.2).
	listingCost := func() (app.ContainerPageDTO, int) {
		before := 0
		if h.MockPrimary != nil {
			before = h.MockPrimary.Requests()
		}
		page, err := h.Browse.Containers(ctx, h.Primary.BookmarkID, "", 5000)
		if err != nil {
			t.Fatal(err)
		}
		if h.MockPrimary == nil {
			return page, 0
		}
		return page, h.MockPrimary.Requests() - before
	}
	page, unchecked := listingCost()
	for _, row := range page.Rows {
		if row.Settings != nil {
			t.Fatalf("an unchecked row carries settings: %+v", row)
		}
	}
	if h.MockPrimary != nil && (unchecked < 1 || unchecked > 2) {
		t.Fatalf("listing the account made %d requests, want only its one or two listing pages", unchecked)
	}
	// Selection HEADs: each container's facts come back with the detail and
	// are then folded into its row.
	want := map[string]app.ContainerSettingsDTO{
		public:   {PublicRead: true, ReadACL: ".r:*"},
		listing:  {PublicRead: true, PublicListing: true, ReadACL: ".r:*,.rlistings"},
		referrer: {ReferrerGrants: []string{".example.com"}, ReadACL: ".r:.example.com"},
		denial:   {ReadACL: ".r:-.example.com"},
		shared:   {SharedWrite: true, WriteACL: h.Secondary.user},
		private:  {},
	}
	generation := int64(0)
	for _, name := range names {
		detail, err := h.Inspector.Container(ctx, h.Primary.BookmarkID, name)
		if err != nil {
			t.Fatal(err)
		}
		got := detail.Settings
		if generation == 0 {
			generation = got.Generation
		}
		expected := want[name]
		if got.PublicRead != expected.PublicRead || got.PublicListing != expected.PublicListing || got.SharedWrite != expected.SharedWrite ||
			len(got.ReferrerGrants) != len(expected.ReferrerGrants) || (len(expected.ReferrerGrants) == 1 && got.ReferrerGrants[0] != expected.ReferrerGrants[0]) ||
			got.ReadACL != expected.ReadACL || got.WriteACL != expected.WriteACL || got.Generation != generation || got.Generation != detail.Scope.Generation || got.ObservedAt == "" {
			t.Fatalf("%s derived %+v, want %+v under generation %d", name, got, expected, detail.Scope.Generation)
		}
		if got.StaticWeb || got.Quota || got.CORS || (got.VersioningMode != "off" && got.VersioningMode != "") {
			t.Fatalf("%s reports features that were not set: %+v", name, got)
		}
	}
	page, checked := listingCost()
	if checked != unchecked {
		t.Fatalf("listing with facts held made %d requests, %d without them: a row was HEADed on listing", checked, unchecked)
	}
	rows := map[string]app.ContainerRowDTO{}
	for _, row := range page.Rows {
		rows[row.Name] = row
	}
	for _, name := range names {
		row, ok := rows[name]
		if !ok || row.Settings == nil || row.Settings.Generation != generation || row.Settings.PublicRead != want[name].PublicRead || row.Settings.ReadACL != want[name].ReadACL {
			t.Fatalf("row %s does not carry the HEAD's facts: %+v", name, row.Settings)
		}
	}
	// A saved edit that makes a container public refreshes its fact from the
	// verified post-write state.
	detail, err := h.Inspector.Container(ctx, h.Primary.BookmarkID, private)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Mutate.UpdateContainer(ctx, private, detail.EditableFingerprint, app.ContainerEditDTO{
		Meta: map[string]string{}, ReadACL: ".r:*", VersioningMode: "off", AckPublic: true,
	})
	if err != nil || result.Outcome != "applied" || result.ContainerAfter == nil || !result.ContainerAfter.Settings.PublicRead || result.ContainerAfter.Settings.Generation != generation {
		t.Fatalf("public edit did not refresh the fact: %+v %v", result, err)
	}
	page, _ = listingCost()
	for _, row := range page.Rows {
		if row.Name == private && (row.Settings == nil || !row.Settings.PublicRead) {
			t.Fatalf("the edited container's row still shows the old fact: %+v", row.Settings)
		}
	}
	// Facts belong to the connection that observed them: after switching to
	// the other account and back, the new generation shows none until a
	// container is selected or checked again.
	if _, err := h.Browse.Connect(ctx, h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	other, err := h.Browse.Containers(ctx, h.Secondary.BookmarkID, "", 5000)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range other.Rows {
		if row.Settings != nil {
			t.Fatalf("the other account's row carries a fact: %+v", row)
		}
	}
	// Returning to the same cached connection keeps its generation, so its
	// facts are still that connection's and still shown.
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	page, _ = listingCost()
	if page.Scope.Generation != generation {
		t.Fatalf("returning to the cached connection changed generation %d to %d", generation, page.Scope.Generation)
	}
	if row := rowNamed(page, public); row == nil || row.Settings == nil || !row.Settings.PublicRead {
		t.Fatalf("the cached connection lost its facts: %+v", row)
	}
	// A rekeyed bookmark connects under a new generation: the older facts
	// are discarded and nothing is shown until a container is selected or
	// checked again.
	namedReadRekey(t, h, h.Primary)
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	page, _ = listingCost()
	if page.Scope.Generation == generation {
		t.Fatalf("rekeying kept generation %d", generation)
	}
	for _, row := range page.Rows {
		if row.Settings != nil {
			t.Fatalf("a fact from generation %d survived the new connection: %+v", generation, row)
		}
	}
	detail, err = h.Inspector.Container(ctx, h.Primary.BookmarkID, public)
	if err != nil || !detail.Settings.PublicRead || detail.Settings.Generation != page.Scope.Generation {
		t.Fatalf("selecting under the new generation did not re-observe the fact: %+v %v", detail.Settings, err)
	}
}

func rowNamed(page app.ContainerPageDTO, name string) *app.ContainerRowDTO {
	for i := range page.Rows {
		if page.Rows[i].Name == name {
			return &page.Rows[i]
		}
	}
	return nil
}

func TestContainerSettingsMock(t *testing.T)      { containerSettingsCases(t, findMockHarness(t)) }
func TestContainerSettingsRealSwift(t *testing.T) { containerSettingsCases(t, New(t)) }
