package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/ncw/swift/v2"
)

// newWritableSession runs the writable mock cluster and returns both the
// session under test and a raw wire connection for out-of-band edits.
func newWritableSession(t *testing.T) (*Session, *swift.Connection) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{
		AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester",
		ApiKey: "testing", AuthVersion: 1,
	}
	if err := conn.ContainerCreate(context.Background(), "media", nil); err != nil {
		t.Fatal(err)
	}
	return s, conn
}

func putDLO(t *testing.T, conn *swift.Connection) {
	t.Helper()
	_, err := conn.ObjectPut(context.Background(), "media", "video", nil, false, "", "video/mp4",
		swift.Headers{
			"X-Object-Meta-Shoot":    "wedding",
			"X-Object-Meta-Location": "sydney",
			"X-Object-Manifest":      "media_segments/video/",
		})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateObjectMetadataAppliesAndPreserves(t *testing.T) {
	s, conn := newWritableSession(t)
	putDLO(t, conn)
	ctx := context.Background()

	before, err := s.ObjectDetail(ctx, "media", "video")
	if err != nil {
		t.Fatal(err)
	}

	// The working-copy contract: start from everything, change one thing.
	edit := ObjectMetadataEdit{Meta: map[string]string{}}
	for k, v := range before.Meta {
		edit.Meta[k] = v
	}
	edit.Meta["Camera"] = "sony-fx6"

	result, err := s.UpdateObjectMetadata(ctx, "media", "video", before.EditableFingerprint, edit)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}

	after := result.After
	// The §60 gate lines: one edit erased nothing, and the DLO survived.
	if after.Meta["Shoot"] != "wedding" || after.Meta["Location"] != "sydney" || after.Meta["Camera"] != "sony-fx6" {
		t.Errorf("Meta after edit = %v", after.Meta)
	}
	if after.Class != ClassDLO || after.Manifest != "media_segments/video/" {
		t.Errorf("DLO not preserved: class=%q manifest=%q", after.Class, after.Manifest)
	}
	if after.ContentType != "video/mp4" {
		t.Errorf("ContentType = %q", after.ContentType)
	}
	if len(result.TransIDs) != 3 {
		t.Errorf("TransIDs = %v, want preflight+write+verify", result.TransIDs)
	}
	if after.EditableFingerprint == before.EditableFingerprint {
		t.Error("fingerprint must move after an applied edit")
	}
}

