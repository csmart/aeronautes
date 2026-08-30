// Package oracle is the test war room's INDEPENDENT witness (§65.10): a
// raw-HTTP Swift reader that never imports internal/swiftcontrol, so a
// parsing bug in the product cannot validate itself in the assertion.
// It snapshots the full identity of an account's containers and objects
// — content digests, headers, large-object inventories, symlink targets
// — and diffs two snapshots so a test can assert an exact delta AND that
// nothing else moved.
package oracle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Oracle reads one Swift account over raw HTTP.
type Oracle struct {
	client     *http.Client
	storageURL string
	token      string
}

// Connect authenticates against a v1 auth endpoint and returns a reader
// bound to the resulting account. No product code is involved.
func Connect(authURL, user, key string) (*Oracle, error) {
	// Never auto-follow redirects: a redirect could resend the token/key
	// to another host or silently rewrite the request (codex war-room
	// H-06). The oracle inspects exactly what it asked for.
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodGet, authURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-User", user)
	req.Header.Set("X-Auth-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("oracle auth %s: HTTP %d", authURL, resp.StatusCode)
	}
	storageURL := resp.Header.Get("X-Storage-Url")
	token := resp.Header.Get("X-Auth-Token")
	if storageURL == "" || token == "" {
		return nil, fmt.Errorf("oracle auth: missing storage URL or token")
	}
	return &Oracle{client: client, storageURL: storageURL, token: token}, nil
}

// ObjectState is the complete identity of one object.
type ObjectState struct {
	Name               string
	Class              string // plain | dlo | slo | symlink
	Size               int64  // logical Content-Length
	Digest             string // SHA-256 of the logical GET body
	ContentType        string
	ContentEncoding    string
	ContentDisposition string
	DeleteAt           string
	Meta               map[string]string // X-Object-Meta-*
	Manifest           string            // DLO X-Object-Manifest
	Segments           []SegmentState    // DLO/SLO backing inventory
	RawSLOManifest     string            // SLO ?multipart-manifest=get body
	SymlinkTarget      string
	SymlinkAccount     string
}

// SegmentState is one backing segment's identity.
type SegmentState struct {
	Name   string
	Size   int64
	Digest string
}

// ContainerState is a container's configuration and object set.
type ContainerState struct {
	Name       string
	ReadACL    string
	WriteACL   string
	QuotaBytes string
	QuotaCount string
	Versioning string // X-Versions-Location | X-History-Location | X-Versions-Enabled value
	Meta       map[string]string
	Objects    map[string]ObjectState
}

// AccountSnapshot is the whole visible state of an account, scoped to
// the containers a test asked for.
type AccountSnapshot struct {
	Containers map[string]ContainerState
}

func (o *Oracle) do(method, path string, query string) (*http.Response, error) {
	url := o.storageURL + path
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", o.token)
	return o.client.Do(req)
}

// ListContainers returns the account's container names beginning with
// prefix (empty = all), following marker pagination. The harness uses it
// to witness the WHOLE destination namespace after a copy/mirror,
// including any sidecar/segment container the engine creates — naming
// only the primary container would leave a sidecar leak invisible to the
// negative-space invariant (grok war-room P1).
func (o *Oracle) ListContainers(prefix string) ([]string, error) {
	query := ""
	if prefix != "" {
		query = "prefix=" + Escape(prefix)
	}
	return o.listPaged("", query, nil)
}

// SnapshotContainers captures the exact state of the named containers.
// A missing container is recorded as absent (not in the map).
func (o *Oracle) SnapshotContainers(names ...string) (*AccountSnapshot, error) {
	snap := &AccountSnapshot{Containers: map[string]ContainerState{}}
	for _, name := range names {
		cs, present, err := o.snapshotContainer(name)
		if err != nil {
			return nil, err
		}
		if present {
			snap.Containers[name] = cs
		}
	}
	return snap, nil
}

