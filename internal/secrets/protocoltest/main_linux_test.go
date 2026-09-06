package protocoltest

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	privateAddress string
	unavailable    string
	service        *secretService
)

// A separate test binary keeps godbus's cached SessionBus away from the real
// keychain integration tests, whichever order go test chooses for packages.
func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) (code int) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		unavailable = "private Secret Service fixture requires dbus-daemon"
		if os.Getenv("AERONAUTES_REQUIRE_SECRET_SERVICE_FIXTURE") == "1" {
			fmt.Fprintln(os.Stderr, unavailable)
			return 1
		}
		return m.Run()
	}
	stop, err := startPrivateBus(daemon)
	if err != nil {
		fmt.Fprintln(os.Stderr, "private Secret Service fixture:", err)
		return 1
	}
	defer func() {
		if err := stop(); err != nil {
			fmt.Fprintln(os.Stderr, "private Secret Service teardown:", err)
			code = 1
		}
	}()
	// The pinned client's prompt wait has no context. A broken fixture must
	// fail this binary and clean up its daemon instead of hanging the suite.
	deadline := time.AfterFunc(45*time.Second, func() {
		fmt.Fprintln(os.Stderr, "private Secret Service fixture exceeded 45 seconds")
		_ = stop()
		os.Exit(1)
	})
	defer deadline.Stop()
	service, err = newSecretService(privateAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, "private Secret Service fixture:", err)
		return 1
	}
	defer service.conn.Close()
	return m.Run()
}

func startPrivateBus(daemon string) (func() error, error) {
	// TMPDIR can be longer than a Unix socket path permits. This Linux-only
	// fixture needs a short private directory, independent of the checkout.
	dir, err := os.MkdirTemp("/tmp", "aero-bus-")
	if err != nil {
		return nil, err
	}
	remove := func() { _ = os.RemoveAll(dir) }
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		remove()
		return nil, errors.New("fixture directory is not private")
	}
	socket := filepath.Join(dir, "bus")
	if len(socket) >= 100 {
		remove()
		return nil, errors.New("fixture socket path is too long")
	}
	// No service directories or includes: an absent fake must never activate
	// a real keyring daemon, even on this private bus.
	config := `<busconfig><type>session</type><listen>unix:path=` + socket + `</listen>
<auth>EXTERNAL</auth><policy context="default">
<allow own="org.freedesktop.secrets"/><allow send_destination="*"/>
<allow receive_sender="*"/></policy></busconfig>`
	configPath := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		remove()
		return nil, err
	}
	cmd := exec.Command(daemon, "--nofork", "--nopidfile", "--config-file="+configPath, "--print-address=1")
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		remove()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		remove()
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	var stopErr error
	stop := func() error {
		once.Do(func() {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				stopErr = errors.New("private D-Bus daemon did not exit after kill")
			}
			stopErr = errors.Join(stopErr, os.RemoveAll(dir))
		})
		return stopErr
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	select {
	case privateAddress = <-ready:
		if !strings.HasPrefix(privateAddress, "unix:path="+socket+",guid=") {
			_ = stop()
			return nil, errors.New("daemon did not return this fixture's socket")
		}
	case <-time.After(5 * time.Second):
		_ = stop()
		return nil, errors.New("private D-Bus startup timed out")
	}
	if err := os.Setenv("DBUS_SESSION_BUS_ADDRESS", privateAddress); err != nil {
		_ = stop()
		return nil, err
	}
	return stop, nil
}

func fixture(t *testing.T) *secretService {
	t.Helper()
	if unavailable != "" {
		t.Skip(unavailable)
	}
	if privateAddress == "" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") != privateAddress {
		t.Fatal("refusing any bus except the per-run private fixture")
	}
	return service
}
