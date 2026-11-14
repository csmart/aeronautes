package journeys

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func mockSession(t *testing.T) (*swifttest.Server, *swiftcontrol.Session) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return mock, session
}

// destructive lists the DELETE and POST requests the mock was sent for a
// container, the requests teardown or an edit could make.
func destructive(mock *swifttest.Server, container string) []string {
	var out []string
	for _, line := range mock.RequestLog() {
		if (strings.HasPrefix(line, "DELETE ") || strings.HasPrefix(line, "POST ")) && strings.Contains(line, "/"+container) {
			out = append(out, line)
		}
	}
	return out
}

// A name that is already taken is not this run's: the create fails, and
// teardown sends it nothing destructive (docs/journeys.md, Safety).
func TestATakenNameIsNeverDeleted(t *testing.T) {
	mock, session := mockSession(t)
	ctx := context.Background()
	owned, err := NewOwned(session)
	if err != nil {
		t.Fatal(err)
	}
	taken := ContainerPrefix + "taken-" + owned.suffix
	mock.AddContainer(taken, "standard")
	if _, err := owned.CreateContainer(ctx, "taken"); !errors.Is(err, swiftcontrol.ErrAlreadyExists) {
		t.Fatalf("creating over an existing container = %v, want it refused", err)
	}
	if failures := owned.Teardown(ctx); len(failures) != 0 {
		t.Fatal(failures)
	}
	if sent := destructive(mock, taken); len(sent) != 0 {
		t.Fatalf("a container this run did not create was sent %v", sent)
	}
}