func (o *Oracle) snapshotContainer(name string) (ContainerState, bool, error) {
	// First page also carries the container's headers and 404 signal.
	resp, err := o.do(http.MethodGet, "/"+name, "format=json")
	if err != nil {
		return ContainerState{}, false, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ContainerState{}, false, nil
	}
	if resp.StatusCode/100 != 2 {
		return ContainerState{}, false, fmt.Errorf("oracle list %s: HTTP %d", name, resp.StatusCode)
	}
	cs := ContainerState{
		Name:       name,
		ReadACL:    resp.Header.Get("X-Container-Read"),
		WriteACL:   resp.Header.Get("X-Container-Write"),
		QuotaBytes: resp.Header.Get("X-Container-Meta-Quota-Bytes"),
		QuotaCount: resp.Header.Get("X-Container-Meta-Quota-Count"),
		Meta:       metaFrom(resp.Header, "X-Container-Meta-"),
		Objects:    map[string]ObjectState{},
	}
	switch {
	case resp.Header.Get("X-Versions-Enabled") != "":
		cs.Versioning = "enabled:" + resp.Header.Get("X-Versions-Enabled")
	case resp.Header.Get("X-History-Location") != "":
		cs.Versioning = "history:" + resp.Header.Get("X-History-Location")
	case resp.Header.Get("X-Versions-Location") != "":
		cs.Versioning = "versions:" + resp.Header.Get("X-Versions-Location")
	}
	names, err := o.listPaged("/"+name, "", body)
	if err != nil {
		return ContainerState{}, false, fmt.Errorf("oracle list %s: %w", name, err)
	}
	for _, objName := range names {
		os, err := o.snapshotObject(name, objName)
		if err != nil {
			return ContainerState{}, false, err
		}
		cs.Objects[objName] = os
	}
	return cs, true, nil
}

// listPaged returns every object name under path, following Swift's
// marker pagination to the end. The default page is 10k; a single GET
// silently truncates a large container and would let AssertNoCollateral
// miss a survivor or a stray in the unwitnessed tail (grok war-room).
// firstPage, if non-nil, is the already-read body of page one (so the
// caller's header GET is not repeated); query is an extra clause such as
// "prefix=segs/".
func (o *Oracle) listPaged(path, query string, firstPage []byte) ([]string, error) {
	var names []string
	marker := ""
	for page := 0; ; page++ {
		var body []byte
		if page == 0 && firstPage != nil {
			body = firstPage
		} else {
			q := "format=json"
			if query != "" {
				q += "&" + query
			}
			if marker != "" {
				q += "&marker=" + Escape(marker)
			}
			resp, err := o.do(http.MethodGet, path, q)
			if err != nil {
				return nil, err
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				return nil, fmt.Errorf("list %s page %d: HTTP %d", path, page, resp.StatusCode)
			}
		}
		var rows []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return names, nil
		}
		for _, row := range rows {
			names = append(names, row.Name)
		}
		// The marker MUST strictly advance; a provider that returns a
		// non-advancing page would otherwise loop forever (codex war-room
		// H-06). Fail closed instead.
		next := rows[len(rows)-1].Name
		if next <= marker {
			return nil, fmt.Errorf("list %s: marker did not advance (%q -> %q) — refusing to loop", path, marker, next)
		}
		marker = next
	}
}

func (o *Oracle) snapshotObject(container, name string) (ObjectState, error) {
	// GET follows a symlink and assembles a large object — the LOGICAL
	// bytes, which is what an operator reads.
	resp, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "")
	if err != nil {
		return ObjectState{}, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ObjectState{}, fmt.Errorf("oracle get %s/%s: HTTP %d", container, name, resp.StatusCode)
	}
	if readErr != nil {
		return ObjectState{}, fmt.Errorf("oracle get %s/%s: read: %w", container, name, readErr)
	}
	sum := sha256.Sum256(body)
	os := ObjectState{
		Name:               name,
		Class:              "plain",
		Size:               int64(len(body)),
		Digest:             hex.EncodeToString(sum[:]),
		ContentType:        resp.Header.Get("Content-Type"),
		ContentEncoding:    resp.Header.Get("Content-Encoding"),
		ContentDisposition: resp.Header.Get("Content-Disposition"),
		DeleteAt:           resp.Header.Get("X-Delete-At"),
		Meta:               metaFrom(resp.Header, "X-Object-Meta-"),
	}
	if manifest := resp.Header.Get("X-Object-Manifest"); manifest != "" {
		os.Class = "dlo"
		os.Manifest = manifest
		segs, err := o.segmentInventory(manifest)
		if err != nil {
			return ObjectState{}, err
		}
		os.Segments = segs
	}
	if isTrue(resp.Header.Get("X-Static-Large-Object")) {
		os.Class = "slo"
		raw, segs, err := o.sloInventory(container, name)
		if err != nil {
			return ObjectState{}, err
		}
		os.RawSLOManifest = raw
		os.Segments = segs
	}
	// A symlink is addressed with ?symlink=get for its own headers. Its
	// IDENTITY is the target pointer + the link's own metadata, NOT the
	// followed content — the target object is witnessed separately, so
	// coupling the two would double-report a target change.
	link, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "symlink=get")
	if err != nil {
		return ObjectState{}, err
	}
	if link.StatusCode/100 != 2 {
		io.Copy(io.Discard, link.Body)
		link.Body.Close()
		return ObjectState{}, fmt.Errorf("oracle symlink probe %s/%s: HTTP %d", container, name, link.StatusCode)
	}
	if t := link.Header.Get("X-Symlink-Target"); t != "" {
		os.Class = "symlink"
		os.SymlinkTarget = t
		os.SymlinkAccount = link.Header.Get("X-Symlink-Target-Account")
		os.Size = 0
		os.Digest = "symlink->" + t
		os.Segments = nil
		os.Meta = metaFrom(link.Header, "X-Object-Meta-")
	}
	io.Copy(io.Discard, link.Body)
	link.Body.Close()
	return os, nil
}

