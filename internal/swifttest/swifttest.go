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
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
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
	// SymlinkTarget makes the object a Swift symlink ("container/name");
	// plain HEAD/GET follow it (with Content-Location), ?symlink=get
	// addresses the link itself.
	SymlinkTarget string
	// SymlinkTargetAccount is the optional cross-account half.
	SymlinkTargetAccount string
	// DeleteAt sets X-Delete-At (unix seconds) when non-zero.
	DeleteAt int64
	// Data is the stored body, served on GET (fixtures may omit it and
	// set Bytes/ETag directly — HEAD-only fidelity).
	Data []byte
}

// Container holds objects and container-level state.
type Container struct {
	Policy  string
	Meta    map[string]string
	ReadACL string
	// WriteACL is the X-Container-Write value.
	WriteACL string
	// VersionsLocation / HistoryLocation are the §21 container-versioning
	// headers (at most one is meaningful on a real server).
	VersionsLocation string
	HistoryLocation  string
	objects          map[string]Object
}

// defaultInfoJSON mirrors upstream master's shape: policies for the §20
// picker, versioned_writes flags per the §21 pin, slo/tempurl/bulk_delete.
const defaultInfoJSON = `{
	"swift": {"max_file_size": 5368709122, "version": "2.36.0",
		"policies": [{"name": "standard", "default": true, "aliases": "standard"},
			{"name": "gold", "aliases": "gold, fast"}, {"name": "ec42", "aliases": "ec42"}]},
	"slo": {"max_manifest_segments": 1000, "min_segment_size": 1},
	"bulk_delete": {"max_deletes_per_request": 10000},
	"tempurl": {"methods": ["GET", "HEAD", "PUT"]},
	"versioned_writes": {"allowed_flags": ["x-versions-location", "x-history-location"]},
	"object_versioning": {}
}`

// Server is the mock cluster; it implements http.Handler.
type Server struct {
	// FailDeletes makes every object DELETE answer 500 — the exact-
	// accounting fixtures' knob.
	FailDeletes bool
	// FailDeletesAfter, when > 0, deletes succeed until this many have
	// landed, then every further delete fails — an exact
	// irreversible-subset-then-fail fixture within a single bulk batch.
	FailDeletesAfter int
	deletesDone      int
	// AfterRequest, when set, runs AFTER each request's response is
	// written — the barrier-test hook for planting a concurrent writer
	// at an exact protocol point.
	AfterRequest func(method, path string)
	// DisableBulkDelete makes ?bulk-delete answer 404 so the deletion
	// engine falls to the parallel single-DELETE path.
	DisableBulkDelete bool
	requestLog        []string

	user  string
	key   string
	token string

	// InfoJSON overrides the /info document (set before serving); ""
	// serves defaultInfoJSON.
	InfoJSON string

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

// RequestLog returns every storage request as "METHOD path?query" —
// the wire-truth oracle for tests asserting what was (never) sent.
func (s *Server) RequestLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requestLog...)
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
		info := s.InfoJSON
		if info == "" {
			info = defaultInfoJSON
		}
		_, _ = w.Write([]byte(info))
		return
	}
	if r.Header.Get("X-Auth-Token") != s.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	s.requests++
	s.requestLog = append(s.requestLog, r.Method+" "+r.URL.Path+querySuffix(r))
	after := s.AfterRequest
	s.mu.Unlock()
	if after != nil {
		defer after(r.Method, r.URL.Path)
	}

	switch {
	case ep == accountPath:
		if r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete") {
			if s.DisableBulkDelete {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			s.handleBulkDelete(w, r)
			return
		}
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
	prefix := q.Get("prefix")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 10000
	}
	paged := make([]row, 0)
	for _, row := range rows {
		if len(paged) == limit {
			break
		}
		if row.Name > marker && strings.HasPrefix(row.Name, prefix) {
			paged = append(paged, row)
		}
	}
	h.Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(paged)
}

func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		s.createContainer(w, r, name)
		return
	case http.MethodPost:
		s.updateContainer(w, r, name)
		return
	case http.MethodDelete:
		s.deleteContainer(w, name)
		return
	}

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
	snapshot := *c
	count := len(c.objects)
	s.mu.Unlock()
	sort.Strings(objectNames)

	h := w.Header()
	h.Set("X-Container-Object-Count", strconv.Itoa(count))
	h.Set("X-Container-Bytes-Used", strconv.FormatInt(bytes, 10))
	h.Set("X-Storage-Policy", snapshot.Policy)
	for k, v := range snapshot.Meta {
		h.Set("X-Container-Meta-"+k, v)
	}
	if snapshot.ReadACL != "" {
		h.Set("X-Container-Read", snapshot.ReadACL)
	}
	if snapshot.WriteACL != "" {
		h.Set("X-Container-Write", snapshot.WriteACL)
	}
	if snapshot.VersionsLocation != "" {
		h.Set("X-Versions-Location", snapshot.VersionsLocation)
	}
	if snapshot.HistoryLocation != "" {
		h.Set("X-History-Location", snapshot.HistoryLocation)
	}
	h.Set("X-Trans-Id", "txswifttest-container")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeObjectListing(w, r, name, objectNames)
}

