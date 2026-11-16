// Body and teardown helpers for the real-cloud oracle (realcloud_oracle_test.go)
// and the regressions that keep them safe; the clouds.yaml loading, backend
// selection and failure redaction they share with the journeys live in
// internal/realcloud. No build tag on purpose: the regressions run in the
// default build without credentials or network.
package swiftcontrol_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/realcloud"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// describeBodies is describeBody over what an archive held, in order.
func describeBodies(bodies []string) string {
	described := make([]string, len(bodies))
	for i, body := range bodies {
		described[i] = realcloud.DescribeBody([]byte(body))
	}
	return "[" + strings.Join(described, ", ") + "]"
}

// bodyMismatch is the failure line for a body that is not the one expected,
// or "" when it is.
func bodyMismatch(what string, got, want []byte) string {
	if bytes.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("%s: read %s, want %s", what, realcloud.DescribeBody(got), realcloud.DescribeBody(want))
}

// teardownProbe removes the probe object and container, best-effort, on its own
// bounded context so it survives the test's context being cancelled. It acts
// only when this run created the container: a refused create (the name was
// already taken) must send no destructive request, because a fresh fingerprint
// proves an object's identity, not that this run owns it.
func teardownProbe(s *swiftcontrol.Session, container, objName string, created bool) {
	if !created {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = s.DeleteName(ctx, container, objName)
	_ = s.DeleteContainer(ctx, container)
}

// --- regressions: default build, no credentials, no network ---

// A body that is not the one written fails the run without being printed: the
// line carries lengths and digests, which still tell the two bodies apart.
func TestOracleBodyMismatchPrintsNoContent(t *testing.T) {
	wrote := []byte("aeronautes keystone v3 oracle probe\n")
	page := []byte("<html>Sign in to identity.provider.example</html>")
	got := bodyMismatch("GET body", page, wrote)
	if got == "" {
		t.Fatal("a different body was not reported as a mismatch")
	}
	for _, leak := range []string{"provider", "Sign in", "html", "oracle probe"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q reached the mismatch line: %s", leak, got)
		}
	}
	if realcloud.DescribeBody(page) == realcloud.DescribeBody(wrote) {
		t.Errorf("the line cannot tell the two bodies apart: %s", got)
	}
	if line := bodyMismatch("GET body", wrote, wrote); line != "" {
		t.Errorf("the body that was written is reported as a mismatch: %s", line)
	}
}

func TestOracleTeardownOnlyWhatThisRunCreated(t *testing.T) {
	// created=false must issue no request at all, so a nil session is safe:
	// touching it would panic and fail this test.
	teardownProbe(nil, "aeronautes-oracle-not-ours", "probe-object", false)
}