// segmentInventory lists a DLO prefix and digests each segment.
func (o *Oracle) segmentInventory(manifest string) ([]SegmentState, error) {
	container, prefix, found := strings.Cut(manifest, "/")
	if !found {
		return nil, fmt.Errorf("oracle: malformed manifest %q", manifest)
	}
	names, err := o.listPaged("/"+container, "prefix="+Escape(prefix), nil)
	if err != nil {
		return nil, err
	}
	var segs []SegmentState
	for _, name := range names {
		seg, err := o.segment(container, name)
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Name < segs[j].Name })
	return segs, nil
}

// sloInventory returns the raw manifest and digests each referenced leaf.
func (o *Oracle) sloInventory(container, name string) (string, []SegmentState, error) {
	resp, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "multipart-manifest=get")
	if err != nil {
		return "", nil, err
	}
	raw, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	// Fail closed: an error body must not be parsed/hashed as a manifest
	// (codex war-room H-06).
	if resp.StatusCode/100 != 2 {
		return "", nil, fmt.Errorf("oracle SLO manifest %s/%s: HTTP %d", container, name, resp.StatusCode)
	}
	if readErr != nil {
		return "", nil, fmt.Errorf("oracle SLO manifest %s/%s: read: %w", container, name, readErr)
	}
	var entries []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", nil, fmt.Errorf("oracle SLO manifest %s/%s: decode: %w", container, name, err)
	}
	var segs []SegmentState
	for _, e := range entries {
		path := strings.TrimPrefix(e.Name, "/")
		c, n, found := strings.Cut(path, "/")
		if !found {
			continue
		}
		seg, err := o.segment(c, n)
		if err != nil {
			return "", nil, err
		}
		segs = append(segs, seg)
	}
	return string(raw), segs, nil
}

func (o *Oracle) segment(container, name string) (SegmentState, error) {
	resp, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "")
	if err != nil {
		return SegmentState{}, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	// Fail closed: never digest an error body as segment content (codex
	// war-room H-06).
	if resp.StatusCode/100 != 2 {
		return SegmentState{}, fmt.Errorf("oracle segment %s/%s: HTTP %d", container, name, resp.StatusCode)
	}
	if readErr != nil {
		return SegmentState{}, fmt.Errorf("oracle segment %s/%s: read: %w", container, name, readErr)
	}
	sum := sha256.Sum256(body)
	return SegmentState{Name: container + "/" + name, Size: int64(len(body)), Digest: hex.EncodeToString(sum[:])}, nil
}

func metaFrom(h http.Header, prefix string) map[string]string {
	m := map[string]string{}
	for k, v := range h {
		if strings.HasPrefix(k, prefix) && len(v) > 0 {
			m[strings.TrimPrefix(k, prefix)] = v[0]
		}
	}
	return m
}

func isTrue(v string) bool { return strings.EqualFold(v, "true") }

// Escape percent-encodes an object name per path segment, keeping "/".
func Escape(name string) string {
	segments := strings.Split(name, "/")
	for i, seg := range segments {
		segments[i] = pathEscape(seg)
	}
	return strings.Join(segments, "/")
}

func pathEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if r == '-' || r == '_' || r == '.' || r == '~' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteByte(r)
		} else {
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}