// createContainer implements container PUT: create with storage policy
// and initial metadata; a PUT on an existing container is accepted and
// its policy is NOT changed (policy is create-time only, §20).
func (s *Server) createContainer(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, exists := s.containers[name]
	if !exists {
		policy := r.Header.Get("X-Storage-Policy")
		if policy == "" {
			policy = "standard"
		}
		c = &Container{Policy: policy, objects: map[string]Object{}}
		s.containers[name] = c
	}
	applyContainerHeaders(c, r)
	if exists {
		w.WriteHeader(http.StatusAccepted)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

// updateContainer implements container POST: per-key ADDITIVE metadata
// (unlike object POST's replace-all), X-Remove-* deletions, ACL and
// versioning header updates.
func (s *Server) updateContainer(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[name]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	applyContainerHeaders(c, r)
	w.WriteHeader(http.StatusNoContent)
}

func applyContainerHeaders(c *Container, r *http.Request) {
	for key, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		value := values[0]
		switch {
		case strings.HasPrefix(key, "X-Container-Meta-"):
			metaKey := strings.TrimPrefix(key, "X-Container-Meta-")
			if c.Meta == nil {
				c.Meta = map[string]string{}
			}
			if value == "" {
				delete(c.Meta, metaKey)
			} else {
				c.Meta[metaKey] = value
			}
		case strings.HasPrefix(key, "X-Remove-Container-Meta-"):
			delete(c.Meta, strings.TrimPrefix(key, "X-Remove-Container-Meta-"))
		case key == "X-Container-Read":
			c.ReadACL = value
		case key == "X-Container-Write":
			c.WriteACL = value
		case key == "X-Remove-Container-Read":
			c.ReadACL = ""
		case key == "X-Remove-Container-Write":
			c.WriteACL = ""
		case key == "X-Versions-Location":
			c.VersionsLocation = value
			c.HistoryLocation = ""
		case key == "X-History-Location":
			c.HistoryLocation = value
			c.VersionsLocation = ""
		case key == "X-Remove-Versions-Location":
			c.VersionsLocation = ""
		case key == "X-Remove-History-Location":
			c.HistoryLocation = ""
		}
	}
}

// putObject implements object PUT: create/replace with body, content
// type, user metadata, DLO manifest, and delete-at headers.
func (s *Server) putObject(w http.ResponseWriter, r *http.Request, container, name string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sum := md5.Sum(body)

	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Header.Get("If-None-Match") == "*" {
		// Conditional create, as measured on real Swift: 412 when the
		// name exists, first writer wins.
		if _, exists := c.objects[name]; exists {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	}
	o := Object{
		Bytes:                int64(len(body)),
		ETag:                 hex.EncodeToString(sum[:]),
		ContentType:          r.Header.Get("Content-Type"),
		Manifest:             r.Header.Get("X-Object-Manifest"),
		SymlinkTarget:        symlinkTargetHeader(r),
		SymlinkTargetAccount: r.Header.Get("X-Symlink-Target-Account"),
		Meta:                 objectMetaFromHeaders(r),
		Data:                 body,
	}
	if o.ContentType == "" {
		o.ContentType = "application/octet-stream"
	}
	if deleteAt := r.Header.Get("X-Delete-At"); deleteAt != "" {
		if epoch, err := strconv.ParseInt(deleteAt, 10, 64); err == nil {
			o.DeleteAt = epoch
		}
	}
	c.objects[name] = o
	w.Header().Set("Etag", o.ETag)
	w.WriteHeader(http.StatusCreated)
}

// postObject implements fast-POST metadata update with real Swift's
// replace-all semantics: user metadata, the DLO X-Object-Manifest, and
// X-Delete-At are REPLACED by exactly what the POST carries — omitting
// one clears it (this is the trap the Phase 3 gate exists for).
// Content-Type is preserved unless supplied; SLO-ness is sysmeta and
// survives untouched.
func (s *Server) postObject(w http.ResponseWriter, r *http.Request, container, name string) {
	s.mu.Lock()
	if c0, ok0 := s.containers[container]; ok0 {
		if o0, ok1 := c0.objects[name]; ok1 && o0.SymlinkTarget != "" && r.URL.Query().Get("symlink") != "get" {
			// Measured on real Swift: the POST applies to the LINK's
			// metadata and the response is 307 toward the target.
			o0.Meta = objectMetaFromHeaders(r)
			c0.objects[name] = o0
			s.mu.Unlock()
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
	}
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	var o Object
	if ok {
		o, ok = c.objects[name]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	o.Meta = objectMetaFromHeaders(r)
	o.Manifest = r.Header.Get("X-Object-Manifest")
	o.DeleteAt = 0
	if deleteAt := r.Header.Get("X-Delete-At"); deleteAt != "" {
		if epoch, err := strconv.ParseInt(deleteAt, 10, 64); err == nil {
			o.DeleteAt = epoch
		}
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		o.ContentType = contentType
	}
	c.objects[name] = o
	w.WriteHeader(http.StatusAccepted)
}

func objectMetaFromHeaders(r *http.Request) map[string]string {
	var meta map[string]string
	for key, values := range r.Header {
		if strings.HasPrefix(key, "X-Object-Meta-") && len(values) > 0 {
			if meta == nil {
				meta = map[string]string{}
			}
			meta[strings.TrimPrefix(key, "X-Object-Meta-")] = values[0]
		}
	}
	return meta
}

// handleBulkDelete implements the bulk middleware's account POST
// ?bulk-delete: a text/plain body of URL-encoded /container/object
// lines, JSON accounting back. Deletion is manifest-only — exactly the
// named object rows go, segments are never followed.
func (s *Server) handleBulkDelete(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var deleted, notFound int64
	errorsList := [][2]string{}
	s.mu.Lock()
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		trimmed := strings.TrimPrefix(line, "/")
		slash := strings.Index(trimmed, "/")
		if slash <= 0 {
			errorsList = append(errorsList, [2]string{line, "400 Bad Request"})
			continue
		}
		container, _ := url.PathUnescape(trimmed[:slash])
		name, unescapeErr := url.PathUnescape(trimmed[slash+1:])
		if unescapeErr != nil {
			errorsList = append(errorsList, [2]string{line, "400 Bad Request"})
			continue
		}
		c, ok := s.containers[container]
		if ok {
			_, ok = c.objects[name]
		}
		if !ok {
			notFound++
			continue
		}
		if s.FailDeletes || (s.FailDeletesAfter > 0 && s.deletesDone >= s.FailDeletesAfter) {
			// Per-item failure, as the real middleware reports: the
			// object stays and the caller sees an exact error row.
			errorsList = append(errorsList, [2]string{line, "500 Internal Server Error"})
			continue
		}
		delete(c.objects, name)
		deleted++
		s.deletesDone++
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Number Deleted":   deleted,
		"Number Not Found": notFound,
		"Response Status":  "200 OK",
		"Response Body":    "",
		"Errors":           errorsList,
	})
}

// deleteObject implements object DELETE.
func (s *Server) deleteObject(w http.ResponseWriter, container, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	if ok {
		_, ok = c.objects[name]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	delete(c.objects, name)
	w.WriteHeader(http.StatusNoContent)
}

// deleteContainer implements container DELETE: 409 while not empty.
func (s *Server) deleteContainer(w http.ResponseWriter, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[name]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if len(c.objects) > 0 {
		w.WriteHeader(http.StatusConflict)
		return
	}
	delete(s.containers, name)
	w.WriteHeader(http.StatusNoContent)
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

	rows := []listingRow{}
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
	switch r.Method {
	case http.MethodPut:
		s.putObject(w, r, container, name)
		return
	case http.MethodPost:
		s.postObject(w, r, container, name)
		return
	case http.MethodDelete:
		if s.FailDeletes {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("multipart-manifest") == "delete" {
			s.deleteSLOWithSegments(w, container, name)
			return
		}
		s.deleteObject(w, container, name)
		return
	case "COPY":
		s.copyObject(w, r, container, name)
		return
	}

	s.mu.Lock()
	c, ok := s.containers[container]
	var o Object
	if ok {
		o, ok = c.objects[name]
	}
	followed := ""
	if ok && o.SymlinkTarget != "" && r.URL.Query().Get("symlink") != "get" && o.SymlinkTargetAccount == "" {
		// Follow the link (same-account only in the mock): the response
		// is the TARGET's, plus Content-Location naming the link chase.
		if slash := strings.Index(o.SymlinkTarget, "/"); slash > 0 {
			tc, tcOK := s.containers[o.SymlinkTarget[:slash]]
			if tcOK {
				if target, tOK := tc.objects[o.SymlinkTarget[slash+1:]]; tOK {
					followed = "/v1/AUTH_test/" + o.SymlinkTarget
					o, ok = target, true
				} else {
					s.mu.Unlock()
					w.WriteHeader(http.StatusNotFound)
					return
				}
			}
		}
	}
	length := o.Bytes
	var logical []byte
	if ok && o.Manifest == "" {
		logical = o.Data
	}
	if ok && o.Manifest != "" {
		// A DLO manifest HEAD reports the assembled logical size (§20):
		// the summed bytes of everything under container/prefix right now.
		if slash := strings.Index(o.Manifest, "/"); slash > 0 {
			segContainer, segPrefix := o.Manifest[:slash], o.Manifest[slash+1:]
			if sc, scOK := s.containers[segContainer]; scOK {
				var segNames []string
				for segName := range sc.objects {
					if strings.HasPrefix(segName, segPrefix) {
						segNames = append(segNames, segName)
					}
				}
				sort.Strings(segNames)
				length = 0
				logical = nil
				for _, segName := range segNames {
					seg := sc.objects[segName]
					length += seg.Bytes
					logical = append(logical, seg.Data...)
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
	if followed != "" {
		h.Set("Content-Location", followed)
	}
	if o.SymlinkTarget != "" && (r.URL.Query().Get("symlink") == "get" || o.SymlinkTargetAccount != "") {
		h.Set("X-Symlink-Target", o.SymlinkTarget)
		if o.SymlinkTargetAccount != "" {
			h.Set("X-Symlink-Target-Account", o.SymlinkTargetAccount)
		}
	}
	for k, v := range o.Meta {
		h.Set("X-Object-Meta-"+k, v)
	}
	if o.DeleteAt != 0 {
		h.Set("X-Delete-At", strconv.FormatInt(o.DeleteAt, 10))
	}
	h.Set("X-Trans-Id", "txswifttest-object")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(logical)
	}
}

// deleteSLOWithSegments implements ?multipart-manifest=delete: the
// manifest AND every referenced segment die — the §34 hazard the
// callers must gate.
func (s *Server) deleteSLOWithSegments(w http.ResponseWriter, container, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	var o Object
	if ok {
		o, ok = c.objects[name]
	}
	if !ok || !o.SLO {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var entries []map[string]any
	_ = json.Unmarshal([]byte(o.SLOManifestJSON), &entries)
	var deleted, notFound int64
	for _, entry := range entries {
		path, _ := entry["name"].(string)
		if path == "" {
			path, _ = entry["path"].(string)
		}
		trimmed := strings.TrimPrefix(path, "/")
		slash := strings.Index(trimmed, "/")
		if slash <= 0 {
			continue
		}
		segContainer, segName := trimmed[:slash], trimmed[slash+1:]
		if sc, scOK := s.containers[segContainer]; scOK {
			if _, exists := sc.objects[segName]; exists {
				delete(sc.objects, segName)
				deleted++
				continue
			}
		}
		notFound++
	}
	delete(c.objects, name)
	deleted++
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Number Deleted": deleted, "Number Not Found": notFound,
		"Response Status": "200 OK", "Errors": [][2]string{},
	})
}

// copyObject implements server-side COPY for PLAIN sources only: the
// stage-and-commit path never copies a manifest (a manifest COPY on
// real Swift assembles the logical object — a different operation the
// executor must never reach for). Source metadata is carried; headers
// on the request are applied on top (real COPY semantics).
func (s *Server) copyObject(w http.ResponseWriter, r *http.Request, container, name string) {
	destination := r.Header.Get("Destination")
	slash := strings.Index(strings.TrimPrefix(destination, "/"), "/")
	if destination == "" || slash <= 0 {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	trimmed := strings.TrimPrefix(destination, "/")
	dstContainer, dstName := trimmed[:slash], trimmed[slash+1:]
	if unescaped, err := url.PathUnescape(dstName); err == nil {
		dstName = unescaped
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	var src Object
	if ok {
		src, ok = c.objects[name]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if src.Manifest != "" || src.SLO {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	dc, ok := s.containers[dstContainer]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	dst := src
	dst.Meta = map[string]string{}
	for k, v := range src.Meta {
		dst.Meta[k] = v
	}
	for k, v := range objectMetaFromHeaders(r) {
		dst.Meta[k] = v
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		dst.ContentType = contentType
	}
	if deleteAt := r.Header.Get("X-Delete-At"); deleteAt != "" {
		if epoch, err := strconv.ParseInt(deleteAt, 10, 64); err == nil {
			dst.DeleteAt = epoch
		}
	}
	dc.objects[dstName] = dst
	w.Header().Set("X-Copied-From", container+"/"+name)
	w.WriteHeader(http.StatusCreated)
}

// symlinkTargetHeader mimics real Swift's measured behavior: a
// leading-slash X-Symlink-Target is IGNORED (the PUT stores a plain
// object), only the bare container/object form creates a link.
func symlinkTargetHeader(r *http.Request) string {
	v := r.Header.Get("X-Symlink-Target")
	if v == "" || strings.HasPrefix(v, "/") {
		return ""
	}
	return v
}

func querySuffix(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}
