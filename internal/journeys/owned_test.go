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
// as they were, and teardown removes the key.
func TestTheAccountKeyIsTheRunsOwn(t *testing.T) {
	_, session := mockSession(t)
	ctx := context.Background()
	if _, err := session.UpdateAccount(ctx, "", swiftcontrol.AccountEdit{Meta: map[string]string{"Operator": "kept"}}); err != nil {
		t.Fatal(err)
	}
	owned, err := NewOwned(session)
	if err != nil {
		t.Fatal(err)
	}
	key, err := owned.SetAccountKey(ctx, "set by the journey")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(key, owned.suffix) {
		t.Fatalf("key %q is not named for this run", key)
	}
	if failures := owned.Teardown(ctx); len(failures) != 0 {
		t.Fatal(failures)
	}
	portrait, err := session.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, left := portrait.Meta[key]; left || portrait.Meta["Operator"] != "kept" {
		t.Fatalf("after teardown the account metadata is %v", portrait.Meta)
	}
}
