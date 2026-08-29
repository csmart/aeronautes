package swifttest_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// The mock's contract is "the real adapter can speak to it": auth,
// account portrait, delimiter listing, and object HEAD all round-trip.
func TestAdapterRoundTrip(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	srv := httptest.NewServer(mock)
	defer srv.Close()

	s, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL:  srv.URL + "/auth/v1.0",
		Username: "test:tester",
		APIKey:   "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	p, err := s.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Containers != 6 {
		t.Errorf("Containers = %d, want 6", p.Containers)
	}

	var containers []string
	for c, err := range s.Containers(ctx, swiftcontrol.ListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		containers = append(containers, c.Name)
	}
	if len(containers) != 6 || containers[0] != ".file-segments" {
		t.Errorf("containers = %v", containers)
	}

	var names []string
	for e, err := range s.Objects(ctx, "fixtures", swiftcontrol.ObjectListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		switch v := e.(type) {
		case swiftcontrol.ObjectEntry:
			names = append(names, "obj:"+v.Name)
		case swiftcontrol.PrefixEntry:
			names = append(names, "pfx:"+v.Prefix)
		}
	}
	want := []string{
		"obj:empty-object", "obj:external-dlo", "obj:external-slo",
		"pfx:fake/", "pfx:folder-marker/", "pfx:implied/",
		"pfx:normal/", "pfx:unicode/", "pfx:weird/",
	}
	if len(names) != len(want) {
		t.Fatalf("root rows = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, names[i], want[i])
		}
	}

	d, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if d.Class != swiftcontrol.ClassDLO || d.Manifest != "external-dlo-segments/wedding-video/" {
		t.Errorf("DLO detail = %+v", d)
	}
	// A DLO manifest HEAD reports the assembled logical size (§20).
	if d.ContentLength != 3_000_000_000 {
		t.Errorf("DLO ContentLength = %d, want assembled 3 GB", d.ContentLength)
	}
	if d.Meta["Shoot"] != "wedding" {
		t.Errorf("Meta = %v", d.Meta)
	}

	if _, err := s.ObjectDetail(ctx, "fixtures", "weird/repeated//slash"); err != nil {
		t.Errorf("hostile-name HEAD failed: %v", err)
	}
}
