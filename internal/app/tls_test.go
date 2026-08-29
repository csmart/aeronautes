package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"strings"
	"testing"

	"os"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// tlsMock serves the mock cluster over HTTPS with httptest's
// self-signed certificate, returning its CA in PEM form.
func tlsMock(t *testing.T) (*swifttest.Server, string, string) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewTLSServer(mock)
	t.Cleanup(srv.Close)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return mock, srv.URL, string(caPEM)
}

func TestCustomCATrust(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.transfer.connections.SetCADir(t.TempDir())
	_, baseURL, caPEM := tlsMock(t)

	// System trust refuses the self-signed endpoint.
	systemBookmark, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "TLS system", AuthURL: baseURL + "/auth/v1.0", Username: "test:tester", TLSMode: "system",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, systemBookmark.ID); err == nil ||
		!strings.Contains(err.Error(), "certificate") {
		t.Fatalf("self-signed accepted under system trust: %v", err)
	}

	// Custom-CA mode requires the bundle…
	if _, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "TLS no ca", AuthURL: baseURL + "/auth/v1.0", Username: "test:tester", TLSMode: "custom_ca",
	}, "testing"); err == nil || !strings.Contains(err.Error(), "CA bundle") {
		t.Fatalf("custom_ca without PEM accepted: %v", err)
	}
	if _, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "TLS junk", AuthURL: baseURL + "/auth/v1.0", Username: "test:tester",
		TLSMode: "custom_ca", CAPem: "not a pem",
	}, "testing"); err == nil {
		t.Fatal("junk PEM accepted")
	}

	// …and with it, the connection verifies against exactly that CA.
	caBookmark, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "TLS ca", AuthURL: baseURL + "/auth/v1.0", Username: "test:tester",
		TLSMode: "custom_ca", CAPem: caPEM,
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if !caBookmark.HasCA {
		t.Error("HasCA not reported")
	}
	if _, err := h.browse.Connect(ctx, caBookmark.ID); err != nil {
		t.Fatalf("custom-CA connect: %v", err)
	}

	// The engine gets the trust as a file whose content is the bundle.
	insecure, caFile, err := h.transfer.connections.TLSRemote(ctx, caBookmark.ID)
	if err != nil || insecure || caFile == "" {
		t.Fatalf("TLSRemote = %v %q %v", insecure, caFile, err)
	}
	block, _ := pem.Decode([]byte(caPEM))
	written, err := readFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	writtenBlock, _ := pem.Decode(written)
	if writtenBlock == nil || string(writtenBlock.Bytes) != string(block.Bytes) {
		t.Fatal("materialised CA file does not match the stored bundle")
	}
	if _, err := x509.ParseCertificate(writtenBlock.Bytes); err != nil {
		t.Fatal(err)
	}

	// Insecure mode connects too, loudly per-bookmark.
	insecureBookmark, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "TLS insecure", AuthURL: baseURL + "/auth/v1.0", Username: "test:tester", TLSMode: "insecure",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, insecureBookmark.ID); err != nil {
		t.Fatalf("insecure connect: %v", err)
	}
	flag, caFile, _ := h.transfer.connections.TLSRemote(ctx, insecureBookmark.ID)
	if !flag || caFile != "" {
		t.Fatalf("insecure TLSRemote = %v %q", flag, caFile)
	}
}
