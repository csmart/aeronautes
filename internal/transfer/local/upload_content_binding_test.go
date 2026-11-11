package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// alteringProxy rewrites the body of one object PUT to different bytes of
// the same length, which is what a network that corrupts in flight, or a
// store that stored something else, would present. Length is preserved
// exactly, so nothing but the content can catch it (ADR 0080).
func alteringProxy(t *testing.T, mock http.Handler, escapedPath string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.EscapedPath() == escapedPath {
			body, err := io.ReadAll(r.Body)
			if err == nil && len(body) > 0 {
				altered := append([]byte(nil), body...)
				altered[0] ^= 0xff
				r.Body = io.NopCloser(bytes.NewReader(altered))
				r.ContentLength = int64(len(altered))
			}
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func bindingSession(t *testing.T, server *httptest.Server) *swiftcontrol.Session {
	t.Helper()
	session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func uploadOne(t *testing.T, session *swiftcontrol.Session, container, prefix, path string) (*UploadPreflight, UploadItem) {
	t.Helper()
	preflight, err := PreflightUpload(t.Context(), session, container, prefix, []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 {
		t.Fatalf("upload fixture was not admitted: %+v %v", preflight, err)
	}
	return preflight, preflight.Items[0]
}

// ADR 0080 decision 1: the native route declares the digest it intends,
// so bytes altered in flight are refused by the store and nothing exists
// at the name. Before this decision the same alteration published and
// verified, because only length was compared.
func TestNativeUploadRefusesAlteredBytes(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	server := alteringProxy(t, mock, "/v1/AUTH_test/media/docs/object")
	session := bindingSession(t, server)
	path := writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
	_, item := uploadOne(t, session, "media", "docs/", path)
	item.Route = "native"

	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))

	if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved {
		t.Fatalf("altered native upload was not refused: %+v", result.Observation)
	}
	if result.Diagnostic.Code != transfercopy.CodeUnverified {
		t.Errorf("refusal code = %v, want the content verdict", result.Diagnostic.Code)
	}
	if _, err := session.ObjectDetail(t.Context(), "media", "docs/object"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("a refused native upload left an object at the name: %v", err)
	}
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Errorf("a refused native upload changed the account: %s", diff)
	}
}

// The control: unaltered bytes publish and verify exactly as before.
func TestNativeUploadPublishesUnalteredBytes(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	body := "the bytes this job planned to send"
	path := writeFile(t, t.TempDir(), "object", body)
	_, item := uploadOne(t, session, "media", "docs/", path)
	item.Route = "native"

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))
	if result.Result != transfercopy.ResultCopied || result.Effect != transfercopy.EffectReportedApplied {
		t.Fatalf("unaltered native upload did not publish: %+v", result.Observation)
	}
	live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil || live.ContentLength != int64(len(body)) {
		t.Fatalf("published object = %+v %v", live, err)
	}
}

// ADR 0080 decisions 3, 4 and 6: the staged commit compares what the
// engine staged with the file this job planned to send, and publishes
// nothing when they disagree. Before this decision each of these cases
// published, because only lengths were compared.
// stageStatic stores a static large object the way the mock's own
// manifest PUT stores one: the assembled bytes, and an entity tag that
// is the digest of its segments' entity tags rather than of its content.
// That tag can never equal the file's own digest, which is why a static
// manifest has to be compared by reading it (ADR 0080 decision 3).
func stageStatic(t *testing.T, mock *swifttest.Server, container, name string, segments ...string) {
	t.Helper()
	var data []byte
	var etags, manifest strings.Builder
	manifest.WriteString("[")
	for i, segment := range segments {
		data = append(data, segment...)
		etags.WriteString(md5hex(segment))
		if i > 0 {
			manifest.WriteString(",")
		}
		seed(mock, "media_segments", fmt.Sprintf("upload/%04d", i+1), segment)
		fmt.Fprintf(&manifest, `{"name":"/media_segments/upload/%04d","hash":%q,"bytes":%d}`, i+1, md5hex(segment), len(segment))
	}
	manifest.WriteString("]")
	tag := md5hex(etags.String())
	mock.PutObject(container, name, swifttest.Object{
		Bytes: int64(len(data)), Data: data, SLO: true, ETag: `"` + tag + `"`,
		SLOEtag: tag, SLOManifestJSON: manifest.String(), ContentType: "application/octet-stream",
	})
}

