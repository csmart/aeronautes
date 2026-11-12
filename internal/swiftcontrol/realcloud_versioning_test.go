//go:build realcloud

// Oracle: measure what the two legacy container-versioning modes do on a
// GENUINE Swift, through the product's own routes rather than a raw probe.
// The 27 October record (docs/experiments/2026-10-27-delete-under-versioning.md)
// measured the SAIO fixture; the delete dialogs and the overwrite question
// state that matrix as fact, so a real store must be shown to agree with it.
// Each mode gets a fresh owner container and a fresh archive container with
// random names; the matrix is written once, overwritten once and deleted
// through DeletePlainObject, and the name and the archive are read after
// every step, bodies included, so what the archive holds is stated from
// content. A backend whose /info does not allow a mode skips that mode: the
// editor never offers it there. Cleanup removes only what this run created
// and reports anything it could not remove. It shares the clouds.yaml
// selection, the failure line and the make target with the Keystone oracle:
//
//	make oracle CLOUDS=/path/to/clouds.yaml [CLOUD=swift]
package swiftcontrol_test

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/realcloud"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestLegacyVersioningOracle(t *testing.T) {
	runOnOracleBackends(t, func(t *testing.T, label, backend string, c realcloud.Cloud) {
		s, err := swiftcontrol.New(swiftcontrol.Config{
			AuthVersion:   3,
			AuthURL:       c.AuthURL,
			Username:      c.Username,
			APIKey:        c.Password,
			UserDomain:    c.UserDomain,
			ProjectName:   c.ProjectName,
			ProjectDomain: c.ProjectDomain,
			Region:        c.Region,
		})
		if err != nil {
			t.Fatalf("New(v3): %s", realcloud.Failure(err))
		}
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := s.Authenticate(ctx); err != nil {
			t.Fatalf("Authenticate against %s: %s", label, realcloud.Failure(err))
		}
		caps, err := s.FetchCapabilities(ctx)
		if err != nil {
			t.Fatalf("FetchCapabilities: %s", realcloud.Failure(err))
		}
		// The flags themselves are the store's strings; whether each mode is
		// allowed is all the matrix needs to say.
		t.Logf("[%s] backend=%s allows versions_location=%v history_location=%v", label, backend,
			caps.AllowsVersioningMode(swiftcontrol.VersioningVersionsLocation), caps.AllowsVersioningMode(swiftcontrol.VersioningHistoryLocation))
		for _, mode := range []swiftcontrol.VersioningMode{swiftcontrol.VersioningVersionsLocation, swiftcontrol.VersioningHistoryLocation} {
			t.Run(string(mode), func(t *testing.T) {
				if !caps.AllowsVersioningMode(mode) {
					t.Skipf("%s does not advertise %s; the editor never offers it there", backend, mode)
				}
				measureLegacyMode(t, s, mode)
			})
		}
	})
}

