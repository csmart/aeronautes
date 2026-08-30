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
	"strconv"
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
	client := &http.Client{Timeout: 30 * time.Second}
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
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return ContainerState{}, false, fmt.Errorf("oracle list %s: %w", name, err)
	}
	for _, row := range rows {
		os, err := o.snapshotObject(name, row.Name)
		if err != nil {
			return ContainerState{}, false, err
		}
		cs.Objects[row.Name] = os
	}
	return cs, true, nil
}

func (o *Oracle) snapshotObject(container, name string) (ObjectState, error) {
	// GET follows a symlink and assembles a large object — the LOGICAL
	// bytes, which is what an operator reads.
	resp, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "")
	if err != nil {
		return ObjectState{}, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ObjectState{}, fmt.Errorf("oracle get %s/%s: HTTP %d", container, name, resp.StatusCode)
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
	if link, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "symlink=get"); err == nil {
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
	}
	return os, nil
}

// segmentInventory lists a DLO prefix and digests each segment.
func (o *Oracle) segmentInventory(manifest string) ([]SegmentState, error) {
	container, prefix, found := strings.Cut(manifest, "/")
	if !found {
		return nil, fmt.Errorf("oracle: malformed manifest %q", manifest)
	}
	resp, err := o.do(http.MethodGet, "/"+container, "format=json&prefix="+prefix)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var rows []struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(body, &rows)
	var segs []SegmentState
	for _, row := range rows {
		seg, err := o.segment(container, row.Name)
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
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var entries []struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &entries)
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
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
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
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(r), 16)))
		}
	}
	return b.String()
}