func TestStagedCommitRefusesStagedContentThatIsNotTheSource(t *testing.T) {
	const body = "0123456789abcdef"
	for _, tc := range []struct {
		name  string
		code  transfercopy.Code
		stage func(t *testing.T, mock *swifttest.Server, stagedName string)
	}{
		{"plain altered to the same length", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media", stagedName, "0123456789ABCDEF")
		}},
		{"segment altered to the same length", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "01234567")
			seed(mock, "media_segments", "upload/0002", "89ABCDEF")
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
		{"segment missing", transfercopy.CodeIncomplete, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "01234567")
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
		{"segment duplicated", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "01234567")
			seed(mock, "media_segments", "upload/0002", "01234567")
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
		{"segments reordered", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "89abcdef")
			seed(mock, "media_segments", "upload/0002", "01234567")
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
		{"a foreign run of the same total length", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "0123456789abcde!")
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
		{"a static manifest assembled from other bytes", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			stageStatic(t, mock, "media", stagedName, "01234567", "89ABCDEF")
		}},
		// ADR 0042's retained counterexample, which is why listing
		// evidence cannot stand in for the read: the row still describes
		// the bytes it replaced.
		{"a segment whose row still describes the bytes it replaced", transfercopy.CodeUnverified, func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "01234567")
			mock.PutObject("media_segments", "upload/0002", swifttest.Object{
				Bytes: 8, ETag: md5hex("89abcdef"), Data: []byte("89ABCDEF"),
			})
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			mock.AddContainer("media_segments", "standard")
			server := httptest.NewServer(mock)
			t.Cleanup(server.Close)
			session := bindingSession(t, server)
			path := writeFile(t, t.TempDir(), "object", body)
			_, item := uploadOne(t, session, "media", "docs/", path)

			stagedName := ".aeronautes/stage/binding/batch/object"
			tc.stage(t, mock, stagedName)
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)

			result := commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
			if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved {
				t.Fatalf("a staged commit of content that is not the source was not refused: %+v", result.Observation)
			}
			if result.label != "stage-content" || result.Diagnostic.Code != tc.code {
				t.Errorf("refusal does not name the staging boundary and its reason: %q %+v, wanted %s",
					result.label, result.Diagnostic, tc.code)
			}
			if result.failure == nil || !strings.Contains(result.failure.Error(), "does not match the file this job planned to send") {
				t.Errorf("refusal does not say what disagreed: %v", result.failure)
			}
			if _, err := session.ObjectDetail(t.Context(), "media", "docs/object"); !swiftcontrol.IsNotFound(err) {
				t.Errorf("a refused staged commit published at the live name: %v", err)
			}
			// The staged bytes are left where they are: this refusal
			// publishes nothing and reclaims nothing.
			if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
				t.Errorf("a refused staged commit changed the account: %s", diff)
			}
		})
	}
}

// The controls: staged content that IS the source publishes, for a plain
// staged object and for a segmented one.
func TestStagedCommitPublishesStagedContentThatIsTheSource(t *testing.T) {
	const body = "0123456789abcdef"
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, mock *swifttest.Server, stagedName string)
	}{
		{"plain", func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media", stagedName, body)
		}},
		{"segmented", func(t *testing.T, mock *swifttest.Server, stagedName string) {
			seed(mock, "media_segments", "upload/0001", "01234567")
			seed(mock, "media_segments", "upload/0002", "89abcdef")
			mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
		}},
		// The control that a class test on the dynamic manifest alone
		// would fail: this content IS the source, and its entity tag is
		// a digest of segment tags, so comparing tags would refuse it.
		{"static manifest", func(t *testing.T, mock *swifttest.Server, stagedName string) {
			stageStatic(t, mock, "media", stagedName, "01234567", "89abcdef")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			mock.AddContainer("media_segments", "standard")
			server := httptest.NewServer(mock)
			t.Cleanup(server.Close)
			session := bindingSession(t, server)
			path := writeFile(t, t.TempDir(), "object", body)
			_, item := uploadOne(t, session, "media", "docs/", path)

			stagedName := ".aeronautes/stage/binding/batch/object"
			tc.stage(t, mock, stagedName)
			result := commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
			if result.Result != transfercopy.ResultCopied || result.Effect != transfercopy.EffectReportedApplied {
				t.Fatalf("a staged commit of the source's own content did not publish: %+v", result.Observation)
			}
			live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil || live.ContentLength != int64(len(body)) {
				t.Fatalf("published object = %+v %v", live, err)
			}
		})
	}
}

// mutatingSource is a held file whose bytes change in place between the
// pass that digests it and the pass that sends it. The swap happens on
// the rewind that follows a read, which is where a file being rewritten
// under an open descriptor would show itself.
type mutatingSource struct {
	passes [][]byte
	pass   int
	offset int
	read   bool
}

