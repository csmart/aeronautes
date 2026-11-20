//go:build uijourney

package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func post(t *testing.T, server *httptest.Server, path, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-Journey-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Only the runner holding the token can script anything, and a picker or a
// question nothing scripted fails rather than answering.
func TestJourneyControlScriptsOnlyWithTheToken(t *testing.T) {
	control.token = "the-token"
	server := httptest.NewServer(control)
	t.Cleanup(server.Close)
	if code := post(t, server, "/pick", "", `{"dir":"/tmp/x"}`); code != http.StatusUnauthorized {
		t.Fatalf("a pick without the token answered %d", code)
	}
	if code := post(t, server, "/pick", "wrong", `{"dir":"/tmp/x"}`); code != http.StatusUnauthorized {
		t.Fatalf("a pick with the wrong token answered %d", code)
	}
	if _, err := nativePicker().PickDirectory("Choose"); err == nil {
		t.Fatal("a folder pick nothing scripted was answered")
	}
	if _, err := nativeOverwriteConfirmer().ConfirmOverwrite(context.Background(), app.OverwriteConfirmation{}); err == nil {
		t.Fatal("an overwrite question nothing scripted was answered")
	}
	if code := post(t, server, "/pick", "the-token", `{"dir":"/tmp/x"}`); code != http.StatusOK {
		t.Fatalf("a scripted pick answered %d", code)
	}
	if code := post(t, server, "/confirm", "the-token", `{"question":"public","answer":true}`); code != http.StatusOK {
		t.Fatalf("a scripted answer answered %d", code)
	}
	if dir, err := nativePicker().PickDirectory("Choose"); err != nil || dir != "/tmp/x" {
		t.Fatalf("scripted pick = %q, %v", dir, err)
	}
	if _, err := nativePicker().PickDirectory("Choose"); err == nil {
		t.Fatal("a scripted pick was answered twice")
	}
	if yes, err := nativePublicConfirmer().ConfirmPublicRead(context.Background(), "c", "a"); err != nil || !yes {
		t.Fatalf("scripted answer = %t, %v", yes, err)
	}
}

// The build serves on loopback only, refuses a variable that would move it,
// and prints one line carrying the two ports and the token and nothing else.
func TestServeOptionsStayOnLoopback(t *testing.T) {
	t.Setenv("WAILS_SERVER_HOST", "0.0.0.0")
	if _, err := serveOptions(journeyServices{}); err == nil {
		t.Fatal("a non-loopback WAILS_SERVER_HOST was accepted")
	}
	t.Setenv("WAILS_SERVER_HOST", "")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	options, err := serveOptions(journeyServices{})
	os.Stdout = stdout
	write.Close()
	if err != nil || options.Host != "127.0.0.1" || options.Port == 0 {
		t.Fatalf("options = %+v, %v", options, err)
	}
	lines := bufio.NewScanner(read)
	lines.Scan()
	if !regexp.MustCompile(`^uijourney ui=127\.0\.0\.1:\d+ control=127\.0\.0\.1:\d+ token=[0-9a-f]{32}$`).MatchString(lines.Text()) || lines.Scan() {
		t.Errorf("startup output = %q", lines.Text())
	}
}

// A fresh Playwright worker asks again for an account the run already
// opened. The answer must keep the first ownership record, or teardown
// would miss the containers created before the second request.
func TestJourneyControlReopeningAnAccountKeepsWhatItOwns(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	store := httptest.NewServer(mock)
	t.Cleanup(store.Close)
	authURL := store.URL + "/auth/v1.0"
	ctx := context.Background()
	if _, err := control.account(ctx, "mock", authURL); err != nil {
		t.Fatal(err)
	}
	reply, err := control.container(ctx, "mock", "reopen", false)
	if err != nil {
		t.Fatal(err)
	}
	created := reply.(map[string]string)["container"]
	if _, err := control.account(ctx, "mock", authURL); err != nil {
		t.Fatal(err)
	}
	control.startTeardown()
	var status map[string]any
	for deadline := time.Now().Add(30 * time.Second); ; {
		status = control.teardownStatus().(map[string]any)
		if status["done"] == true || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status["done"] != true || status["failures"] != 0 {
		t.Fatalf("teardown = %v, want done with no failed removals", status)
	}
	deleted := false
	for _, line := range mock.RequestLog() {
		deleted = deleted || (strings.HasPrefix(line, "DELETE ") && strings.HasSuffix(line, "/"+created))
	}
	if !deleted {
		t.Fatalf("teardown never deleted %s, created before the account was asked for again", created)
	}
}

func TestJourneyControlSAIORequiresItsOwnEndpoint(t *testing.T) {
	t.Setenv("AERONAUTES_SAIO_AUTH_URL", "")
	var c journeyControl
	_, err := c.account(context.Background(), "saio", "http://unused.invalid/auth/v1.0")
	if err == nil || !strings.Contains(err.Error(), "no SAIO endpoint configured") {
		t.Fatalf("an unconfigured SAIO account = %v, want endpoint refusal", err)
	}
}
