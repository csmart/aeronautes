package swifttest_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

type listingResponse struct {
	*httptest.ResponseRecorder
	beforeHeaders func()
}

func (w *listingResponse) Header() http.Header {
	if change := w.beforeHeaders; change != nil {
		w.beforeHeaders = nil
		change()
	}
	return w.ResponseRecorder.Header()
}

func TestContainerListingKeepsOneSnapshot(t *testing.T) {
	for _, change := range []string{"replace object", "remove container"} {
		t.Run(change, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("source", "standard")
			mock.PutObject("source", "file", swifttest.Object{Bytes: 3, ETag: "old-tag"})
			response := &listingResponse{
				ResponseRecorder: httptest.NewRecorder(),
				beforeHeaders: func() {
					if change == "replace object" {
						mock.PutObject("source", "file", swifttest.Object{Bytes: 20, ETag: "new-tag"})
					} else {
						mock.RemoveObject("source", "file")
						mock.RemoveContainer("source")
					}
				},
			}
			request := httptest.NewRequest(http.MethodGet, "/v1/AUTH_test/source?format=json", nil)
			request.Header.Set("X-Auth-Token", literalMockToken(t, mock))
			// Header publication happens after the handler captures the count.
			// Mutating there must not replace the corresponding listing rows.
			mock.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("X-Container-Object-Count") != "1" || response.Header().Get("X-Container-Bytes-Used") != "3" {
				t.Fatalf("captured container headers: code=%d headers=%v", response.Code, response.Header())
			}
			var rows []struct {
				Name, Hash string
				Bytes      int64
			}
			if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Name != "file" || rows[0].Bytes != 3 || rows[0].Hash != "old-tag" {
				t.Fatalf("listing changed after header snapshot: %+v", rows)
			}
		})
	}
}