func (m *mutatingSource) current() []byte { return m.passes[min(m.pass, len(m.passes)-1)] }

func (m *mutatingSource) Read(p []byte) (int, error) {
	body := m.current()
	if m.offset >= len(body) {
		return 0, io.EOF
	}
	n := copy(p, body[m.offset:])
	m.offset += n
	m.read = true
	return n, nil
}

func (m *mutatingSource) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != io.SeekStart {
		return 0, errors.New("the fixture only rewinds")
	}
	if m.read {
		m.pass++
		m.read = false
	}
	m.offset = 0
	return 0, nil
}

func (m *mutatingSource) Close() error { return nil }

// ADR 0080 decision 2: what the store refuses is a held file whose bytes
// change in place between the digest and the body. This is the case an
// implementation that digested the bytes it had already sent could never
// fail.
func TestNativeUploadRefusesAHeldFileThatChangesInPlace(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	first := "the bytes the digest describes"
	path := writeFile(t, t.TempDir(), "object", first)
	_, item := uploadOne(t, session, "media", "docs/", path)
	item.Route = "native"

	held := &mutatingSource{passes: [][]byte{[]byte(first), []byte(strings.ToUpper(first))}}
	result := nativeUpload(t.Context(), session, "media", item, held, item.Replaces, nil)
	if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved {
		t.Fatalf("a held file that changed in place was not refused: %+v", result.Observation)
	}
	if _, err := session.ObjectDetail(t.Context(), "media", "docs/object"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("the refused upload left an object at the name: %v", err)
	}
}

// The control for that case: replacing the PATHNAME while the job holds
// the original open changes nothing. The descriptor is what the route
// sends, so the upload publishes the bytes the job opened. An
// implementation that re-opened the pathname for its digest would refuse
// this, and refusing it would be the defect.
func TestNativeUploadSendsTheHeldFileWhenItsPathnameIsReplaced(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	held := "the bytes this job opened, of a known length"
	dir := t.TempDir()
	path := writeFile(t, dir, "object", held)
	_, item := uploadOne(t, session, "media", "docs/", path)
	item.Route = "native"

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// The pathname now names a different file of the same length.
	replacement := strings.ToUpper(held)
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "other"), path); err != nil {
		t.Fatal(err)
	}

	result := nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))
	if result.Result != transfercopy.ResultCopied || result.Effect != transfercopy.EffectReportedApplied {
		t.Fatalf("a replaced pathname refused the held file: %+v", result.Observation)
	}
	body, _, err := session.OpenObject(t.Context(), "media", "docs/object")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	published, err := io.ReadAll(body)
	if err != nil || string(published) != held {
		t.Fatalf("published %q, want the bytes the job opened", published)
	}
}

// md5hex is the hex MD5 of a string, for a fixture that must state an
// entity tag the stored bytes do not have.
func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The staged read admits only a complete response. A store that answers
// the staged GET with a partial body whose bytes happen to match the
// file would otherwise be read as the whole staged object: the reviewer's
// regression for a review finding, retained. The staged object is eight
// bytes and the file is four, so a prefix that matches is exactly the
// case a length check made after publication cannot prevent.
func TestStagedCommitRefusesAPartialStagedRead(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	stagedName := ".aeronautes/stage/binding/batch/object"
	seed(mock, "media_segments", "upload/0001", "GOOD")
	seed(mock, "media_segments", "upload/0002", "TAIL")
	mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
	var partialReads, livePuts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/AUTH_test/media/"+stagedName {
			partialReads.Add(1)
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 0-3/8")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "GOOD")
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/v1/AUTH_test/media/docs/object" {
			livePuts.Add(1)
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	item := UploadItem{Path: writeFile(t, t.TempDir(), "object", "GOOD"), Name: "docs/object", Size: 4}

	result := commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
	if partialReads.Load() != 1 {
		t.Fatalf("the partial-response fault was never exercised: %d", partialReads.Load())
	}
	if livePuts.Load() != 0 {
		t.Fatalf("an incomplete staged read authorised a publication: %d live writes", livePuts.Load())
	}
	if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved || result.label != "stage-content" {
		t.Fatalf("a partial staged read was not refused at the staging boundary: %+v %q", result.Observation, result.label)
	}
	if _, err := session.ObjectDetail(t.Context(), "media", item.Name); !swiftcontrol.IsNotFound(err) {
		t.Errorf("a refused staged commit published at the live name: %v", err)
	}
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Errorf("a refused staged commit changed the account: %s", diff)
	}
}