// measureLegacyMode runs the overwrite-then-delete matrix for one mode and
// asserts the behaviour the SAIO record states, so a real store that differs
// fails the run instead of being described wrongly in a dialog.
func measureLegacyMode(t *testing.T, s *swiftcontrol.Session, mode swiftcontrol.VersioningMode) {
	// fail must be built per subtest: FailNow on a parent test from a subtest
	// goroutine is an error of its own.
	fail := func(op string, err error) {
		t.Helper()
		t.Fatalf("%s: %s", op, realcloud.Failure(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	suffix := randomSuffix(t)
	owner := "aeronautes-oracle-versioned-" + suffix
	archive := "aeronautes-oracle-archive-" + suffix
	var created []string
	defer func() { teardownVersioningProbe(t, s, created) }()
	for _, name := range []string{owner, archive} {
		if _, err := s.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: name}); err != nil {
			fail("CreateContainer "+name, err)
		}
		created = append(created, name)
	}

	// The mode is set through the container editor's route, so the edit's
	// own preflight, delta and verify are part of what is measured.
	before, err := s.ContainerDetail(ctx, owner)
	if err != nil {
		fail("ContainerDetail before the edit", err)
	}
	edit := swiftcontrol.ContainerEdit{Versioning: swiftcontrol.ContainerVersioning{Mode: mode, Archive: archive}}
	res, err := s.UpdateContainer(ctx, owner, before.EditableFingerprint, edit)
	if err != nil {
		fail("UpdateContainer versioning", err)
	}
	if res.Outcome != swiftcontrol.UpdateApplied || res.After.Versioning != edit.Versioning {
		t.Fatalf("versioning edit: outcome=%s; the container shows the mode asked for: %v, the archive asked for: %v",
			res.Outcome, res.After.Versioning.Mode == mode, res.After.Versioning.Archive == archive)
	}
	t.Logf("%s set on %s with archive %s", mode, owner, archive)

	const name = "probe-object"
	first := "first version\n"
	second := "second version, longer than the first\n"
	for _, body := range []string{first, second} {
		if _, err := s.PutObject(ctx, owner, name, bytes.NewReader([]byte(body)), "text/plain", nil); err != nil {
			fail("PutObject", err)
		}
	}
	expectCurrent(t, ctx, s, owner, name, second)
	expectArchive(t, ctx, s, archive, first)
	t.Logf("after two writes: the name serves the second body, the archive holds the first")

	// The delete goes through the product's protocol, whose literal read
	// afterwards reports a successor at the name when there is one.
	current, err := s.ObjectDetail(ctx, owner, name)
	if err != nil {
		fail("ObjectDetail before the delete", err)
	}
	del, err := s.DeletePlainObject(ctx, owner, name, current.ContentFingerprint())
	if err != nil {
		fail("DeletePlainObject", err)
	}
	if !del.Accepted || del.Outcome != swiftcontrol.UpdateApplied {
		t.Fatalf("DeletePlainObject: accepted=%v outcome=%s", del.Accepted, del.Outcome)
	}
	switch mode {
	case swiftcontrol.VersioningVersionsLocation:
		// The second body is destroyed, the archived first body moves back to
		// the name and the archive empties; the route reports that successor.
		if del.After == nil {
			t.Fatalf("the delete should leave the archived predecessor at the name; the read saw absence")
		}
		expectCurrent(t, ctx, s, owner, name, first)
		expectArchive(t, ctx, s, archive)
		t.Logf("delete reverted the name to the first body (successor reported, length %d) and emptied the archive", del.After.ContentLength)

		restored, err := s.ObjectDetail(ctx, owner, name)
		if err != nil {
			fail("ObjectDetail of the restored body", err)
		}
		again, err := s.DeletePlainObject(ctx, owner, name, restored.ContentFingerprint())
		if err != nil {
			fail("DeletePlainObject of the restored body", err)
		}
		if !again.Accepted || again.Outcome != swiftcontrol.UpdateApplied || again.After != nil {
			t.Fatalf("second delete: accepted=%v outcome=%s successor=%v", again.Accepted, again.Outcome, again.After != nil)
		}
		expectAbsent(t, ctx, s, owner, name)
		expectArchive(t, ctx, s, archive)
		t.Logf("a second delete destroyed the restored body; nothing archived")
	case swiftcontrol.VersioningHistoryLocation:
		// Both bodies end in the archive with a zero-byte deletion marker, and
		// the name stops resolving.
		if del.After != nil {
			t.Fatalf("the delete should leave the name absent; the read saw a successor of length %d", del.After.ContentLength)
		}
		expectAbsent(t, ctx, s, owner, name)
		expectArchive(t, ctx, s, archive, first, second, "")
		t.Logf("delete moved the second body to the archive beside the first and wrote a marker")
	}
}

// expectCurrent asserts the object at name has body by HEAD length and by
// reading it: the matrix is about content, not about a row existing.
func expectCurrent(t *testing.T, ctx context.Context, s *swiftcontrol.Session, container, name, body string) {
	t.Helper()
	detail, err := s.ObjectDetail(ctx, container, name)
	if err != nil {
		t.Fatalf("ObjectDetail %s: %s", name, realcloud.Failure(err))
	}
	if detail.ContentLength != int64(len(body)) {
		t.Fatalf("%s: HEAD length %d, want %d", name, detail.ContentLength, len(body))
	}
	if line := bodyMismatch(name, []byte(readBody(t, ctx, s, container, name)), []byte(body)); line != "" {
		t.Fatal(line)
	}
}

func expectAbsent(t *testing.T, ctx context.Context, s *swiftcontrol.Session, container, name string) {
	t.Helper()
	_, err := s.ObjectDetail(ctx, container, name)
	switch {
	case err == nil:
		t.Fatalf("%s: still present, want not found", name)
	case !swiftcontrol.IsNotFound(err):
		t.Fatalf("ObjectDetail %s: %s", name, realcloud.Failure(err))
	}
}

// expectArchive asserts the archive holds exactly the given bodies ("" is a
// zero-byte marker), read back one by one. The listing is retried briefly
// because a real cluster's container listing can trail the object write.
func expectArchive(t *testing.T, ctx context.Context, s *swiftcontrol.Session, archive string, want ...string) {
	t.Helper()
	slices.Sort(want)
	var got []string
	for attempt := 0; attempt < 10; attempt++ {
		got = got[:0]
		for entry, err := range s.Objects(ctx, archive, swiftcontrol.ObjectListOptions{Deep: true}) {
			if err != nil {
				t.Fatalf("list %s: %s", archive, realcloud.Failure(err))
			}
			if obj, ok := entry.(swiftcontrol.ObjectEntry); ok {
				got = append(got, readBody(t, ctx, s, archive, obj.Name))
			}
		}
		slices.Sort(got)
		if slices.Equal(got, want) {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("archive %s holds %s, want %s", archive, describeBodies(got), describeBodies(want))
}

func readBody(t *testing.T, ctx context.Context, s *swiftcontrol.Session, container, name string) string {
	t.Helper()
	rc, err := s.OpenObjectContent(ctx, container, name)
	if err != nil {
		// The name can be one the store listed, so the container this run
		// named stands for it.
		t.Fatalf("open an object in %s: %s", container, realcloud.Failure(err))
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read an object in %s: %s", container, realcloud.Failure(err))
	}
	return string(body)
}

// teardownVersioningProbe removes the containers this run created, their
// objects first, on its own bounded context. Versioning is switched off on
// the owner before the sweep so a delete there neither restores from nor
// archives into the other container; the sweep still repeats, because a
// failed switch-off leaves either effect possible. A container that remains
// is reported: litter in a real account is a failure, not a shrug.
func teardownVersioningProbe(t *testing.T, s *swiftcontrol.Session, created []string) {
	t.Helper()
	if len(created) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := created[0]
	_, _ = s.UpdateContainer(ctx, owner, "", swiftcontrol.ContainerEdit{Versioning: swiftcontrol.ContainerVersioning{Mode: swiftcontrol.VersioningOff}})
	for pass := 0; pass < 3; pass++ {
		for _, container := range created {
			for entry, err := range s.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true}) {
				if err != nil {
					break
				}
				if obj, ok := entry.(swiftcontrol.ObjectEntry); ok {
					_ = s.DeleteObject(ctx, container, obj.Name)
				}
			}
		}
	}
	for _, container := range created {
		if err := s.DeleteContainer(ctx, container); err != nil {
			t.Errorf("probe container %s was not removed: %s", container, realcloud.Failure(err))
		}
	}
}
