package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestBookmarkRetargetRequiresKeyReentry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	before, err := h.bookmarks.List(ctx)
	if err != nil || len(before) != 1 {
		t.Fatalf("bookmark fixture: %v", err)
	}
	live, err := h.transfer.connections.Session()
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(sink.Close)
	edited := before[0]
	edited.DisplayName = "retargeted"
	edited.AuthURL = sink.URL + "/auth/v1.0"
	edited.HasCredential = false
	_, saveErr := h.bookmarks.Save(ctx, edited, "")
	if saveErr == nil {
		// Exercise the archived attack's second bridge call if Save admits it.
		_, _ = h.browse.Connect(ctx, edited.ID)
		t.Errorf("blank-key retarget was accepted")
	} else if !strings.Contains(saveErr.Error(), "re-enter") {
		t.Errorf("refusal does not explain key reentry: %v", saveErr)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("retarget receiver received %d request(s)", got)
	}
	after, err := h.bookmarks.List(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("refused edit changed the bookmark: %v", err)
	}
	current, err := h.transfer.connections.Session()
	if err != nil || current != live {
		t.Fatalf("refused edit invalidated the existing session: %v", err)
	}
	if _, err := current.AccountPortrait(ctx); err != nil {
		t.Fatalf("existing connection stopped working after refusal: %v", err)
	}
}

func TestBookmarkCAReimportKeepsCredentialBinding(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, endpoint, ca := tlsMock(t)
	b, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "custom trust", AuthURL: endpoint + "/auth/v1.0",
		Username: "test:tester", TLSMode: "custom_ca", CAPem: ca,
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	b.CAPem = ca
	if _, err := h.bookmarks.Save(ctx, b, ""); err != nil {
		t.Fatalf("identical CA reimport demanded reentry: %v", err)
	}
	if _, err := h.browse.Connect(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM ca_certs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	b.CAPem = ca + "\n"
	if _, err := h.bookmarks.Save(ctx, b, ""); err == nil {
		t.Error("changed CA bytes retained a stored key")
	}
	var after int
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM ca_certs`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("refused edit published a CA row: before=%d after=%d", before, after)
	}
	if _, err := h.browse.Account(ctx, b.ID); err != nil {
		t.Fatalf("refused trust edit disconnected the bookmark: %v", err)
	}
}

func TestBookmarkReentryAllowsReplacementReceiver(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	list, err := h.bookmarks.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal("missing initial bookmark")
	}
	mock := swifttest.New("replacement:user", "replacement-key")
	var authRequests, wrongKeys atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1.0" {
			authRequests.Add(1)
			if r.Header.Get("X-Auth-Key") != "replacement-key" {
				wrongKeys.Add(1)
			}
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(receiver.Close)
	b := list[0]
	b.AuthURL = receiver.URL + "/auth/v1.0"
	b.Username = "replacement:user"
	if _, err := h.bookmarks.Save(ctx, b, "replacement-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if authRequests.Load() == 0 || wrongKeys.Load() != 0 {
		t.Fatalf("replacement authentication requests=%d wrong keys=%d", authRequests.Load(), wrongKeys.Load())
	}
}

func TestBookmarkWriteFailureRollsBackNewCA(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, endpoint, ca := tlsMock(t)
	b, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "custom trust", AuthURL: endpoint + "/auth/v1.0",
		Username: "test:tester", TLSMode: "custom_ca", CAPem: ca,
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER refuse_bookmark_update BEFORE UPDATE ON bookmarks
			BEGIN SELECT RAISE(ABORT, 'injected bookmark write failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	b.CAPem = ca
	if _, err := h.bookmarks.Save(ctx, b, ""); err == nil || !strings.Contains(err.Error(), "injected bookmark write failure") {
		t.Fatalf("bookmark failure was not reached: %v", err)
	}
	var bundles int
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM ca_certs`).Scan(&bundles); err != nil {
		t.Fatal(err)
	}
	if bundles != 1 {
		t.Errorf("failed bookmark write left %d CA rows, want only the original", bundles)
	}
	if _, err := h.browse.Account(ctx, b.ID); err != nil {
		t.Fatalf("failed bookmark write disconnected the old session: %v", err)
	}
}
