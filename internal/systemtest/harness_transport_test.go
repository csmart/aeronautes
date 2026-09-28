package systemtest

import (
	"net/http"
	"testing"
)

// The same invariant for the accounts a real harness provisions, which is
// where the failure actually happened. It needs the fixture, so it skips
// without one and runs in the integration job.
func TestHarnessAccountsOwnTheirTransport(t *testing.T) {
	h := New(t)
	seen := map[http.RoundTripper]string{}
	for name, a := range map[string]*Account{"primary": h.Primary, "secondary": h.Secondary} {
		if a == nil {
			continue
		}
		if a.rawClient.Transport == nil {
			t.Fatalf("the %s seed client rides the shared default pool", name)
		}
		if a.rawClient.Transport == http.DefaultTransport {
			t.Fatalf("the %s seed client uses the shared default transport itself", name)
		}
		if other, clash := seen[a.rawClient.Transport]; clash {
			t.Errorf("the %s and %s seed clients share one transport", name, other)
		}
		seen[a.rawClient.Transport] = name
	}
}
