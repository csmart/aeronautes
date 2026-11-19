package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func selectedPreviewHarness(t *testing.T, intercept func(http.ResponseWriter, *http.Request) bool) (*harness, *DeleteService) {
	t.Helper()
	h := newHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if intercept != nil && intercept(w, r) {
			return
		}
		h.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	h.authURL = srv.URL + "/auth/v1.0"
	h.connect(t)
	return h, NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
}

func TestSelectedPreviewSharesArchiveAndKeepsFolderAnswers(t *testing.T) {
	h, service := selectedPreviewHarness(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet && r.URL.Query().Get("prefix") == "unread/" {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	})
	h.mock.AddContainer("owner", "standard")
	h.mock.EnableVersionArchive("owner", "fixtures", true)
	seedObject(h.mock, "fixtures", "first/plain", "four", nil)
	seedObject(h.mock, "fixtures", "first/empty", "", nil)
	seedObject(h.mock, "fixtures", "second/plain", "two", nil)
	seedObject(h.mock, "fixtures", "second-neighbour/plain", "kept", nil)
	req := selectedRequest(h, []ListingRowDTO{
		{Kind: "prefix", Name: "first/"}, {Kind: "prefix", Name: "unread/"},
		{Kind: "prefix", Name: "second/"}, {Kind: "prefix", Name: "absent/"},
		{Kind: "object", Name: "external-slo"},
	})
	req.Confirm = "" // A preview needs no authority to delete.
	before := takeCollateral(t, h)
	start := len(h.mock.RequestLog())
	got, err := service.PreviewSelectedDelete(t.Context(), req)
	if err != nil || len(got) != 4 {
		t.Fatalf("preview = %+v, %v", got, err)
	}
	for i, want := range []struct {
		prefix                    string
		objects, bytes, manifests int64
	}{
		{"first/", 2, 4, 1}, {"unread/", 0, 0, 0}, {"second/", 1, 3, 0}, {"absent/", 0, 0, 0},
	} {
		row := got[i]
		if row.Prefix != want.prefix {
			t.Fatalf("folder %d = %q, want %q", i, row.Prefix, want.prefix)
		}
		if i == 1 {
			if row.Preview != nil || row.Error == "" {
				t.Fatalf("failed folder lost its error: %+v", row)
			}
			continue
		}
		p := row.Preview
		if p == nil || row.Error != "" || p.Objects != want.objects || p.Bytes != want.bytes || p.PossibleManifests != want.manifests || p.RequestCost != want.objects || p.Strategy != "per-name" {
			t.Fatalf("folder %s = %+v, error %q", row.Prefix, p, row.Error)
		}
		if strings.Join(p.ArchiveOf, "|") != "owner" || p.ArchiveUnestablished != "" {
			t.Fatalf("folder %s lost the shared archive observation: %+v", row.Prefix, p)
		}
	}
	reads := map[string]int{}
	for _, line := range h.mock.RequestLog()[start:] {
		reads[line]++
		if !strings.HasPrefix(line, "HEAD ") && !strings.HasPrefix(line, "GET ") {
			t.Fatalf("preview wrote to Swift: %s", line)
		}
	}
	if reads["HEAD /v1/AUTH_test"] != 1 || reads["HEAD /v1/AUTH_test/owner"] != 1 {
		t.Fatalf("archive observation was repeated: %v", reads)
	}
	if after := takeCollateral(t, h); after.jobRows != before.jobRows || after.auditRows != before.auditRows {
		t.Fatalf("preview created durable work: %+v -> %+v", before, after)
	}
	start = h.mock.Requests()
	req.Members = req.Members[4:]
	got, err = service.PreviewSelectedDelete(t.Context(), req)
	if err != nil || len(got) != 0 || h.mock.Requests() != start {
		t.Fatalf("object-only preview read the store: %+v, %v", got, err)
	}
}

func TestSelectedPreviewRefusesInvalidMembershipAndScope(t *testing.T) {
	h, service := selectedHarness(t)
	good := selectedRequest(h, []ListingRowDTO{{Kind: "prefix", Name: "normal/"}})
	requests := h.mock.Requests()
	for _, members := range [][]ListingRowDTO{
		nil, {{Kind: "prefix", Name: ""}}, {{Kind: "prefix", Name: "normal"}},
		{{Kind: "container", Name: "fixtures"}},
		{{Kind: "prefix", Name: "normal/"}, {Kind: "object", Name: "normal/hello.txt"}},
		{{Kind: "prefix", Name: "normal/"}, {Kind: "prefix", Name: "normal/"}},
	} {
		req := good
		req.Members = members
		if _, err := service.PreviewSelectedDelete(t.Context(), req); err == nil {
			t.Fatalf("accepted members %+v", members)
		}
	}
	for _, scope := range []ListingScopeDTO{{}, {BookmarkID: good.Scope.BookmarkID, Generation: good.Scope.Generation + 1}} {
		req := good
		req.Scope = scope
		if _, err := service.PreviewSelectedDelete(t.Context(), req); err == nil {
			t.Fatalf("accepted scope %+v", scope)
		}
	}
	service.ceiling = func(context.Context) int { return 1 }
	good.Members = append(good.Members, ListingRowDTO{Kind: "object", Name: "empty-object"})
	if _, err := service.PreviewSelectedDelete(t.Context(), good); err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("selection ceiling: %v", err)
	}
	if h.mock.Requests() != requests {
		t.Fatal("invalid preview reached Swift")
	}
}

func TestSelectedPreviewBoundsAndCancelsReaders(t *testing.T) {
	for _, phase := range []string{"folders", "archives"} {
		t.Run(phase, func(t *testing.T) {
			entered, left := make(chan struct{}, 64), make(chan struct{}, 64)
			h, service := selectedPreviewHarness(t, func(w http.ResponseWriter, r *http.Request) bool {
				folder := r.Method == http.MethodGet && strings.HasPrefix(r.URL.Query().Get("prefix"), "chosen-")
				archive := r.Method == http.MethodHead && strings.Count(r.URL.Path, "/") == 3
				if (phase == "folders" && folder) || (phase == "archives" && archive) {
					entered <- struct{}{}
					<-r.Context().Done()
					left <- struct{}{}
					return true
				}
				return false
			})
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			members := make([]ListingRowDTO, 32)
			for i := range members {
				members[i] = ListingRowDTO{Kind: "prefix", Name: fmt.Sprintf("chosen-%02d/", i)}
				h.mock.AddContainer(fmt.Sprintf("archive-%02d", i), "standard")
			}
			result := make(chan error, 1)
			go func() { _, err := service.PreviewSelectedDelete(ctx, selectedRequest(h, members)); result <- err }()
			for range 8 {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("eight readers did not reach the held HTTP boundary")
				}
			}
			// The eight reads remain held, so a ninth would exceed the bound.
			select {
			case <-entered:
				t.Fatal("more than eight reads in flight")
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("preview did not retire after cancellation")
			}
			for range 8 {
				select {
				case <-left:
				case <-time.After(5 * time.Second):
					t.Fatal("an HTTP request outlived the preview")
				}
			}
			if len(entered) != 0 {
				t.Fatal("queued work reached Swift after cancellation")
			}
		})
	}
}