// The comparison consumes the stream to its end, so a staged body whose
// bytes match but which never terminates is refused rather than accepted
// at the last byte. This is the EOF half of the lifecycle the reviewer
// asked this route to drive; the Close half is joined
// by the same call, and the reviewer's own fixture for it addressed the
// earlier OpenObject seam, whose reader re-HEADs at Close. The complete
// content reader does not, so the reachable late failures on this path
// are an unterminated stream and a cancellation, retained here and below.
func TestStagedCommitRefusesAnUnterminatedStagedRead(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	stagedName := ".aeronautes/stage/binding/batch/object"
	seed(mock, "media_segments", "upload/0001", "bytes")
	mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
	var aborted, livePuts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/AUTH_test/media/"+stagedName {
			w.Header().Set("Transfer-Encoding", "chunked")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "bytes")
			w.(http.Flusher).Flush()
			aborted.Add(1)
			// Drop the connection without the terminating chunk: every
			// byte the comparison wanted has arrived, and the stream
			// still never ended.
			panic(http.ErrAbortHandler)
		}
		if r.Method == http.MethodPut && r.URL.Path == "/v1/AUTH_test/media/docs/object" {
			livePuts.Add(1)
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	item := UploadItem{Path: writeFile(t, t.TempDir(), "object", "bytes"), Name: "docs/object", Size: 5}

	result := commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
	if aborted.Load() != 1 {
		t.Fatalf("the unterminated-stream fault was never exercised: %d", aborted.Load())
	}
	if livePuts.Load() != 0 {
		t.Fatalf("a staged read that never ended authorised a publication: %d live writes", livePuts.Load())
	}
	if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved || result.label != "stage-content" {
		t.Fatalf("an unterminated staged read was not refused at the staging boundary: %+v %q", result.Observation, result.label)
	}
	if _, err := session.ObjectDetail(t.Context(), "media", item.Name); !swiftcontrol.IsNotFound(err) {
		t.Errorf("a refused staged commit published at the live name: %v", err)
	}
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Errorf("a refused staged commit changed the account: %s", diff)
	}
}

// Cancellation during the staged read is the other late failure the
// shared comparison joins into its verdict. Nothing is published, and
// the staged bytes stay where they are under ADR 0020.
func TestStagedCommitRefusesACancelledStagedRead(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	stagedName := ".aeronautes/stage/binding/batch/object"
	seed(mock, "media_segments", "upload/0001", "bytes")
	mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	var livePuts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/AUTH_test/media/"+stagedName {
			cancel()
		}
		if r.Method == http.MethodPut && r.URL.Path == "/v1/AUTH_test/media/docs/object" {
			livePuts.Add(1)
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	item := UploadItem{Path: writeFile(t, t.TempDir(), "object", "bytes"), Name: "docs/object", Size: 5}

	result := commitStagedUpload(ctx, session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
	if livePuts.Load() != 0 {
		t.Fatalf("a cancelled staged read authorised a publication: %d live writes", livePuts.Load())
	}
	if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved {
		t.Fatalf("a cancelled staged read was not refused: %+v", result.Observation)
	}
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Errorf("a cancelled staged commit changed the account: %s", diff)
	}
}

