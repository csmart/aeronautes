package swiftcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// archiveSession runs the mock behind an optional fault and returns a session
// over it; containers is the set the account holds.
func archiveSession(t *testing.T, fault func(http.ResponseWriter, *http.Request) bool, containers ...string) (*Session, *swifttest.Server) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	for _, name := range containers {
		mock.AddContainer(name, "standard")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fault != nil && fault(w, r) {
			return
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	return s, mock
}

func containerHEADs(mock *swifttest.Server) int {
	n := 0
	for _, line := range mock.RequestLog() {
		// An object HEAD has a further path segment; a container HEAD ends at
		// the container name.
		if strings.HasPrefix(line, "HEAD ") && strings.Count(line, "/") == 3 {
			n++
		}
	}
	return n
}

// Owners are found by either legacy location, unrelated containers are
// admitted, and the target's own versioning plays no part.
func TestArchiveOwnersFindsEitherLegacyLocation(t *testing.T) {
	s, mock := archiveSession(t, nil, "archive", "by-versions", "by-history", "elsewhere", "plain")
	mock.EnableVersionArchive("by-versions", "archive", false)
	mock.EnableVersionArchive("by-history", "archive", true)
	mock.EnableVersionArchive("elsewhere", "another-archive", false)

	got := s.ArchiveOwners(context.Background(), "archive", 1000)
	if got.Unestablished != "" || !slices.Equal(got.Owners, []string{"by-history", "by-versions"}) {
		t.Fatalf("owners = %+v, want by-history and by-versions with the answer established", got)
	}
	if none := s.ArchiveOwners(context.Background(), "plain", 1000); none.Unestablished != "" || len(none.Owners) != 0 {
		t.Fatalf("an unrelated container reads as an archive: %+v", none)
	}
}

// A location is matched by the name it denotes, not by its wire spelling:
// the store keeps it percent-encoded, with a plus kept literal, and a
// spelling that cannot be decoded leaves the answer unestablished rather
// than silently clearing a container that holds earlier versions.
func TestArchiveOwnersDecodesTheLocation(t *testing.T) {
	s, mock := archiveSession(t, nil, "space +%", "encoded-owner", "broken-owner", "plain")
	mock.EnableVersionArchive("encoded-owner", "space%20+%25", false)

	got := s.ArchiveOwners(context.Background(), "space +%", 1000)
	if got.Unestablished != "" || !slices.Equal(got.Owners, []string{"encoded-owner"}) {
		t.Fatalf("the encoded location did not match its container: %+v", got)
	}

	mock.EnableVersionArchive("broken-owner", "space%zz", true)
	got = s.ArchiveOwners(context.Background(), "space +%", 1000)
	if !slices.Equal(got.Owners, []string{"encoded-owner"}) || !strings.Contains(got.Unestablished, "could not be interpreted") {
		t.Fatalf("an undecodable location did not leave the answer unestablished: %+v", got)
	}
}

// An account above the bound is not read: the answer says so and no
// container is HEADed, which is the cost the bound exists to cap.
func TestArchiveOwnersStopsAtTheBoundWithoutReading(t *testing.T) {
	s, mock := archiveSession(t, nil, "archive", "a", "b", "c")
	mock.EnableVersionArchive("a", "archive", false)
	before := containerHEADs(mock)

	got := s.ArchiveOwners(context.Background(), "archive", 2)
	if len(got.Owners) != 0 || !strings.Contains(got.Unestablished, "reads at most 2") {
		t.Fatalf("an account above the bound was read or misreported: %+v", got)
	}
	if n := containerHEADs(mock); n != before {
		t.Fatalf("%d container HEADs were made above the bound", n-before)
	}
}

// A container whose HEAD fails leaves the answer unestablished and keeps the
// owners that were read, so the dialog can say both.
func TestArchiveOwnersReportsAFailedReadAsUnestablished(t *testing.T) {
	broken := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/broken") {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	s, mock := archiveSession(t, broken, "archive", "broken", "owner")
	mock.EnableVersionArchive("owner", "archive", true)

	got := s.ArchiveOwners(context.Background(), "archive", 1000)
	if !slices.Equal(got.Owners, []string{"owner"}) || !strings.Contains(got.Unestablished, "could not be read") {
		t.Fatalf("a failed read did not leave the answer unestablished with the owner found: %+v", got)
	}
}

// A container deleted between the listing and its own read is not an owner and
// is not a doubt either: the answer stays established, with the owners found.
func TestArchiveOwnersTreatsAContainerGoneSinceTheListingAsNoOwner(t *testing.T) {
	gone := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		return false
	}
	s, mock := archiveSession(t, gone, "archive", "gone", "owner")
	mock.EnableVersionArchive("owner", "archive", true)

	got := s.ArchiveOwners(context.Background(), "archive", 1000)
	if !slices.Equal(got.Owners, []string{"owner"}) || got.Unestablished != "" {
		t.Fatalf("a container that vanished left the answer in doubt or lost an owner: %+v", got)
	}
}
