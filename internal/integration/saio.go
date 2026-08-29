package integration

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/ncw/swift/v2"
)

// Harness is one run's view of the cluster: the adapter session under
// test, a raw wire connection for fixture setup/teardown, and the
// run-unique container prefix.
type Harness struct {
	Session *swiftcontrol.Session
	// Prefix namespaces this run's containers ("it3f2a1b-"); suffixes that
	// matter to classification (…_segments) are preserved by Container.
	Prefix string

	conn *swift.Connection
	// conn2 is the SECOND account (SAIO's test2), for cross-account
	// transfer tests; nil when its default credentials do not work.
	conn2 *swift.Connection
}

// Conn2 returns the second account's raw connection, or skips the
// calling test when the environment has no second account.
func (h *Harness) Conn2() *swift.Connection { return h.conn2 }

// Container maps a canonical fixture container name into this run.
func (h *Harness) Container(base string) string {
	return h.Prefix + base
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newHarness connects both clients and loads the fixtures. It returns
// (nil, "") reason when the suite should be skipped.
func newHarness(ctx context.Context) (*Harness, string, error) {
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		return nil, "AERONAUTES_SAIO_AUTH_URL not set", nil
	}
	user := envOr("AERONAUTES_SAIO_USER", "test:tester")
	key := envOr("AERONAUTES_SAIO_KEY", "testing")

	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, "", err
	}
	h := &Harness{
		Prefix: "it" + hex.EncodeToString(raw[:]) + "-",
		conn: &swift.Connection{
			AuthUrl:     authURL,
			UserName:    user,
			ApiKey:      key,
			AuthVersion: 1,
			UserAgent:   "aeronautes-integration",
		},
	}
	if err := h.conn.Authenticate(ctx); err != nil {
		return nil, "", fmt.Errorf("fixture connection auth: %w", err)
	}
	conn2 := &swift.Connection{
		AuthUrl:     authURL,
		UserName:    envOr("AERONAUTES_SAIO_USER2", "test2:tester2"),
		ApiKey:      envOr("AERONAUTES_SAIO_KEY2", "testing2"),
		AuthVersion: 1,
		UserAgent:   "aeronautes-integration",
	}
	if err := conn2.Authenticate(ctx); err == nil {
		h.conn2 = conn2
	}

	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: authURL, Username: user, APIKey: key,
	})
	if err != nil {
		return nil, "", err
	}
	if err := session.Authenticate(ctx); err != nil {
		return nil, "", fmt.Errorf("adapter auth: %w", err)
	}
	h.Session = session

	if err := h.loadFixtures(ctx); err != nil {
		h.teardown(ctx)
		return nil, "", fmt.Errorf("load fixtures: %w", err)
	}
	return h, "", nil
}

// EncodingNames is the §14.3 hostile-name suite, round-tripped against
// real Swift byte-exact (mirrors the mock-path suite in swiftcontrol).
var EncodingNames = []string{
	"a b", "a+b", "a%b", "a?b", "a#b",
	"日本語", "foo//bar", "/foo", "foo/", "foo%2Fbar",
	"line\nbreak", "a/./b", "a/../b", "   ", "trailing space ",
}

func (h *Harness) put(ctx context.Context, container, name string, data []byte, contentType string, headers swift.Headers) error {
	_, err := h.conn.ObjectPut(ctx, container, name, bytes.NewReader(data), true, "", contentType, headers)
	return err
}

