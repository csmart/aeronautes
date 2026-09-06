// Package oracle is the test war room's INDEPENDENT witness (§65.10): a
// raw-HTTP Swift reader that never imports internal/swiftcontrol, so a
// parsing bug in the product cannot validate itself in the assertion.
// For the containers it is asked to snapshot it records the object
// identity it MODELS — content digest, content/meta headers, large-object
// segment inventory, symlink target — and diffs two snapshots so a test
// can assert an exact delta over that modelled identity.
//
// It is a SCOPE-LIMITED witness, not a whole-account/complete-state one.
// Deliberately out of scope today (tracked gaps): account-level config,
// archived object versions, storage-policy/CORS/staticweb/TempURL header
// families, and local-filesystem / jobs / audit / recovery state. A
// symlink's identity is its target pointer (it is not followed for
// content); a dangling or cross-account link that fails a logical GET is
// not yet representable. Ranged/nested SLO entry forms are refused, not
// modelled. Callers get exactness over what is modelled — no more.
package oracle

import (
	"crypto/sha256"
	"encoding/base64"
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
	// Decoding here would erase stored bytes and encoding metadata from
	// the witness. The product and other tests keep their own policy.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	// Never auto-follow redirects: a redirect could resend the token/key
	// to another host or silently rewrite the request (codex war-room
	// H-06). The oracle inspects exactly what it asked for.
	client := &http.Client{
		Transport:     transport,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	connected := false
	defer func() {
		if !connected {
			client.CloseIdleConnections()
		}
	}()
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
	connected = true
	return &Oracle{client: client, storageURL: storageURL, token: token}, nil
}

// Each oracle owns its transport, so fixture teardown must release its
// idle connections after the final snapshot or residue check.
func (o *Oracle) Close() { o.client.CloseIdleConnections() }

// ObjectState is the MODELLED identity of one object (the fields below),
// not its complete Swift identity — see the package doc for what is out
// of scope.
type ObjectState struct {
	Name               string
	Class              string // plain | dlo | slo | symlink
	Size               int64  // content-coded GET body length, after large-object assembly
	Digest             string // SHA-256 of those bytes, without content decoding
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
	Name            string
	Size            int64
	Digest          string
	ContentEncoding string
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

// AccountSnapshot is the modelled state of the containers a test asked
// for (not the whole account — see the package doc for what is out of
// scope).
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

// ListObjects returns a container's object names via marker pagination,
// WITHOUT any logical GET — so a caller (e.g. test cleanup) can enumerate
// a container whose manifests are already broken (codex war-room R-04).
func (o *Oracle) ListObjects(container string) ([]string, error) {
	return o.listPaged("/"+container, "", nil)
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

func readComplete(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	// A valid-looking subset must never stand in for a whole object or
	// inventory. Reject its headers before waiting for more body data.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if len(resp.Header.Values("Content-Range")) != 0 {
		return nil, fmt.Errorf("partial representation: Content-Range present")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return body, nil
}

func (o *Oracle) snapshotContainer(name string) (ContainerState, bool, error) {
	// First page also carries the container's headers and 404 signal.
	resp, err := o.do(http.MethodGet, "/"+name, "format=json")
	if err != nil {
		return ContainerState{}, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return ContainerState{}, false, nil
	}
	body, err := readComplete(resp)
	if err != nil {
		return ContainerState{}, false, fmt.Errorf("oracle list %s: %w", name, err)
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
			// The caller already read and status-checked this body.
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
			body, err = readComplete(resp)
			if err != nil {
				return nil, fmt.Errorf("list %s page %d: %w", path, page, err)
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
		// Every name MUST be strictly greater than the last one seen —
		// within the page and across the page boundary. This detects
		// duplicates, out-of-order rows, and a non-advancing marker that
		// would otherwise loop forever; all are fail-closed (codex F-02).
		prev := marker
		for _, row := range rows {
			if row.Name <= prev {
				return nil, fmt.Errorf("list %s: names not strictly increasing (%q after %q) — refusing", path, row.Name, prev)
			}
			prev = row.Name
			names = append(names, row.Name)
		}
		marker = prev
	}
}

func (o *Oracle) snapshotObject(container, name string) (ObjectState, error) {
	// Large-object assembly belongs to Swift; decoding its content coding
	// here would hide a change to the representation being witnessed.
	resp, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "")
	if err != nil {
		return ObjectState{}, err
	}
	body, err := readComplete(resp)
	if err != nil {
		return ObjectState{}, fmt.Errorf("oracle get %s/%s: %w", container, name, err)
	}
	sum := sha256.Sum256(body)
	os := ObjectState{
		Name:               name,
		Class:              "plain",
		Size:               int64(len(body)),
		Digest:             hex.EncodeToString(sum[:]),
		ContentType:        resp.Header.Get("Content-Type"),
		ContentEncoding:    strings.Join(resp.Header.Values("Content-Encoding"), ", "),
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
	raw, err := readComplete(resp)
	if err != nil {
		return "", nil, fmt.Errorf("oracle SLO manifest %s/%s: %w", container, name, err)
	}
	var entries []struct {
		Name   string `json:"name"`
		Data   string `json:"data"`    // inline base64 data segment
		Range  string `json:"range"`   // ranged reference into a segment
		SubSLO bool   `json:"sub_slo"` // nested SLO manifest
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", nil, fmt.Errorf("oracle SLO manifest %s/%s: decode: %w", container, name, err)
	}
	var segs []SegmentState
	for i, e := range entries {
		// Every entry must be witnessed. Forms the oracle cannot yet
		// digest exactly are REFUSED, never silently omitted, so a
		// manifest it can't fully account for fails closed (codex F-02).
		// Full ranged/nested modeling is deferred (R-05).
		switch {
		case e.SubSLO:
			return "", nil, fmt.Errorf("oracle SLO %s/%s entry %d: nested sub_slo not modeled — refusing to under-witness", container, name, i)
		case e.Range != "":
			return "", nil, fmt.Errorf("oracle SLO %s/%s entry %d: ranged reference %q not modeled — refusing", container, name, i, e.Range)
		case e.Data != "":
			decoded, err := base64.StdEncoding.DecodeString(e.Data)
			if err != nil {
				return "", nil, fmt.Errorf("oracle SLO %s/%s entry %d: inline data decode: %w", container, name, i, err)
			}
			sum := sha256.Sum256(decoded)
			segs = append(segs, SegmentState{Name: fmt.Sprintf("inline:%d", i), Size: int64(len(decoded)), Digest: hex.EncodeToString(sum[:])})
		default:
			path := strings.TrimPrefix(e.Name, "/")
			c, n, found := strings.Cut(path, "/")
			if !found {
				return "", nil, fmt.Errorf("oracle SLO %s/%s entry %d: unparseable segment name %q — refusing to omit", container, name, i, e.Name)
			}
			seg, err := o.segment(c, n)
			if err != nil {
				return "", nil, err
			}
			segs = append(segs, seg)
		}
	}
	return string(raw), segs, nil
}

func (o *Oracle) segment(container, name string) (SegmentState, error) {
	resp, err := o.do(http.MethodGet, "/"+container+"/"+Escape(name), "")
	if err != nil {
		return SegmentState{}, err
	}
	body, err := readComplete(resp)
	if err != nil {
		return SegmentState{}, fmt.Errorf("oracle segment %s/%s: %w", container, name, err)
	}
	sum := sha256.Sum256(body)
	return SegmentState{
		Name: container + "/" + name, Size: int64(len(body)), Digest: hex.EncodeToString(sum[:]),
		ContentEncoding: strings.Join(resp.Header.Values("Content-Encoding"), ", "),
	}, nil
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