// strippingProxy alters the body of one object PUT to different bytes of the same
// length AND deletes the declaration that would have let the store refuse it.
// That is a middlebox, not a store: with the header gone the store has nothing to
// enforce and keeps whatever it was handed, so the publication happens and only a
// question asked afterwards can report it. alteringProxy above is the other half
// of the pair — it keeps the header, so the store refuses and nothing is
// published.
func strippingProxy(t *testing.T, mock http.Handler, escapedPath string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.EscapedPath() == escapedPath {
			body, err := io.ReadAll(r.Body)
			if err == nil && len(body) > 0 {
				altered := append([]byte(nil), body...)
				altered[0] ^= 0xff
				r.Body = io.NopCloser(bytes.NewReader(altered))
				r.ContentLength = int64(len(altered))
				r.Header.Del("ETag")
				r.Header.Del("Content-Md5")
			}
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// ADR 0045: a plain upload's live HEAD must report a plain object whose length
// AND entity tag equal the local file's length and digest. Length alone was all
// this asked before, so a path that removed the declared tag left the altered
// bytes at the live name reported as copied and verified — the publication was
// not prevented and nothing afterwards disagreed with it.
//
// Both routes are held, because both publish a plain object from the same file:
// the native PUT, and the plain staged commit, which ADR 0045 says in terms no
// staged comparison substitutes for.
func TestPlainUploadReportsAStrippedDeclaration(t *testing.T) {
	const body = "the bytes this job planned to send"
	for _, tc := range []struct {
		name   string
		staged bool
	}{
		{name: "native"},
		{name: "staged_plain_commit", staged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			server := strippingProxy(t, mock, "/v1/AUTH_test/media/docs/object")
			session := bindingSession(t, server)
			path := writeFile(t, t.TempDir(), "object", body)
			_, item := uploadOne(t, session, "media", "docs/", path)

			var result uploadResult
			if tc.staged {
				stagedName := ".aeronautes/stage/binding/batch/object"
				seed(mock, "media", stagedName, body)
				result = commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
			} else {
				item.Route = "native"
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				result = nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))
			}

			if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved {
				t.Fatalf("a stripped declaration was not reported: %+v", result.Observation)
			}
			if result.Diagnostic.Code != transfercopy.CodeUnverified {
				t.Errorf("code = %v, want the content verdict", result.Diagnostic.Code)
			}
			if result.verification == nil || result.verification.Result == transfercopy.ResultVerified {
				t.Errorf("verification = %+v, want anything but verified", result.verification)
			}
			// The bytes ARE at the live name: nothing prevented this, and the
			// effect is unresolved residue rather than a clean refusal. Saying so
			// is the whole of what this check buys.
			live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatalf("the fixture published nothing, so the case proves nothing: %v", err)
			}
			if live.ContentLength != int64(len(body)) {
				t.Errorf("live = %+v, want the altered body at the same length", live)
			}
			if strings.Trim(live.ETag, `"`) == md5hex(body) {
				t.Error("the fixture did not alter the published bytes")
			}
		})
	}
}

// The control for the pair above: with nothing stripped and nothing altered, both
// routes still publish and verify. A check that refused everything would pass the
// cases above and fail these.
func TestPlainUploadVerifiesWhatItPublished(t *testing.T) {
	const body = "the bytes this job planned to send"
	for _, tc := range []struct {
		name   string
		staged bool
	}{
		{name: "native"},
		{name: "staged_plain_commit", staged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			server := httptest.NewServer(mock)
			t.Cleanup(server.Close)
			session := bindingSession(t, server)
			path := writeFile(t, t.TempDir(), "object", body)
			_, item := uploadOne(t, session, "media", "docs/", path)

			var result uploadResult
			if tc.staged {
				stagedName := ".aeronautes/stage/binding/batch/object"
				seed(mock, "media", stagedName, body)
				result = commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
			} else {
				item.Route = "native"
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				result = nativeUpload(t.Context(), session, "media", item, file, item.Replaces, comparesAgainstLive(t, session, "media", item))
			}
			if result.Result != transfercopy.ResultCopied || result.Effect != transfercopy.EffectReportedApplied {
				t.Fatalf("an unaltered upload did not publish: %+v", result.Observation)
			}
			if result.verification == nil || result.verification.Result != transfercopy.ResultVerified {
				t.Errorf("verification = %+v, want verified", result.verification)
			}
			live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil || strings.Trim(live.ETag, `"`) != md5hex(body) {
				t.Fatalf("published object = %+v %v", live, err)
			}
		})
	}
}

// A manifest commit is NOT asked for the file's digest. Its live entity tag is an
// aggregate over segment tags, which can never equal the content's own MD5, so a
// pass that compared them would refuse content that matches. What binds that
// publication is the staged content comparison, which runs before the committing
// fact and the manifest PUT.
func TestManifestCommitIsNotAskedForTheFileDigest(t *testing.T) {
	const body = "0123456789abcdef"
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	path := writeFile(t, t.TempDir(), "object", body)
	_, item := uploadOne(t, session, "media", "docs/", path)

	stagedName := ".aeronautes/stage/binding/batch/object"
	seed(mock, "media_segments", "upload/0001", "01234567")
	seed(mock, "media_segments", "upload/0002", "89abcdef")
	mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/upload/"})

	result := commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), nil)
	if result.Result != transfercopy.ResultCopied || result.verification == nil ||
		result.verification.Result != transfercopy.ResultVerified {
		t.Fatalf("a manifest commit of the source's own content did not verify: %+v %+v", result.Observation, result.verification)
	}
	live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil || live.Class != swiftcontrol.ClassDLO {
		t.Fatalf("published object = %+v %v", live, err)
	}
	if strings.Trim(live.ETag, `"`) == md5hex(body) {
		t.Error("this fixture no longer distinguishes an aggregate tag from a content digest")
	}
}
