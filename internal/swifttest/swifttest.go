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
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
	// VersionsEnabled is modern object versioning (X-Versions-Enabled),
	// where versions live in the same container. This mock stores no such
	// versions; it can present the mode so a caller that must refuse it
	// can be shown refusing it.
	VersionsEnabled bool
	objects         map[string]Object
}

// defaultInfoJSON mirrors upstream master's shape: policies for the §20
// picker, versioned_writes flags per the §21 pin, slo/tempurl/bulk_delete.
const defaultInfoJSON = `{
	"swift": {"max_file_size": 5368709122, "version": "2.36.0",
		"max_meta_count": 90, "max_meta_name_length": 128,
		"max_meta_value_length": 256, "max_meta_overall_size": 4096,
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
	// SegmentAggregateOverride pins the aggregate entity tag a dynamic
	// large object reports, keyed by "container/name". The store derives
	// that value from a listing of the segment rows, so a listing that
	// has not caught up can report the previous aggregate after the
	// segments changed; this models exactly that and nothing else.
	SegmentAggregateOverride map[string]string
	// FailDeletes makes every object DELETE answer 500 — the exact-
	// accounting fixtures' knob.
	FailDeletes bool
	// FailDeletesAfter, when > 0, deletes succeed until this many have
	// landed, then every further delete fails, a bulk row and a single
	// object DELETE alike — an exact irreversible-subset-then-fail fixture.
	FailDeletesAfter int
	deletesDone      int
	// AfterRequest, when set, runs AFTER each request's response is
	// written — the barrier-test hook for planting a concurrent writer
	// at an exact protocol point.
	AfterRequest func(method, path string)
	// BeforeRequest, when set, runs after a request is logged and before it
	// is served, so a fixture can change the store or hold the request at a
	// protocol point the response hook cannot reach: a writer landing
	// between a listing and the HEAD that follows it, or between two
	// listings.
	BeforeRequest func(method, path string)
	// StaleListingRows names objects a container listing reports although
	// the store does not hold them, keyed by container. Container listings
	// lag deletes, so a row for an object that is gone is a thing a real
	// store does and a caller that trusts a listing has to be shown it.
	StaleListingRows map[string][]string
	// listingFailRemaining, when > 0, makes that many container GET
	// listings answer 500, so a caller that depends on a complete listing
	// can be shown what it does with an incomplete one. A container HEAD is
	// left alone: it is a different question and a caller may legitimately
	// ask it first.
	listingFailRemaining int
	// objectHEADFailRemaining, when > 0, makes that many object HEAD or
	// GET requests answer 500. Used to reproduce a verify-read failure
	// after a metadata POST (ADR 0058).
	objectHEADFailRemaining int
	// containerHEADFailRemaining, when > 0, makes that many container HEAD
	// requests answer 500: the fixture for a versioning re-read that fails
	// at a replacement's fixed point (ADR 0102 decision 2).
	containerHEADFailRemaining int
	// objectHEADFailSkip is how many object HEADs still pass untouched
	// before objectHEADFailRemaining starts to apply. A fixture that must
	// let one read through and fail the next cannot say so with a plain
	// count, and after ADR 0085 added a comparison read immediately before
	// a replacing publication, "the verify read" is no longer the first
	// HEAD a route takes.
	objectHEADFailSkip int
	// ManifestReadFault, when set, spoils the next ?multipart-manifest=get
	// response for that object ("container/name"): "partial" answers a
	// valid entry list with HTTP 206, "ranged" answers HTTP 200 with a
	// Content-Range, "trailing" answers a valid list followed by other
	// bytes, and "truncated" answers a list that stops mid-array. Each is a
	// response a lenient reader parses as a manifest, which is why a record
	// of what an operation is about to replace must not use one.
	ManifestReadFault map[string]string
	// dropNextObjectPOST closes the POST connection without a response,
	// so the caller sees a transport failure after the request left.
	dropNextObjectPOST bool
	// dropNextObjectDELETE applies the next object DELETE and then closes the
	// connection without a response, so the client has a request that
	// landed and no answer for it (ADR 0092's lost-reply case).
	dropNextObjectDELETE bool
	// dropPUTResponseFor is "container/name". The next PUT to that object
	// is APPLIED and then its connection is closed without a response, so
	// the caller cannot know what the store did with a write that landed.
	// This is the shape an unresolved publication actually has, and it is
	// the one thing a store cannot be asked to do on purpose.
	dropPUTResponseFor string
	// objectPOSTStatus, when non-zero, is consumed by the next object
	// POST as a store refusal without applying the write.
	objectPOSTStatus int
	// DisableBulkDelete makes ?bulk-delete answer 404 so the deletion
	// engine falls to the parallel single-DELETE path.
	DisableBulkDelete bool
	// ListingPageCap, when > 0, caps every container listing response at
	// this many rows however large a limit the caller asked for. Real Swift
	// may answer with fewer rows than the limit and still have more
	// (ADR 0021), and a caller that reads a short non-empty response as the
	// end of a container is wrong; this makes that store behaviour a fixture
	// rather than a hope.
	ListingPageCap int
	// BulkDeleteExtraErrorRow, when set, adds an error row for this
	// "/container/object" path to every bulk-delete reply. No request line
	// named it, so the reply is not wholly about the request that asked —
	// the shape a caller must not read as a disposition for its own names.
	BulkDeleteExtraErrorRow string
	requestLog              []string

	user  string
	key   string
	token string

	// IgnoresACLRemoval answers X-Remove-Container-Read and -Write without
	// clearing the ACL, as Ceph RGW's Swift API was measured to do (F36);
	// an empty X-Container-Read or -Write still clears it. Set before
	// serving.
	IgnoresACLRemoval bool
	// InfoJSON overrides the /info document (set before serving); ""
	// serves defaultInfoJSON.
	InfoJSON string
	// V3StorageEndpoint overrides the object-store endpoint URL the mock
	// Keystone v3 catalog advertises; "" derives it from the request host
	// (this same server). A test sets it to a public http URL to exercise
	// the client's storage-URL gate on the catalog's choice.
	V3StorageEndpoint string
	// V3Endpoints, when non-empty, is the full ordered set of object-store
	// public endpoints the mock Keystone v3 catalog advertises. A test
	// sets several regions (and reorders them between authentications) to
	// exercise region-pinned endpoint selection across a re-auth.
	V3Endpoints []V3Endpoint

	mu          sync.Mutex
	containers  map[string]*Container
	accountMeta map[string]string
	requests    int
	// authRequests and infoRequests count what the storage counters leave
	// out, so a test can prove a session was reused rather than reopened.
	authRequests int
	infoRequests int
}

// New builds an empty cluster accepting the given TempAuth credentials.
func New(user, key string) *Server {
	return &Server{
		user:        user,
		key:         key,
		token:       "AUTH_tk_swifttest_0000000000000001",
		containers:  map[string]*Container{},
		accountMeta: map[string]string{},
	}
}

// AddContainer creates (or replaces) a container.
func (s *Server) AddContainer(name, policy string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[name] = &Container{Policy: policy, objects: map[string]Object{}}
}

// PutObject stores an object, creating its container if needed.
// SetManifestReadFault arms ManifestReadFault for one object while the
// server is running, under the same lock the handler reads it with, so a
// case can spoil a read that happens LATER in a run rather than the first
// one of the run.
func (s *Server) SetManifestReadFault(container, name, fault string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ManifestReadFault == nil {
		s.ManifestReadFault = map[string]string{}
	}
	s.ManifestReadFault[container+"/"+name] = fault
}

// EnableVersionArchive presents a container as using one of the legacy
// versioning modes, naming the archive that keeps what is replaced. That
// archive is what makes removing a replaced object's segments the wrong
// thing to do, so a caller that reclaims has to be shown this mode.
func (s *Server) EnableVersionArchive(container, archive string, history bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	if !ok {
		return
	}
	if history {
		c.HistoryLocation, c.VersionsLocation = archive, ""
		return
	}
	c.VersionsLocation, c.HistoryLocation = archive, ""
}

// EnableModernVersioning presents a container as using modern object
// versioning, which is a mode some callers must refuse rather than act on.
func (s *Server) EnableModernVersioning(container string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.containers[container]; ok {
		c.VersionsEnabled = true
	}
}

// SetContainerMeta changes a container's metadata directly, modelling an
// external writer so a test can move the configuration between an operation's
// before and after reads.
func (s *Server) SetContainerMeta(container, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.containers[container]; ok {
		if c.Meta == nil {
			c.Meta = map[string]string{}
		}
		c.Meta[key] = value
	}
}

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

// AuthRequests reports how many authentication requests, TempAuth or
// Keystone v3, the server has answered.
func (s *Server) AuthRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authRequests
}

// InfoRequests reports how many /info requests the server has answered.
func (s *Server) InfoRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.infoRequests
}

// ExpireToken retires the token the server has issued: the next storage
// request carrying it is answered 401, and authenticating again returns
// the new one, as a cluster does when a token's lifetime ends.
func (s *Server) ExpireToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token += "-renewed"
}

// RevokeKey stops the server accepting the key it was built with, as an
// operator does by rotating it on the cluster; tokens already issued stay
// valid until ExpireToken.
func (s *Server) RevokeKey() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key += "-revoked"
}

func (s *Server) currentKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key
}

func (s *Server) currentToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
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
	if ep == "/auth/v1.0" || ep == "/v3/auth/tokens" {
		s.mu.Lock()
		s.authRequests++
		s.mu.Unlock()
	}
	if ep == "/auth/v1.0" {
		s.handleAuth(w, r)
		return
	}
	if ep == "/v3/auth/tokens" {
		s.handleV3Auth(w, r)
		return
	}
	if ep == "/info" {
		s.mu.Lock()
		s.infoRequests++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		info := s.InfoJSON
		if info == "" {
			info = defaultInfoJSON
		}
		_, _ = w.Write([]byte(info))
		return
	}
	if r.Header.Get("X-Auth-Token") != s.currentToken() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	s.requests++
	s.requestLog = append(s.requestLog, r.Method+" "+r.URL.Path+querySuffix(r))
	after, before := s.AfterRequest, s.BeforeRequest
	s.mu.Unlock()
	if before != nil {
		before(r.Method, r.URL.Path)
	}
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
		container, object, hasObject := strings.Cut(rest, "/")
		container, err := url.PathUnescape(container)
		if err != nil || strings.Contains(container, "/") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !hasObject {
			s.handleContainer(w, r, container)
			return
		}
		name, err := url.PathUnescape(object)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.handleObject(w, r, container, name)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Auth-User") != s.user || r.Header.Get("X-Auth-Key") != s.currentKey() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	w.Header().Set("X-Storage-Url", scheme+"://"+r.Host+accountPath)
	w.Header().Set("X-Auth-Token", s.currentToken())
	w.WriteHeader(http.StatusOK)
}

// V3Endpoint is one object-store public endpoint the mock Keystone v3
// catalog advertises, used by tests that need multiple regions.
type V3Endpoint struct {
	Region string
	URL    string
}

// handleV3Auth is a minimal Keystone v3 identity endpoint (POST
// /v3/auth/tokens): it checks the password-method credentials, then
// returns the subject token and a service catalog whose object-store
// public endpoint is this mock's own account, so a v3-authenticated
// client then talks to the same server. V3StorageEndpoint overrides that
// endpoint, which a test uses to exercise the client's storage-URL gate.
// The token is the same one TempAuth issues, so the ordinary X-Auth-Token
// check accepts the client's subsequent requests.
func (s *Server) handleV3Auth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Auth struct {
			Identity struct {
				Password struct {
					User struct {
						Name     string `json:"name"`
						Password string `json:"password"`
					} `json:"user"`
				} `json:"password"`
			} `json:"identity"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	user := body.Auth.Identity.Password.User
	if user.Name != s.user || user.Password != s.currentKey() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	storageURL := s.V3StorageEndpoint
	if storageURL == "" {
		storageURL = scheme + "://" + r.Host + accountPath
	}
	type endpoint struct {
		Interface string `json:"interface"`
		Region    string `json:"region"`
		URL       string `json:"url"`
	}
	type service struct {
		Type      string     `json:"type"`
		Endpoints []endpoint `json:"endpoints"`
	}
	var reply struct {
		Token struct {
			ExpiresAt string    `json:"expires_at"`
			Catalog   []service `json:"catalog"`
		} `json:"token"`
	}
	reply.Token.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var eps []endpoint
	if len(s.V3Endpoints) > 0 {
		for _, e := range s.V3Endpoints {
			eps = append(eps, endpoint{Interface: "public", Region: e.Region, URL: e.URL})
		}
	} else {
		eps = []endpoint{{Interface: "public", Region: "RegionOne", URL: storageURL}}
	}
	reply.Token.Catalog = []service{{Type: "object-store", Endpoints: eps}}
	payload, err := json.Marshal(reply)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Subject-Token", s.currentToken())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(payload)
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
	if r.Method == http.MethodPost {
		applyAccountHeaders(s, r)
		s.mu.Unlock()
		w.Header().Set("X-Trans-Id", "txswifttest-account-post")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	metaCopy := make(map[string]string, len(s.accountMeta))
	for k, v := range s.accountMeta {
		metaCopy[k] = v
	}
	s.mu.Unlock()

	h := w.Header()
	h.Set("X-Account-Container-Count", strconv.Itoa(containerCount))
	h.Set("X-Account-Object-Count", strconv.FormatInt(objects, 10))
	h.Set("X-Account-Bytes-Used", strconv.FormatInt(bytes, 10))
	h.Set("X-Trans-Id", "txswifttest-account")
	for k, v := range metaCopy {
		h.Set("X-Account-Meta-"+k, v)
	}

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
	if r.Method == http.MethodGet && s.listingFailRemaining > 0 {
		s.listingFailRemaining--
		s.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodHead && s.containerHEADFailRemaining > 0 {
		s.containerHEADFailRemaining--
		s.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
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
	// snapshot is a shallow copy, so snapshot.Meta still aliases c.Meta;
	// clone it under the lock because the header loop below ranges it after
	// the unlock, while another request's SetContainerMeta may write c.Meta.
	snapshot.Meta = maps.Clone(c.Meta)
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
	if snapshot.VersionsEnabled {
		h.Set("X-Versions-Enabled", "true")
	}
	if snapshot.HistoryLocation != "" {
		h.Set("X-History-Location", snapshot.HistoryLocation)
	}
	h.Set("X-Trans-Id", "txswifttest-container")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// A listing may report an object the store no longer holds, because
	// container updates lag deletes; a fixture asks for that by name.
	if stale := s.StaleListingRows[name]; len(stale) > 0 {
		objectNames = append(append([]string(nil), objectNames...), stale...)
		sort.Strings(objectNames)
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
	applyContainerHeaders(c, r, s.IgnoresACLRemoval)
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
	applyContainerHeaders(c, r, s.IgnoresACLRemoval)
	w.Header().Set("X-Trans-Id", "txswifttest-container-post")
	w.WriteHeader(http.StatusNoContent)
}

func applyContainerHeaders(c *Container, r *http.Request, ignoresACLRemoval bool) {
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
		case key == "X-Remove-Container-Read" && !ignoresACLRemoval:
			c.ReadACL = ""
		case key == "X-Remove-Container-Write" && !ignoresACLRemoval:
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

// applyAccountHeaders applies a POST's additive account-metadata delta to
// the stored account meta; the caller holds s.mu. It mirrors real Swift:
// X-Account-Meta-<k> sets (empty value deletes) and X-Remove-Account-Meta-<k>
// removes, so a TempURL key the delta never names simply survives.
func applyAccountHeaders(s *Server, r *http.Request) {
	if s.accountMeta == nil {
		s.accountMeta = map[string]string{}
	}
	for key, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		value := values[0]
		switch {
		case strings.HasPrefix(key, "X-Account-Meta-"):
			metaKey := strings.TrimPrefix(key, "X-Account-Meta-")
			if value == "" {
				delete(s.accountMeta, metaKey)
			} else {
				s.accountMeta[metaKey] = value
			}
		case strings.HasPrefix(key, "X-Remove-Account-Meta-"):
			delete(s.accountMeta, strings.TrimPrefix(key, "X-Remove-Account-Meta-"))
		}
	}
}

// putObject implements object PUT: create/replace with body, content
// type, user metadata, DLO manifest, and delete-at headers.
// metaLimits reads the user metadata limits the served /info document
// advertises, so enforcement always matches the advertisement, whether
// the default document or an override is in effect. A missing or
// unusable value disables that one check, which is what a service that
// does not advertise a limit does.
func (s *Server) metaLimits() (count, overall, nameLength, valueLength int) {
	info := s.InfoJSON
	if info == "" {
		info = defaultInfoJSON
	}
	var doc struct {
		Swift struct {
			MaxMetaCount       int `json:"max_meta_count"`
			MaxMetaOverallSize int `json:"max_meta_overall_size"`
			MaxMetaNameLength  int `json:"max_meta_name_length"`
			MaxMetaValueLength int `json:"max_meta_value_length"`
		} `json:"swift"`
	}
	_ = json.Unmarshal([]byte(info), &doc)
	return doc.Swift.MaxMetaCount, doc.Swift.MaxMetaOverallSize, doc.Swift.MaxMetaNameLength, doc.Swift.MaxMetaValueLength
}

// metaLimitExceeded reports whether a request's user metadata exceeds
// what this server advertises, which the real service enforces with
// 400 on every one of these dimensions.
func (s *Server) metaLimitExceeded(r *http.Request) bool {
	count, overall, nameLength, valueLength := s.metaLimits()
	entries, size := 0, 0
	for name, values := range r.Header {
		key, isMeta := strings.CutPrefix(http.CanonicalHeaderKey(name), "X-Object-Meta-")
		if !isMeta {
			continue
		}
		if nameLength > 0 && len(key) > nameLength {
			return true
		}
		for _, v := range values {
			if valueLength > 0 && len(v) > valueLength {
				return true
			}
			entries++
			size += len(key) + len(v)
		}
	}
	return (count > 0 && entries > count) || (overall > 0 && size > overall)
}

func (s *Server) putObject(w http.ResponseWriter, r *http.Request, container, name string) {
	if s.metaLimitExceeded(r) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
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
	if declared := strings.Trim(r.Header.Get("ETag"), `"`); declared != "" && !strings.EqualFold(declared, hex.EncodeToString(sum[:])) {
		// A declared entity tag the body does not match, as measured on
		// real Swift: 422, nothing stored at that name, and a prior
		// object at that name left as it was (ADR 0080).
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	if r.URL.Query().Get("multipart-manifest") == "put" {
		// A static large object is stored the way a deployed Swift
		// reports it afterwards: the listing and HEAD carry the logical
		// size and the manifest's entity tag, GET assembles the segments,
		// and ?multipart-manifest=get returns the manifest itself. The
		// manifest's own bytes are never the object's size (ADR 0070).
		o, status := s.staticLargeObject(r, body)
		if status != http.StatusCreated {
			w.WriteHeader(status)
			return
		}
		c.objects[name] = o
		if s.dropPUTResponse(w, container, name) {
			return
		}
		w.Header().Set("Etag", o.ETag)
		w.WriteHeader(http.StatusCreated)
		return
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
	if s.dropPUTResponse(w, container, name) {
		return
	}
	w.Header().Set("Etag", o.ETag)
	w.WriteHeader(http.StatusCreated)
}

// dropPUTResponse closes the connection after the write has been applied,
// when this object was armed for it. The caller holds s.mu.
func (s *Server) dropPUTResponse(w http.ResponseWriter, container, name string) bool {
	if s.dropPUTResponseFor != container+"/"+name {
		return false
	}
	s.dropPUTResponseFor = ""
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("swifttest: cannot drop object PUT response")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		panic("swifttest: cannot drop object PUT response: " + err.Error())
	}
	conn.Close()
	return true
}

// staticLargeObject validates an SLO manifest PUT against the segments
// this mock holds and returns the stored object, or the 400 a deployed
// Swift answers with for a manifest that names a missing segment or
// misstates its size or entity tag. The caller holds s.mu.
func (s *Server) staticLargeObject(r *http.Request, manifest []byte) (Object, int) {
	var entries []struct {
		Path      string `json:"path"`
		ETag      string `json:"etag"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(manifest, &entries); err != nil || len(entries) == 0 {
		return Object{}, http.StatusBadRequest
	}
	var data []byte
	var total int64
	var etags strings.Builder
	// A deployed Swift answers ?multipart-manifest=get with the stored
	// entries in listing form (name, hash, bytes, content_type,
	// last_modified), not with the path/etag/size_bytes form they were
	// PUT in; the oracle and the client both read that form.
	type storedEntry struct {
		Name         string `json:"name"`
		Hash         string `json:"hash"`
		Bytes        int64  `json:"bytes"`
		ContentType  string `json:"content_type"`
		LastModified string `json:"last_modified"`
	}
	stored := make([]storedEntry, 0, len(entries))
	for _, entry := range entries {
		path := strings.TrimPrefix(entry.Path, "/")
		slash := strings.Index(path, "/")
		if slash <= 0 || slash == len(path)-1 {
			return Object{}, http.StatusBadRequest
		}
		container, ok := s.containers[path[:slash]]
		if !ok {
			return Object{}, http.StatusBadRequest
		}
		segment, ok := container.objects[path[slash+1:]]
		if !ok || segment.SLO || segment.Manifest != "" {
			return Object{}, http.StatusBadRequest
		}
		if entry.ETag != "" && strings.Trim(entry.ETag, `"`) != segment.ETag {
			return Object{}, http.StatusBadRequest
		}
		if entry.SizeBytes != 0 && entry.SizeBytes != segment.Bytes {
			return Object{}, http.StatusBadRequest
		}
		data = append(data, segment.Data...)
		total += segment.Bytes
		etags.WriteString(segment.ETag)
		stored = append(stored, storedEntry{Name: "/" + path, Hash: segment.ETag, Bytes: segment.Bytes, ContentType: segment.ContentType, LastModified: "2026-08-28T10:00:00.000000"})
	}
	manifestJSON, err := json.Marshal(stored)
	if err != nil {
		return Object{}, http.StatusBadRequest
	}
	// Observed on the deployed service: a static large object's entity
	// tag is the quoted MD5 of its segments' concatenated entity tags,
	// and the listing repeats it as slo_etag.
	sum := md5.Sum([]byte(etags.String()))
	etag := hex.EncodeToString(sum[:])
	o := Object{
		Bytes:           total,
		ETag:            `"` + etag + `"`,
		SLOEtag:         etag,
		SLO:             true,
		SLOManifestJSON: string(manifestJSON),
		ContentType:     r.Header.Get("Content-Type"),
		Meta:            objectMetaFromHeaders(r),
		Data:            data,
	}
	if o.ContentType == "" {
		o.ContentType = "application/octet-stream"
	}
	return o, http.StatusCreated
}

// postObject implements fast-POST metadata update with real Swift's
// replace-all semantics: user metadata, the DLO X-Object-Manifest, and
// X-Delete-At are REPLACED by exactly what the POST carries — omitting
// one clears it (this is the trap the Phase 3 gate exists for).
// Content-Type is preserved unless supplied; SLO-ness is sysmeta and
// survives untouched.
func (s *Server) postObject(w http.ResponseWriter, r *http.Request, container, name string) {
	s.mu.Lock()
	drop := s.dropNextObjectPOST
	s.dropNextObjectPOST = false
	status := s.objectPOSTStatus
	s.objectPOSTStatus = 0
	s.mu.Unlock()
	if drop {
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
		panic("swifttest: cannot drop object POST")
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if s.metaLimitExceeded(r) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
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
	w.Header().Set("X-Trans-Id", "txswifttest-object-post")
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
	if s.BulkDeleteExtraErrorRow != "" {
		errorsList = append(errorsList, [2]string{s.BulkDeleteExtraErrorRow, "500 Internal Server Error"})
	}
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
	if s.FailDeletesAfter > 0 && s.deletesDone >= s.FailDeletesAfter {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	delete(c.objects, name)
	s.deletesDone++
	w.Header().Set("X-Trans-Id", "txswifttest-object-delete")
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
	if s.ListingPageCap > 0 && s.ListingPageCap < limit {
		limit = s.ListingPageCap
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
		if delimiter != "" {
			rest := name[len(prefix):]
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				subdir := prefix + rest[:idx+len(delimiter)]
				// Only a complete child-group marker skips that group.
				// The query's own object must not hide its children.
				if subdir == marker {
					continue
				}
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

// FailNextListings makes the next n container listings answer 500.
func (s *Server) FailNextListings(n int) {
	s.mu.Lock()
	s.listingFailRemaining = n
	s.mu.Unlock()
}

// FailNextContainerHEADs makes the next n container HEAD requests answer
// 500, so a route's re-read of a container's versioning fails while every
// other request proceeds.
func (s *Server) FailNextContainerHEADs(n int) {
	s.mu.Lock()
	s.containerHEADFailRemaining = n
	s.mu.Unlock()
}

// FailNextObjectHEADs fails the next n object HEAD/GET requests with 500.
func (s *Server) FailNextObjectHEADs(n int) {
	s.FailObjectHEADsAfter(0, n)
}

// FailObjectHEADsAfter lets the next skip object HEADs pass and then fails the
// following n, so a fixture can name WHICH read it is failing rather than only
// how many.
func (s *Server) FailObjectHEADsAfter(skip, n int) {
	s.mu.Lock()
	s.objectHEADFailSkip = skip
	s.objectHEADFailRemaining = n
	s.mu.Unlock()
}

// DropNextObjectPUTResponse makes the next PUT to that object apply its
// write and then close the connection without answering, so the caller
// learns nothing about a write that landed. Nothing about the request is
// refused: the point is an outcome that is unknown rather than failed.
func (s *Server) DropNextObjectPUTResponse(container, name string) {
	s.mu.Lock()
	s.dropPUTResponseFor = container + "/" + name
	s.mu.Unlock()
}

// DropNextObjectPOST closes the next object POST without a response.
func (s *Server) DropNextObjectPOST() {
	s.mu.Lock()
	s.dropNextObjectPOST = true
	s.mu.Unlock()
}

// DropNextObjectDELETE applies the next object DELETE and loses its reply.
func (s *Server) DropNextObjectDELETE() {
	s.mu.Lock()
	s.dropNextObjectDELETE = true
	s.mu.Unlock()
}

// RemoveObject deletes a stored object directly, as another writer would.
func (s *Server) RemoveObject(container, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.containers[container]; ok {
		delete(c.objects, name)
	}
}

// SetObjectPOSTStatus makes the next object POST return status without writing.
func (s *Server) SetObjectPOSTStatus(status int) {
	s.mu.Lock()
	s.objectPOSTStatus = status
	s.mu.Unlock()
}

// PatchObjectMeta adds or replaces one stored metadata key without a POST.
func (s *Server) PatchObjectMeta(container, name, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[container]
	if !ok {
		return
	}
	o, ok := c.objects[name]
	if !ok {
		return
	}
	meta := make(map[string]string, len(o.Meta)+1)
	for k, v := range o.Meta {
		meta[k] = v
	}
	meta[key] = value
	o.Meta = meta
	c.objects[name] = o
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, container, name string) {
	if r.Method == http.MethodHead || r.Method == http.MethodGet {
		s.mu.Lock()
		fail := s.objectHEADFailRemaining > 0 && s.objectHEADFailSkip == 0
		if s.objectHEADFailRemaining > 0 && s.objectHEADFailSkip > 0 {
			s.objectHEADFailSkip--
		}
		if fail {
			s.objectHEADFailRemaining--
		}
		s.mu.Unlock()
		if fail {
			w.Header().Set("X-Trans-Id", "txswifttest-object-head-fault")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
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
		s.mu.Lock()
		drop := s.dropNextObjectDELETE
		s.dropNextObjectDELETE = false
		s.mu.Unlock()
		if drop {
			s.deleteObject(&discardResponse{}, container, name)
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
			panic("swifttest: cannot drop object DELETE")
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
	// derivedETag is the DLO aggregate recomputed from the segment rows
	// this mock lists, matching the shape of the upstream computation;
	// empty leaves the stored value in place.
	var derivedETag string
	rawManifest := r.URL.Query().Get("multipart-manifest") == "get"
	if ok && (o.Manifest == "" || rawManifest) {
		logical = o.Data
	}
	if ok && o.Manifest != "" && !rawManifest {
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
				var concatenated strings.Builder
				for _, segName := range segNames {
					seg := sc.objects[segName]
					length += seg.Bytes
					logical = append(logical, seg.Data...)
					concatenated.WriteString(strings.Trim(seg.ETag, `"`))
				}
				// Observed on the deployed service: a dynamic large
				// object reports the MD5 of its segments' concatenated
				// entity tags, so a same-size substitution changes it. A
				// static large object keeps the value its manifest
				// recorded, which this mock does not model.
				if !o.SLO {
					derived := md5.Sum([]byte(concatenated.String()))
					derivedETag = hex.EncodeToString(derived[:])
				}
				if pinned, pinnedOK := s.SegmentAggregateOverride[container+"/"+name]; pinnedOK {
					derivedETag = pinned
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
		body, status := o.SLOManifestJSON, http.StatusOK
		s.mu.Lock()
		fault := s.ManifestReadFault[container+"/"+name]
		delete(s.ManifestReadFault, container+"/"+name)
		s.mu.Unlock()
		switch fault {
		case "partial":
			status = http.StatusPartialContent
		case "ranged":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
		case "trailing":
			body += "\n[{\"name\":\"/elsewhere/extra\"}]"
		case "truncated":
			if cut := strings.LastIndex(body, "}"); cut > 0 {
				body = body[:cut]
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return
	}

	h := w.Header()
	h.Set("Content-Type", o.ContentType)
	if derivedETag != "" {
		h.Set("Etag", derivedETag)
	} else {
		h.Set("Etag", o.ETag)
	}
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
	dstContainer, err := url.PathUnescape(trimmed[:slash])
	if err != nil || strings.Contains(dstContainer, "/") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	dstName := trimmed[slash+1:]
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

// discardResponse lets a handler apply its effect while the real connection is
// closed without an answer.
type discardResponse struct{ header http.Header }

func (d *discardResponse) Header() http.Header {
	if d.header == nil {
		d.header = http.Header{}
	}
	return d.header
}
func (d *discardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardResponse) WriteHeader(int)             {}
