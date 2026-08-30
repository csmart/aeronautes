package oracle

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// rawClient is a minimal raw-HTTP Swift writer for SEEDING test state —
// separate from the Oracle (which only reads) and from the product.
type rawClient struct {
	client     *http.Client
	storageURL string
	token      string
}

func seedClient(t *testing.T) *rawClient {
	t.Helper()
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		t.Skip("AERONAUTES_SAIO_AUTH_URL not set")
	}
	c := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, authURL, nil)
	req.Header.Set("X-Auth-User", envOr("AERONAUTES_SAIO_USER", "test:tester"))
	req.Header.Set("X-Auth-Key", envOr("AERONAUTES_SAIO_KEY", "testing"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return &rawClient{client: c, storageURL: resp.Header.Get("X-Storage-Url"), token: resp.Header.Get("X-Auth-Token")}
}

func (r *rawClient) req(t *testing.T, method, path, query string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	url := r.storageURL + path
	if query != "" {
		url += "?" + query
	}
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", r.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("%s %s: HTTP %d %s", method, path, resp.StatusCode, b)
	}
	return resp
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestOracleWitnessesEveryClass(t *testing.T) {
	seed := seedClient(t)
	oracle, err := Connect(os.Getenv("AERONAUTES_SAIO_AUTH_URL"),
		envOr("AERONAUTES_SAIO_USER", "test:tester"), envOr("AERONAUTES_SAIO_KEY", "testing"))
	if err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("oracle-%d", time.Now().UnixNano())
	data := ns + "-data"
	segs := ns + "-segs"
	for _, c := range []string{data, segs} {
		seed.req(t, http.MethodPut, "/"+c, "", nil, nil).Body.Close()
	}
	defer func() {
		for _, c := range []string{data, segs} {
			purge(t, seed, oracle, c)
		}
	}()

	// Plain object with metadata + content headers.
	plainBody := []byte("plain content here")
	seed.req(t, http.MethodPut, "/"+data+"/plain.txt", "", plainBody, map[string]string{
		"Content-Type": "text/plain", "X-Object-Meta-Team": "ops",
	}).Body.Close()

	// DLO: two segments under a prefix + a manifest.
	seg0, seg1 := bytes.Repeat([]byte("A"), 100), bytes.Repeat([]byte("B"), 60)
	seed.req(t, http.MethodPut, "/"+segs+"/dlo/00", "", seg0, nil).Body.Close()
	seed.req(t, http.MethodPut, "/"+segs+"/dlo/01", "", seg1, nil).Body.Close()
	seed.req(t, http.MethodPut, "/"+data+"/big.bin", "", nil, map[string]string{"X-Object-Manifest": segs + "/dlo/"}).Body.Close()

	// SLO: manifest referencing one leaf.
	leaf := bytes.Repeat([]byte("S"), 40)
	seed.req(t, http.MethodPut, "/"+segs+"/slo/leaf", "", leaf, nil).Body.Close()
	manifest := fmt.Sprintf(`[{"path":"/%s/slo/leaf","size_bytes":40}]`, segs)
	seed.req(t, http.MethodPut, "/"+data+"/manifest.slo", "multipart-manifest=put", []byte(manifest), nil).Body.Close()

	// Symlink to the plain object.
	seed.req(t, http.MethodPut, "/"+data+"/link.txt", "", nil, map[string]string{"X-Symlink-Target": data + "/plain.txt"}).Body.Close()

	snap, err := oracle.SnapshotContainers(data, segs)
	if err != nil {
		t.Fatal(err)
	}
	objs := snap.Containers[data].Objects

	if p := objs["plain.txt"]; p.Class != "plain" || p.Size != int64(len(plainBody)) || p.Meta["Team"] != "ops" || p.ContentType != "text/plain" {
		t.Fatalf("plain witnessed as %+v", p)
	}
	if b := objs["big.bin"]; b.Class != "dlo" || b.Size != 160 || len(b.Segments) != 2 {
		t.Fatalf("DLO witnessed as %+v", b)
	}
	if s := objs["manifest.slo"]; s.Class != "slo" || len(s.Segments) != 1 || s.Segments[0].Size != 40 {
		t.Fatalf("SLO witnessed as %+v", s)
	}
	if l := objs["link.txt"]; l.Class != "symlink" || l.SymlinkTarget != data+"/plain.txt" {
		t.Fatalf("symlink witnessed as %+v", l)
	}

	// Diff catches a SAME-SIZE content swap and nothing else.
	before := snap
	swap := bytes.Repeat([]byte("Z"), len(plainBody)) // same length, different bytes
	seed.req(t, http.MethodPut, "/"+data+"/plain.txt", "", swap, map[string]string{"Content-Type": "text/plain", "X-Object-Meta-Team": "ops"}).Body.Close()
	after, err := oracle.SnapshotContainers(data, segs)
	if err != nil {
		t.Fatal(err)
	}
	d := Diff(before, after)
	if len(d.ChangedObjects) != 1 || d.ChangedObjects[0].Object != "plain.txt" || !hasField(d.ChangedObjects[0].Fields, "content") {
		t.Fatalf("same-size swap not caught as a content change: %s", d)
	}
	if len(d.AddedObjects)+len(d.RemovedObjects)+len(d.ChangedContainers) != 0 {
		t.Fatalf("phantom changes: %s", d)
	}
}

func hasField(fs []Field, want Field) bool {
	for _, f := range fs {
		if f == want {
			return true
		}
	}
	return false
}

func purge(t *testing.T, seed *rawClient, o *Oracle, container string) {
	t.Helper()
	// Enumerate by JSON-decoded raw listing (never scrape structured data
	// with a whitespace-sensitive string split, which silently matched
	// nothing against SAIO's spaced JSON and leaked every object — codex
	// war-room R-04), then delete each object and the container, checking
	// status and retrying an eventual-consistency 409.
	names, err := o.ListObjects(container)
	if err == nil {
		for _, name := range names {
			purgeDelete(seed, "/"+container+"/"+Escape(name))
		}
	}
	if left := purgeDelete(seed, "/"+container); left {
		t.Errorf("purge left container %q behind", container)
	}
}

// purgeDelete issues a DELETE and returns true if the resource still
// appears to exist afterwards (non-2xx, non-404 after bounded retries).
func purgeDelete(seed *rawClient, path string) bool {
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequest(http.MethodDelete, seed.storageURL+path, nil)
		req.Header.Set("X-Auth-Token", seed.token)
		resp, err := seed.client.Do(req)
		if err != nil {
			return true
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusNotFound {
			return false
		}
		if resp.StatusCode == http.StatusConflict {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return true
	}
	return true
}