func TestUpdateObjectMetadataRemovalAndExpiry(t *testing.T) {
	s, conn := newWritableSession(t)
	putDLO(t, conn)
	ctx := context.Background()

	before, err := s.ObjectDetail(ctx, "media", "video")
	if err != nil {
		t.Fatal(err)
	}
	// Intent: only Shoot survives, and an expiry is set.
	deleteAt := time.Now().Add(time.Hour).Truncate(time.Second)
	result, err := s.UpdateObjectMetadata(ctx, "media", "video", before.EditableFingerprint, ObjectMetadataEdit{
		Meta:     map[string]string{"Shoot": "wedding"},
		DeleteAt: deleteAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}
	if _, still := result.After.Meta["Location"]; still {
		t.Error("removed key still present")
	}
	if result.After.DeleteAt.Unix() != deleteAt.Unix() {
		t.Errorf("DeleteAt = %v, want %v", result.After.DeleteAt, deleteAt)
	}

	// Second edit stating no expiry clears it (the caller states the
	// complete outcome; nothing is implicit).
	result2, err := s.UpdateObjectMetadata(ctx, "media", "video", result.After.EditableFingerprint, ObjectMetadataEdit{
		Meta: map[string]string{"Shoot": "wedding"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result2.Outcome != UpdateApplied || !result2.After.DeleteAt.IsZero() {
		t.Errorf("expiry not cleared: %q %v", result2.Outcome, result2.After.DeleteAt)
	}
}

func TestUpdateObjectMetadataConflict(t *testing.T) {
	s, conn := newWritableSession(t)
	putDLO(t, conn)
	ctx := context.Background()

	before, err := s.ObjectDetail(ctx, "media", "video")
	if err != nil {
		t.Fatal(err)
	}

	// Someone else edits between our read and our write.
	if err := conn.ObjectUpdate(ctx, "media", "video", swift.Headers{
		"X-Object-Meta-Interloper": "yes",
		"X-Object-Manifest":        "media_segments/video/",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := s.UpdateObjectMetadata(ctx, "media", "video", before.EditableFingerprint, ObjectMetadataEdit{
		Meta: map[string]string{"Ours": "would-clobber"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateConflict {
		t.Fatalf("Outcome = %q, want conflict", result.Outcome)
	}
	if result.After != nil || len(result.TransIDs) != 1 {
		t.Errorf("conflict must not write: %+v", result)
	}

	// The interloper's state is untouched.
	current, err := s.ObjectDetail(ctx, "media", "video")
	if err != nil {
		t.Fatal(err)
	}
	if current.Meta["Interloper"] != "yes" {
		t.Errorf("state after refused write = %v", current.Meta)
	}
	if _, wrote := current.Meta["Ours"]; wrote {
		t.Error("conflicted update wrote anyway")
	}
}

func TestVerifyObjectOutcome(t *testing.T) {
	before := &ObjectDetail{Class: ClassDLO, ContentType: "video/mp4"}
	edit := ObjectMetadataEdit{Meta: map[string]string{"shoot": "wedding"}}

	// Canonical-case comparison: intent "shoot" matches reported "Shoot".
	good := &ObjectDetail{
		Class: ClassDLO, ContentType: "video/mp4",
		Meta: map[string]string{"Shoot": "wedding"},
	}
	if mismatch := verifyObjectOutcome(before, edit, good); mismatch != "" {
		t.Errorf("unexpected mismatch: %s", mismatch)
	}

	// Lost manifest, lost key, stray key, wrong type — all reported.
	bad := &ObjectDetail{
		Class: ClassPlain, ContentType: "text/plain",
		Meta: map[string]string{"Stray": "x"},
	}
	mismatch := verifyObjectOutcome(before, edit, bad)
	for _, want := range []string{"no longer a DLO", "missing", "unexpected metadata", "content type"} {
		if !strings.Contains(mismatch, want) {
			t.Errorf("mismatch %q lacks %q", mismatch, want)
		}
	}
}

func TestUpdateObjectMetadataVerifyReadIsUnverified(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{
		AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester",
		ApiKey: "testing", AuthVersion: 1,
	}
	if err := conn.ContainerCreate(context.Background(), "media", nil); err != nil {
		t.Fatal(err)
	}
	putDLO(t, conn)
	mock.AfterRequest = func(method, path string) {
		if method == "POST" && strings.Contains(path, "/media/video") {
			mock.FailNextObjectHEADs(1)
		}
	}
	result, err := s.UpdateObjectMetadata(context.Background(), "media", "video", "", ObjectMetadataEdit{
		Meta: map[string]string{"Shoot": "wedding", "Location": "sydney", "Camera": "fx6"},
	})
	if !errors.Is(err, ErrUnverifiedWrite) {
		t.Fatalf("err = %v, want unverified", err)
	}
	if result == nil || result.Outcome != UpdateUnverified {
		t.Fatalf("result = %+v", result)
	}
	if result.Limitation == "" || len(result.TransIDs) < 2 {
		t.Fatalf("completed POST must keep limitation and write trans id: %+v", result)
	}
}

func TestUpdateObjectMetadataTransportFailureIsUnverified(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{
		AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester",
		ApiKey: "testing", AuthVersion: 1,
	}
	if err := conn.ContainerCreate(context.Background(), "media", nil); err != nil {
		t.Fatal(err)
	}
	putDLO(t, conn)
	mock.DropNextObjectPOST()
	result, err := s.UpdateObjectMetadata(context.Background(), "media", "video", "", ObjectMetadataEdit{
		Meta: map[string]string{"Shoot": "wedding"},
	})
	if !errors.Is(err, ErrUnverifiedWrite) {
		t.Fatalf("err = %v, want unverified", err)
	}
	if result == nil || result.Outcome != UpdateUnverified {
		t.Fatalf("result = %+v", result)
	}
}

func TestUpdateObjectMetadataStoreRefusalIsFailed(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{
		AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester",
		ApiKey: "testing", AuthVersion: 1,
	}
	if err := conn.ContainerCreate(context.Background(), "media", nil); err != nil {
		t.Fatal(err)
	}
	putDLO(t, conn)
	mock.SetObjectPOSTStatus(http.StatusForbidden)
	result, err := s.UpdateObjectMetadata(context.Background(), "media", "video", "", ObjectMetadataEdit{
		Meta: map[string]string{"Shoot": "wedding"},
	})
	if err == nil || errors.Is(err, ErrUnverifiedWrite) {
		t.Fatalf("err = %v, want store refusal", err)
	}
	if result != nil {
		t.Fatalf("store refusal must not keep a write result: %+v", result)
	}
}

func TestPostUnproven(t *testing.T) {
	if postUnproven(nil) {
		t.Fatal("nil is proven")
	}
	if postUnproven(Normalize(swift.Forbidden)) {
		t.Fatal("HTTP 403 is a store answer")
	}
	if !postUnproven(Normalize(context.DeadlineExceeded)) {
		t.Fatal("deadline with no HTTP status is unproven")
	}
}
