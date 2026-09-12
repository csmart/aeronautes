package systemtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/ncw/swift/v2"
)

func TestPublicConnectHTTPErrorRefusalMock(t *testing.T) {
	h := findMockHarness(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	primary := h.Container(h.Primary, "public-error-primary")
	secondary := h.Container(h.Secondary, "public-error-secondary")
	h.Seed(h.Primary, primary, "keep", []byte("primary content"), nil)
	h.Seed(h.Secondary, secondary, "keep", []byte("secondary content"), nil)
	beforePrimary := h.SnapshotNamespace(h.Primary)
	beforeSecondary := h.SnapshotNamespace(h.Secondary)
	beforeAccount, healthy := h.ConnectBookmark(ctx, h.Primary.BookmarkID)
	if healthy.Err() != nil || beforeAccount.Name != "AUTH_primary" {
		t.Fatalf("healthy public Connect control = %+v, %v", beforeAccount, healthy.Err())
	}

	const user = "probe:public-error"
	const key = "SYNTHETIC_CONNECT_KEY_b39126"
	const bodyCanary = "SYNTHETIC_BODY_ONLY_c057da"
	type requestObservation struct {
		method, path, protocol string
		keyMatched             bool
		ioErr                  error
	}
	var mu sync.Mutex
	var requests []requestObservation
	var handlers sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		observation := requestObservation{
			method: r.Method, path: r.URL.Path, protocol: r.Proto,
			keyMatched: r.Header.Get("X-Auth-User") == user && r.Header.Get("X-Auth-Key") == key,
		}
		defer func() {
			mu.Lock()
			requests = append(requests, observation)
			mu.Unlock()
		}()
		// WriteHeader would replace the provider-selected reason phrase.
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			observation.ioErr = err
			return
		}
		if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			observation.ioErr = errors.Join(err, conn.Close())
			return
		}
		_, writeErr := fmt.Fprintf(rw, "HTTP/1.1 500 reason-only-%s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			r.Header.Get("X-Auth-Key"), len(bodyCanary), bodyCanary)
		observation.ioErr = errors.Join(writeErr, rw.Flush(), conn.Close())
	}))
	var closeOnce sync.Once
	closeServer := func() {
		closeOnce.Do(func() {
			server.Close()
			// Close does not wait for hijacked handlers; these remain fixture-owned.
			handlers.Wait()
		})
	}
	t.Cleanup(closeServer)
	saved, err := h.Bookmarks.Save(ctx, app.BookmarkDTO{
		DisplayName: "public-error", AuthURL: server.URL + "/auth/v1.0", Username: user, TLSMode: "system",
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	beforeBookmarks, err := h.Bookmarks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	account, receipt := h.ConnectBookmark(ctx, saved.ID)
	closeServer()
	if receipt.Err() == nil {
		t.Fatal("failed authentication became a successful public Connect")
	}
	var normalized *swiftcontrol.SwiftError
	var wire *swift.Error
	if !errors.As(receipt.Err(), &normalized) || !errors.As(receipt.Err(), &wire) || !errors.Is(receipt.Err(), wire) {
		t.Fatalf("public error lost typed/native cause: %v", receipt.Err())
	}
	if normalized.Kind != swiftcontrol.KindServer || normalized.HTTPStatus != 500 || !normalized.Retryable {
		t.Fatalf("public error classification changed: %+v", normalized)
	}
	if !reflect.DeepEqual(account, app.AccountDTO{}) {
		t.Fatalf("refused Connect returned account data: %+v", account)
	}
	mu.Lock()
	observed := append([]requestObservation(nil), requests...)
	mu.Unlock()
	if len(observed) != 1 || observed[0].method != http.MethodGet || observed[0].path != "/auth/v1.0" || observed[0].protocol != "HTTP/1.1" || !observed[0].keyMatched || observed[0].ioErr != nil {
		t.Fatalf("owned auth fixture requests/closure = %+v", observed)
	}
	afterAccount, err := h.Browse.Account(ctx)
	if err != nil || !reflect.DeepEqual(beforeAccount, afterAccount) {
		t.Fatalf("refused Connect changed active account: before=%+v after=%+v error=%v", beforeAccount, afterAccount, err)
	}
	page, err := h.Browse.Containers(ctx, "", 100)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Name != primary || page.NextMarker != "" {
		t.Fatalf("refused Connect changed active namespace: page=%+v error=%v", page, err)
	}
	afterBookmarks, err := h.Bookmarks.List(ctx)
	if err != nil || !reflect.DeepEqual(beforeBookmarks, afterBookmarks) {
		t.Fatalf("refused Connect changed bookmarks: error=%v before=%+v after=%+v", err, beforeBookmarks, afterBookmarks)
	}
	bookmarkJSON, err := json.Marshal(afterBookmarks)
	if err != nil {
		t.Fatal(err)
	}
	h.AssertNoCollateral(beforePrimary, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeSecondary, h.SnapshotNamespace(h.Secondary))
	if strings.Contains(string(bookmarkJSON), key) || strings.Contains(string(bookmarkJSON), bodyCanary) {
		t.Fatal("public bookmark DTO exposed an auth canary")
	}
	if strings.Contains(receipt.Err().Error(), bodyCanary) || strings.Contains(normalized.Message, bodyCanary) {
		t.Fatal("SEC-PUBLIC-BODY: public Connect returned the response body canary")
	}
	if strings.Contains(receipt.Err().Error(), key) || strings.Contains(normalized.Message, key) {
		t.Fatal("SEC-PUBLIC-REASON: public Connect returned the synthetic saved key from the HTTP reason phrase")
	}
	if normalized.Message != "Internal Server Error" {
		t.Fatalf("public Message = %q, want local Internal Server Error", normalized.Message)
	}
	receipt.CreditRefusal()
}
