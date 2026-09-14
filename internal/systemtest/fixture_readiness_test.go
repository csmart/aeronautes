package systemtest

import (
	"strings"
	"testing"
	"time"
)

// The readiness gate exists because the pipeline's /info wait proves the
// proxy answers, not that a client can create a container (ADR 0050).
// These cases hold the difference: a writable account is admitted, and
// an account that answers a container PUT with a non-2xx is refused with
// what was observed rather than being let through to fail later at a
// seed.

// A ready fixture is admitted, and the probe leaves nothing behind for
// the residue sweep to find.
func TestFixtureReadinessAdmitsAWritableAccount(t *testing.T) {
	h := New(t)
	if err := h.writableWithin("primary", h.Primary, fixtureWritableDeadline); err != nil {
		t.Fatalf("writable primary refused: %v", err)
	}
	names, err := h.Primary.Oracle.ListContainers(h.ns)
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	for _, name := range names {
		if strings.Contains(name, "-readiness-") {
			t.Errorf("readiness probe left %q behind", name)
		}
	}
}

// An account whose container PUT is refused must not be admitted. A
// corrupted token stands in for any fixture that answers a write with a
// non-2xx; the gate must report the status it saw rather than returning
// success, because the alternative is the first seed failing later with
// the same information and less of it.
func TestFixtureReadinessRefusesAnUnwritableAccount(t *testing.T) {
	h := New(t)
	broken := *h.Primary
	broken.token = "not-a-token"

	start := time.Now()
	err := h.writableWithin("primary", &broken, 750*time.Millisecond)
	if err == nil {
		t.Fatal("an account that cannot create a container was admitted")
	}
	if elapsed := time.Since(start); elapsed < 750*time.Millisecond {
		t.Errorf("gave up after %s, before its own deadline", elapsed)
	}
	for _, want := range []string{"primary", "not writable within", "last status 401"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not report %q: %v", want, err)
		}
	}
}

// The probe reports a status rather than failing the test, so the
// deadline is the only thing that decides.
func TestFixtureReadinessProbeReportsRatherThanFailing(t *testing.T) {
	h := New(t)
	broken := *h.Primary
	broken.token = "not-a-token"
	status, _, err := h.probeWritable(&broken, h.ns+"-probe-report")
	if err != nil {
		t.Fatalf("probe returned a transport error: %v", err)
	}
	if status/100 == 2 {
		t.Fatalf("probe reported %d for an unusable token", status)
	}
}
