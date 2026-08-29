// Package swifttest is an in-process mock Swift cluster for contract
// tests (§57.2) and headless development against realistic data. It
// serves TempAuth v1 auth, account/container/object listings with
// limit/marker/prefix/delimiter semantics, object HEADs with metadata
// and large-object headers, and /info — over a raw handler so hostile
// object names (foo//bar, percent%name) survive byte-exact.
//
// It is deliberately NOT a Swift implementation: just enough honest
// surface for the client to be tested against. The SAIO job (§57.3) is
// the real thing.
package swifttest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Object is one stored object's listable/HEADable state.
type Object struct {
	Bytes       int64
	ContentType string
	ETag        string
	// SLOEtag marks the listing row as SLO-listed (slo_etag field).
	SLOEtag string
	// Manifest sets X-Object-Manifest (DLO) on HEAD responses.
	Manifest string
	// SLO sets X-Static-Large-Object: True on HEAD responses.
	SLO bool
	// SLOManifestJSON is served verbatim for ?multipart-manifest=get.
	SLOManifestJSON string
	// Meta becomes X-Object-Meta-<key> headers on HEAD responses.
	Meta map[string]string
	// DeleteAt sets X-Delete-At (unix seconds) when non-zero.
	DeleteAt int64
}

// Container holds objects and container-level state.
type Container struct {
	Policy  string
	Meta    map[string]string
	objects map[string]Object
}

// Server is the mock cluster; it implements http.Handler.
type Server struct {
	user  string
	key   string
	token string

	mu         sync.Mutex
	containers map[string]*Container
	requests   int
}

// New builds an empty cluster accepting the given TempAuth credentials.
func New(user, key string) *Server {
	return &Server{
		user:       user,
		key:        key,
		token:      "AUTH_tk_swifttest_0000000000000001",
		containers: map[string]*Container{},
	}
}

// AddContainer creates (or replaces) a container.
func (s *Server) AddContainer(name, policy string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[name] = &Container{Policy: policy, objects: map[string]Object{}}
}

// PutObject stores an object, creating its container if needed.
func (s *Server) PutObject(container, name string, o Object) {
	if o.ContentType == "" {
		o.ContentType = "application/octet-stream"
	}
	if o.ETag == "" {
		o.ETag = "d41d8cd98f00b204e9800998ecf8427e"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	if !ok {
		c = &Container{Policy: "standard", objects: map[string]Object{}}
		s.containers[container] = c
	}
	c.objects[name] = o
}

// Requests reports how many storage requests the server has handled.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

const accountPath = "/v1/AUTH_test"

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ep := r.URL.EscapedPath()
	if ep == "/auth/v1.0" {
		s.handleAuth(w, r)
		return
	}
	if ep == "/info" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"swift":{"max_file_size":5368709122,"version":"2.36.0"},"slo":{"max_manifest_segments":1000},"bulk_delete":{"max_deletes_per_request":10000},"tempurl":{"methods":["GET","HEAD","PUT"]}}`))
		return
	}
	if r.Header.Get("X-Auth-Token") != s.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	s.requests++
	s.mu.Unlock()

	switch {
	case ep == accountPath:
		s.handleAccount(w, r)
	case strings.HasPrefix(ep, accountPath+"/"):
		rest := strings.TrimPrefix(ep, accountPath+"/")
		slash := strings.Index(rest, "/")
		if slash == -1 {
			s.handleContainer(w, r, rest)
			return
		}
		name, err := url.PathUnescape(rest[slash+1:])
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.handleObject(w, r, rest[:slash], name)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Auth-User") != s.user || r.Header.Get("X-Auth-Key") != s.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	w.Header().Set("X-Storage-Url", scheme+"://"+r.Host+accountPath)
	w.Header().Set("X-Auth-Token", s.token)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	names := make([]string, 0, len(s.containers))
	var objects, bytes int64
	type row struct {
		Name  string `json:"name"`
		Count int64  `json:"count"`
		Bytes int64  `json:"bytes"`
	}
	rows := make([]row, 0, len(s.containers))
	for name, c := range s.containers {
		names = append(names, name)
		var cb int64
		for _, o := range c.objects {
			cb += o.Bytes
		}
		objects += int64(len(c.objects))
		bytes += cb
	}
	sort.Strings(names)
	for _, name := range names {
		c := s.containers[name]
		var cb int64
		for _, o := range c.objects {
			cb += o.Bytes
		}
		rows = append(rows, row{Name: name, Count: int64(len(c.objects)), Bytes: cb})
	}
	containerCount := len(s.containers)
	s.mu.Unlock()

	h := w.Header()
	h.Set("X-Account-Container-Count", strconv.Itoa(containerCount))
	h.Set("X-Account-Object-Count", strconv.FormatInt(objects, 10))
	h.Set("X-Account-Bytes-Used", strconv.FormatInt(bytes, 10))
	h.Set("X-Trans-Id", "txswifttest-account")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	q := r.URL.Query()
	marker := q.Get("marker")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 10000
	}
	paged := rows[:0:0]
	for _, row := range rows {
		if len(paged) == limit {
			break
		}
		if row.Name > marker {
			paged = append(paged, row)
		}
	}
	h.Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(paged)
}

func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	c, ok := s.containers[name]
	if !ok {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		return
	}
	objectNames := make([]string, 0, len(c.objects))
	var bytes int64
	for n, o := range c.objects {
		objectNames = append(objectNames, n)
		bytes += o.Bytes
	}
	policy := c.Policy
	meta := c.Meta
	count := len(c.objects)
	s.mu.Unlock()
	sort.Strings(objectNames)

	h := w.Header()
	h.Set("X-Container-Object-Count", strconv.Itoa(count))
	h.Set("X-Container-Bytes-Used", strconv.FormatInt(bytes, 10))
	h.Set("X-Storage-Policy", policy)
	for k, v := range meta {
		h.Set("X-Container-Meta-"+k, v)
	}
	h.Set("X-Trans-Id", "txswifttest-container")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeObjectListing(w, r, name, objectNames)
}

type listingRow struct {
	Name         string `json:"name,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Hash         string `json:"hash,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	SLOEtag      string `json:"slo_etag,omitempty"`
	SubDir       string `json:"subdir,omitempty"`
}