// loadFixtures creates this run's §58 canonical namespace.
func (h *Harness) loadFixtures(ctx context.Context) error {
	for _, c := range []string{"fixtures", "external-dlo-segments", "slo-parts", "photos_segments", "encoding"} {
		if err := h.conn.ContainerCreate(ctx, h.Container(c), nil); err != nil {
			return fmt.Errorf("create %s: %w", c, err)
		}
	}

	fixtures := h.Container("fixtures")
	plain := []struct {
		name, contentType string
		data              []byte
	}{
		{"normal/hello.txt", "text/plain", []byte("hello world!")},
		{"empty-object", "application/octet-stream", nil},
		{"folder-marker/", "application/directory", nil},
		{"implied/child.txt", "text/plain", bytes.Repeat([]byte("x"), 2048)},
		{"unicode/日本語.txt", "text/plain", []byte("konnichiwa")},
		{"weird/a+b.txt", "text/plain", bytes.Repeat([]byte("y"), 64)},
		{"weird/percent%name", "application/octet-stream", bytes.Repeat([]byte("z"), 96)},
		{"weird/repeated//slash", "application/octet-stream", bytes.Repeat([]byte("w"), 128)},
		// Folder-ish real object (§14.1) plus a child that implies the
		// same prefix: both rows must appear.
		{"fake/", "text/plain", []byte("junk-in-a-folder-suit")},
		{"fake/child.txt", "text/plain", []byte("child")},
	}
	for _, o := range plain {
		if err := h.put(ctx, fixtures, o.name, o.data, o.contentType, nil); err != nil {
			return fmt.Errorf("put %s: %w", o.name, err)
		}
	}

	// Metadata + scheduled expiry carriers.
	if err := h.put(ctx, fixtures, "tagged.txt", []byte("tagged"), "text/plain", swift.Headers{
		"X-Object-Meta-Shoot":    "wedding",
		"X-Object-Meta-LOCATION": "sydney", // case-insensitivity probe
	}); err != nil {
		return err
	}
	deleteAt := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	if err := h.put(ctx, fixtures, "expiring", []byte("soon gone"), "text/plain", swift.Headers{
		"X-Delete-At": deleteAt,
	}); err != nil {
		return err
	}

	// DLO: three 1000-byte segments and a manifest pointing at the prefix.
	segStore := h.Container("external-dlo-segments")
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("wedding-video/%016d", i)
		if err := h.put(ctx, segStore, name, bytes.Repeat([]byte{byte(i)}, 1000), "application/octet-stream", nil); err != nil {
			return err
		}
	}
	if err := h.put(ctx, fixtures, "external-dlo", nil, "video/mp4", swift.Headers{
		"X-Object-Manifest": segStore + "/wedding-video/",
	}); err != nil {
		return err
	}

	// SLO: two segments and a static manifest via ?multipart-manifest=put.
	sloParts := h.Container("slo-parts")
	type sloSeg struct {
		Path      string `json:"path"`
		ETag      string `json:"etag"`
		SizeBytes int    `json:"size_bytes"`
	}
	var manifest []sloSeg
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("backup/part-%06d", i)
		data := bytes.Repeat([]byte{0xA0 + byte(i)}, 500)
		if err := h.put(ctx, sloParts, name, data, "application/octet-stream", nil); err != nil {
			return err
		}
		sum := md5.Sum(data)
		manifest = append(manifest, sloSeg{
			Path:      "/" + sloParts + "/" + name,
			ETag:      hex.EncodeToString(sum[:]),
			SizeBytes: len(data),
		})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := h.rawPut(ctx, fixtures+"/external-slo?multipart-manifest=put", body, "application/x-tar"); err != nil {
		return fmt.Errorf("slo manifest: %w", err)
	}

	// The classification trap: named like a segment store, actually normal.
	photos := h.Container("photos_segments")
	for _, name := range []string{"sunrise.jpg", "sunset.jpg"} {
		if err := h.put(ctx, photos, name, []byte("jpegish"), "image/jpeg", nil); err != nil {
			return err
		}
	}

	// The hostile-name suite lives in its own container.
	encoding := h.Container("encoding")
	for _, name := range EncodingNames {
		if err := h.put(ctx, encoding, name, []byte("payload:"+name), "application/octet-stream", nil); err != nil {
			return fmt.Errorf("put hostile %q: %w", name, err)
		}
	}
	return nil
}

// rawPut PUTs against the storage URL directly — for query-parameter
// endpoints (SLO manifests) the wire library does not model.
func (h *Harness) rawPut(ctx context.Context, pathAndQuery string, body []byte, contentType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		h.conn.StorageUrl+"/"+pathAndQuery, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", h.conn.AuthToken)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var msg bytes.Buffer
		_, _ = msg.ReadFrom(resp.Body)
		return fmt.Errorf("PUT %s: %s: %s", pathAndQuery, resp.Status, msg.String())
	}
	return nil
}

// rawPut2 PUTs a container (empty body) or object on the second account.
func (h *Harness) rawPut2(ctx context.Context, pathAndQuery string, body []byte, contentType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		h.conn2.StorageUrl+"/"+pathAndQuery, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", h.conn2.AuthToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("PUT %s: %s", pathAndQuery, resp.Status)
	}
	return nil
}

// teardown removes every container this run created, best effort.
func (h *Harness) teardown(ctx context.Context) {
	h.teardownAccount(ctx, h.conn)
	if h.conn2 != nil {
		h.teardownAccount(ctx, h.conn2)
	}
}

func (h *Harness) teardownAccount(ctx context.Context, conn *swift.Connection) {
	names, err := conn.ContainerNamesAll(ctx, &swift.ContainersOpts{Prefix: h.Prefix})
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration teardown: list containers: %v\n", err)
		return
	}
	for _, container := range names {
		// JSON listing, never the plain-text one: a newline inside an
		// object name (the §14.3 suite has one) corrupts text listings.
		objects, err := conn.ObjectsAll(ctx, container, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "integration teardown: list %s: %v\n", container, err)
			continue
		}
		for _, object := range objects {
			if err := conn.ObjectDelete(ctx, container, object.Name); err != nil {
				fmt.Fprintf(os.Stderr, "integration teardown: delete %s/%s: %v\n", container, object.Name, err)
			}
		}
		if err := conn.ContainerDelete(ctx, container); err != nil {
			fmt.Fprintf(os.Stderr, "integration teardown: delete container %s: %v\n", container, err)
		}
	}
}