// A sidecar that already exists stops the step and is never deleted, while
// the run's own container and a sidecar its step created are removed.
func TestTeardownRemovesOnlyWhatTheRunCreated(t *testing.T) {
	mock, session := mockSession(t)
	ctx := context.Background()
	owned, err := NewOwned(session)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := owned.CreateContainer(ctx, "mine")
	if err != nil {
		t.Fatal(err)
	}
	other, err := owned.CreateContainer(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	mock.AddContainer(other+"_segments", "standard")
	if err := owned.ClaimSidecar(ctx, other); err == nil {
		t.Fatal("a sidecar that already existed was claimed")
	}
	if err := owned.ClaimSidecar(ctx, mine); err != nil {
		t.Fatal(err)
	}
	if err := owned.ClaimSidecar(ctx, "someone-elses"); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("claiming a sidecar for a container the run did not create = %v", err)
	}
	// The step writes its segments and an object.
	mock.AddContainer(mine+"_segments", "standard")
	if _, err := session.PutObject(ctx, mine, "a", strings.NewReader("a"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	mock.AddContainer("unrelated", "standard")

	if failures := owned.Teardown(ctx); len(failures) != 0 {
		t.Fatal(failures)
	}
	if sent := destructive(mock, other+"_segments"); len(sent) != 0 {
		t.Fatalf("a sidecar this run did not create was sent %v", sent)
	}
	if sent := destructive(mock, "unrelated"); len(sent) != 0 {
		t.Fatalf("an unrelated container was sent %v", sent)
	}
	for _, gone := range []string{mine, mine + "_segments", other} {
		if _, err := session.ContainerDetail(ctx, gone); !swiftcontrol.IsNotFound(err) {
			t.Errorf("%s survived teardown: %v", gone, err)
		}
	}
	if _, err := session.ContainerDetail(ctx, other+"_segments"); err != nil {
		t.Errorf("the pre-existing sidecar was removed: %v", err)
	}
}

// The account key is the run's own, every other key and the quota are left
// as they were, and teardown removes the key whatever its suffix starts
// with: the client upper-cases a letter after a hyphen, so a suffix starting
// with a-f is spelt differently on the account than as first written.
func TestTheAccountKeyIsTheRunsOwn(t *testing.T) {
	for _, suffix := range []string{"abcdef0123456789", "0123456789abcdef"} {
		t.Run(suffix[:1], func(t *testing.T) {
			_, session := mockSession(t)
			ctx := context.Background()
			if _, err := session.UpdateAccount(ctx, "", swiftcontrol.AccountEdit{Meta: map[string]string{"Operator": "kept"}, QuotaBytes: 1 << 30}); err != nil {
				t.Fatal(err)
			}
			owned, err := NewOwned(session)
			if err != nil {
				t.Fatal(err)
			}
			owned.suffix = suffix
			if _, err := owned.SetAccountKey(ctx, "set by the journey"); err != nil {
				t.Fatal(err)
			}
			if !accountHolds(t, session, suffix) {
				t.Fatal("the run's key was not set")
			}
			if failures := owned.Teardown(ctx); len(failures) != 0 {
				t.Fatal(failures)
			}
			portrait, err := session.AccountPortrait(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if accountHolds(t, session, suffix) || portrait.Meta["Operator"] != "kept" || portrait.QuotaBytes != 1<<30 {
				t.Fatalf("after teardown the account holds %v with quota %d", portrait.Meta, portrait.QuotaBytes)
			}
		})
	}
}

// accountHolds reports whether any account metadata key, in any spelling,
// carries the run's suffix.
func accountHolds(t *testing.T, session *swiftcontrol.Session, suffix string) bool {
	t.Helper()
	portrait, err := session.AccountPortrait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for key := range portrait.Meta {
		if strings.Contains(strings.ToLower(key), suffix) {
			return true
		}
	}
	return false
}

// A container a journey deleted itself is skipped by teardown, not reported.
func TestTeardownSkipsAContainerAlreadyGone(t *testing.T) {
	_, session := mockSession(t)
	ctx := context.Background()
	owned, err := NewOwned(session)
	if err != nil {
		t.Fatal(err)
	}
	name, err := owned.CreateContainer(ctx, "gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.DeleteContainer(ctx, name); err != nil {
		t.Fatal(err)
	}
	if failures := owned.Teardown(ctx); len(failures) != 0 {
		t.Fatalf("teardown reported a container the journey already deleted: %v", failures)
	}
}

// Names is the read a browser journey checks a deletion against. It lists
// what the run created or claimed and refuses anything else unread, so a
// journey never lists a container that is not the run's.
func TestNamesListsOnlyTheRunsOwnContainers(t *testing.T) {
	mock, session := mockSession(t)
	ctx := context.Background()
	owned, err := NewOwned(session)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := owned.CreateContainer(ctx, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.ClaimSidecar(ctx, mine); err != nil {
		t.Fatal(err)
	}
	if names, err := owned.Names(ctx, mine+"_segments"); err != nil || len(names) != 0 {
		t.Fatalf("names of a sidecar nothing has written to = %v, %v; want none", names, err)
	}
	mock.AddContainer(mine+"_segments", "standard")
	mock.AddContainer("unrelated", "standard")
	for container, name := range map[string]string{mine: "dir/big", mine + "_segments": "dir/big/0001", "unrelated": "theirs"} {
		if _, err := session.PutObject(ctx, container, name, strings.NewReader("x"), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
	}
	for container, want := range map[string]string{mine: "dir/big", mine + "_segments": "dir/big/0001"} {
		if names, err := owned.Names(ctx, container); err != nil || len(names) != 1 || names[0] != want {
			t.Fatalf("names of %s = %v, %v; want [%s]", container, names, err, want)
		}
	}
	sent := len(mock.RequestLog())
	if _, err := owned.Names(ctx, "unrelated"); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("listing a container the run did not create = %v, want it refused", err)
	}
	if len(mock.RequestLog()) != sent {
		t.Fatal("the refusal still sent a request")
	}
}

// A real store archives each object deleted from a container that keeps
// versions, and refuses the delete once the archive is gone, so teardown
// stops the container keeping versions before it deletes anything there.
func TestTeardownStopsKeepingVersionsFirst(t *testing.T) {
	mock, session := mockSession(t)
	ctx := context.Background()
	owned, err := NewOwned(session)
	if err != nil {
		t.Fatal(err)
	}
	live, err := owned.CreateContainer(ctx, "live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.PutObject(ctx, live, "a", strings.NewReader("a"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	mock.EnableVersionArchive(live, "archive", true)
	if failures := owned.Teardown(ctx); len(failures) != 0 {
		t.Fatal(failures)
	}
	sent := destructive(mock, live)
	if len(sent) < 2 || !strings.HasPrefix(sent[0], "POST ") || !strings.HasSuffix(sent[0], "/"+live) {
		t.Fatalf("teardown sent %v, want the container's own edit before any delete", sent)
	}
}