// writeObjectListing emulates real Swift's delimiter grouping (verified
// via §57.3): every name below a child prefix — an object named exactly
// at the boundary included — folds into one subdir row, and a marker
// naming a subdir group skips the whole group. Only an object named
// exactly at the *query* prefix (no delimiter left in its remainder)
// lists as an object row.
func (s *Server) writeObjectListing(w http.ResponseWriter, r *http.Request, container string, names []string) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 10000
	}
	marker := q.Get("marker")
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")

	var rows []listingRow
	emittedSubdir := map[string]bool{}
	for _, name := range names {
		if len(rows) == limit {
			break
		}
		if name <= marker || !strings.HasPrefix(name, prefix) {
			continue
		}
		// Real Swift treats a marker that names a subdir group as "skip the
		// whole group": names inside it never re-emit the subdir row.
		if delimiter != "" && marker != "" && strings.HasSuffix(marker, delimiter) &&
			strings.HasPrefix(name, marker) {
			continue
		}
		if delimiter != "" {
			rest := name[len(prefix):]
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				subdir := prefix + rest[:idx+len(delimiter)]
				if !emittedSubdir[subdir] {
					emittedSubdir[subdir] = true
					rows = append(rows, listingRow{SubDir: subdir})
				}
				continue
			}
		}
		s.mu.Lock()
		o := s.containers[container].objects[name]
		s.mu.Unlock()
		rows = append(rows, listingRow{
			Name:         name,
			Bytes:        o.Bytes,
			Hash:         o.ETag,
			ContentType:  o.ContentType,
			LastModified: "2026-08-28T10:00:00.000000",
			SLOEtag:      o.SLOEtag,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, container, name string) {
	s.mu.Lock()
	c, ok := s.containers[container]
	var o Object
	if ok {
		o, ok = c.objects[name]
	}
	length := o.Bytes
	if ok && o.Manifest != "" {
		// A DLO manifest HEAD reports the assembled logical size (§20):
		// the summed bytes of everything under container/prefix right now.
		if slash := strings.Index(o.Manifest, "/"); slash > 0 {
			segContainer, segPrefix := o.Manifest[:slash], o.Manifest[slash+1:]
			if sc, scOK := s.containers[segContainer]; scOK {
				length = 0
				for segName, seg := range sc.objects {
					if strings.HasPrefix(segName, segPrefix) {
						length += seg.Bytes
					}
				}
			}
		}
	}
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if r.URL.Query().Get("multipart-manifest") == "get" && o.SLOManifestJSON != "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(o.SLOManifestJSON))
		return
	}

	h := w.Header()
	h.Set("Content-Type", o.ContentType)
	h.Set("Etag", o.ETag)
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("Last-Modified", "Fri, 28 Aug 2026 10:00:00 GMT")
	if o.Manifest != "" {
		h.Set("X-Object-Manifest", o.Manifest)
	}
	if o.SLO {
		h.Set("X-Static-Large-Object", "True")
	}
	for k, v := range o.Meta {
		h.Set("X-Object-Meta-"+k, v)
	}
	if o.DeleteAt != 0 {
		h.Set("X-Delete-At", strconv.FormatInt(o.DeleteAt, 10))
	}
	h.Set("X-Trans-Id", "txswifttest-object")
	w.WriteHeader(http.StatusOK)
}
