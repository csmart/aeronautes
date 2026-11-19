//go:build uijourney

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/journeys"
	"github.com/csmart/aeronautes/internal/realcloud"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// This build drives the real application from a browser (ADR 0112). The
// native dialogs are answered by what the test runner scripts over a
// token-guarded control listener on 127.0.0.1, credentials never leave Go,
// and the secret store lives for the process.

var control = &journeyControl{answers: map[string][]bool{}, accounts: map[string]openAccount{}, started: time.Now().UTC().Format(time.RFC3339Nano)}

func nativePicker() app.FilePicker { return scriptedPicker{} }

func nativePublicConfirmer() app.PublicACLConfirmer { return scriptedConfirmer{} }

func nativeOverwriteConfirmer() app.OverwriteConfirmer { return scriptedConfirmer{} }

// nativeSecrets opens the session store directly: the default opener probes
// the OS keychain, which this build must never touch.
func nativeSecrets() (secrets.SecretStore, secrets.Backend) {
	return secrets.NewSessionStore(), secrets.BackendSession
}

// serveOptions pins the application server to loopback, opens the control
// listener and prints the one startup line the runner reads: the two ports
// and the token, nothing else.
func serveOptions(services journeyServices) (application.ServerOptions, error) {
	// These variables exist to move the listener, and this process holds
	// real credentials, so any setting other than loopback refuses to start.
	if host := os.Getenv("WAILS_SERVER_HOST"); host != "" && host != "127.0.0.1" {
		return application.ServerOptions{}, errors.New("the UI journey build serves on 127.0.0.1 only; unset WAILS_SERVER_HOST")
	}
	if os.Getenv("WAILS_SERVER_PORT") != "" {
		return application.ServerOptions{}, errors.New("the UI journey build chooses its own port; unset WAILS_SERVER_PORT")
	}
	// A restart the runner makes after a journey quits the application
	// comes back on the ports and token of the first start, so the journey
	// carries on against the same addresses (test plan step 9).
	uiPort, controlAddr, token, err := listenAgain(os.Getenv("AERONAUTES_UIJOURNEY_LISTEN"))
	if err != nil {
		return application.ServerOptions{}, err
	}
	if uiPort == 0 {
		if uiPort, err = freeLoopbackPort(); err != nil {
			return application.ServerOptions{}, err
		}
		controlAddr = "127.0.0.1:0"
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return application.ServerOptions{}, err
		}
		token = hex.EncodeToString(random)
	}
	listener, err := net.Listen("tcp", controlAddr)
	if err != nil {
		return application.ServerOptions{}, err
	}
	control.token = token
	control.bookmarks = services.bookmarks
	go http.Serve(listener, control)
	fmt.Fprintf(os.Stdout, "uijourney ui=127.0.0.1:%d control=%s token=%s\n", uiPort, listener.Addr(), control.token)
	return application.ServerOptions{Host: "127.0.0.1", Port: uiPort}, nil
}

// listenAgain reads the startup line's ui, control and token from an earlier
// start, as the runner passes them back; empty means a first start.
func listenAgain(line string) (uiPort int, controlAddr, token string, err error) {
	if line == "" {
		return 0, "", "", nil
	}
	var ui string
	if _, err := fmt.Sscanf(line, "uijourney ui=%s control=%s token=%s", &ui, &controlAddr, &token); err != nil {
		return 0, "", "", fmt.Errorf("the earlier start line cannot be read: %w", err)
	}
	host, port, err := net.SplitHostPort(ui)
	if err != nil || host != "127.0.0.1" || !strings.HasPrefix(controlAddr, "127.0.0.1:") {
		return 0, "", "", errors.New("the earlier start was not on 127.0.0.1")
	}
	if _, err := fmt.Sscanf(port, "%d", &uiPort); err != nil {
		return 0, "", "", err
	}
	return uiPort, controlAddr, token, nil
}

func freeLoopbackPort() (int, error) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer probe.Close()
	return probe.Addr().(*net.TCPAddr).Port, nil
}

// journeyControl holds what the runner scripted and what the run owns.
type journeyControl struct {
	token     string
	bookmarks *app.BookmarkService

	// opening keeps two requests for one account from both opening it.
	opening sync.Mutex

	mu       sync.Mutex
	picks    []pick
	answers  map[string][]bool
	accounts map[string]openAccount
	// asked is what each native surface was asked since the runner last
	// read it, so a journey checks the words a person would read there.
	asked []asked
	// started tells one start of the application from the next, so a
	// journey that quit it waits for the new start, not the old one's last
	// moments, before it loads the page again.
	started string
}

// asked is one native surface's words: a picker's title, or a question's
// title and message.
type asked struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func (c *journeyControl) record(kind, title, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, asked{Kind: kind, Title: title, Text: text})
}

// openAccount is an account the run opened: its ownership record and the
// bookmark the page connects through, empty for the mock, whose bookmark
// the editor makes.
type openAccount struct {
	owned    *journeys.Owned
	bookmark string
}

// pick is one scripted picker answer: files for a file pick, dir for a
// folder pick, or cancel for a person closing the chooser, which the native
// picker answers with nothing chosen and no error.
type pick struct {
	Files  []string `json:"files"`
	Dir    string   `json:"dir"`
	Cancel bool     `json:"cancel"`
}

type scriptedPicker struct{}

func (scriptedPicker) PickFiles(title string) ([]string, error) {
	control.record("pick files", title, "")
	next, err := control.nextPick()
	if err == nil && next.Cancel {
		return nil, nil
	}
	if err != nil || next.Files == nil {
		return nil, errors.Join(err, errors.New("the UI journey scripted no file pick"))
	}
	return next.Files, nil
}

func (scriptedPicker) PickDirectory(title string) (string, error) {
	control.record("pick folder", title, "")
	next, err := control.nextPick()
	if err == nil && next.Cancel {
		return "", nil
	}
	if err != nil || next.Dir == "" {
		return "", errors.Join(err, errors.New("the UI journey scripted no folder pick"))
	}
	return next.Dir, nil
}

func (c *journeyControl) nextPick() (pick, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.picks) == 0 {
		return pick{}, errors.New("nothing scripted")
	}
	next := c.picks[0]
	c.picks = c.picks[1:]
	return next, nil
}

type scriptedConfirmer struct{}

func (scriptedConfirmer) ConfirmPublicRead(_ context.Context, container, account string) (bool, error) {
	title, message := app.PublicReadQuestion(container, account)
	control.record("public", title, message)
	return control.nextAnswer("public")
}

func (scriptedConfirmer) ConfirmOverwrite(_ context.Context, about app.OverwriteConfirmation) (bool, error) {
	title, lines := app.OverwriteQuestion(about)
	control.record("overwrite", title, strings.Join(lines, "\n"))
	return control.nextAnswer("overwrite")
}

// nextAnswer is the next scripted answer to a question, and an error when
// none was scripted, so a journey cannot proceed on an answer nobody gave.
func (c *journeyControl) nextAnswer(question string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	queued := c.answers[question]
	if len(queued) == 0 {
		return false, fmt.Errorf("the UI journey scripted no answer to the %s question", question)
	}
	c.answers[question] = queued[1:]
	return queued[0], nil
}

// ServeHTTP answers the runner. Every request needs the token; every error
// is the redacted form, so nothing the runner might print names a host.
func (c *journeyControl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Journey-Token")), []byte(c.token)) != 1 {
		http.Error(w, "refused", http.StatusUnauthorized)
		return
	}
	var request struct {
		pick
		Question  string `json:"question"`
		Answer    bool   `json:"answer"`
		Label     string `json:"label"`
		AuthURL   string `json:"authUrl"`
		Name      string `json:"name"`
		Segmented bool   `json:"segmented"`
		Container string `json:"container"`
		Body      string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	var reply any
	var err error
	switch r.URL.Path {
	case "/pick":
		c.mu.Lock()
		c.picks = append(c.picks, request.pick)
		c.mu.Unlock()
	case "/confirm":
		c.mu.Lock()
		c.answers[request.Question] = append(c.answers[request.Question], request.Answer)
		c.mu.Unlock()
	case "/account":
		reply, err = c.account(ctx, request.Label, request.AuthURL)
	case "/container":
		reply, err = c.container(ctx, request.Label, request.Name, request.Segmented)
	case "/same-name":
		reply, err = c.namedContainer(ctx, request.Label, request.Name)
	case "/names":
		reply, err = c.names(ctx, request.Label, request.Container)
	case "/put":
		reply, err = c.put(ctx, request.Label, request.Container, request.Name, request.Body)
	case "/quit":
		// The same way out as closing the window: the shutdown that runs
		// then is what the next start's disclosure reports on. The reply
		// goes first, so the runner is not left waiting on a closed socket.
		go func() {
			time.Sleep(200 * time.Millisecond)
			application.Get().Quit()
		}()
		reply = map[string]bool{"quitting": true}
	case "/started":
		reply = map[string]string{"started": c.started}
	case "/crash":
		// A stop with no shutdown at all, as a crash or a power cut ends a
		// run: what the next start finds unfinished is what it discloses.
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(3)
		}()
		reply = map[string]bool{"crashing": true}
	case "/asked":
		c.mu.Lock()
		reply, c.asked = append([]asked{}, c.asked...), nil
		c.mu.Unlock()
	case "/teardown":
		reply = c.teardown(ctx)
	default:
		http.Error(w, "unknown", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, realcloud.Failure(err), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

// account opens the run's own session on one account and starts the record
// of what the run creates there. A real cloud's entry is read from
// clouds.yaml here, in Go, and its bookmark saved with its password, so no
// credential reaches the runner or the page; the mock's bookmark is made
// through the editor, with the mock's fixed test key.
//
// An account already open is answered from what was opened. Playwright
// starts a fresh worker after a failure, and that worker asks again: a
// second record would leave the containers in the first out of teardown,
// and a second bookmark of the same name would make the rail ambiguous.
func (c *journeyControl) account(ctx context.Context, label, authURL string) (any, error) {
	c.opening.Lock()
	defer c.opening.Unlock()
	c.mu.Lock()
	open, ok := c.accounts[label]
	c.mu.Unlock()
	if ok {
		return map[string]string{"bookmark": open.bookmark}, nil
	}
	var config swiftcontrol.Config
	var cloud realcloud.Cloud
	switch label {
	case "mock":
		config = swiftcontrol.Config{AuthURL: authURL, Username: "test:tester", APIKey: "testing"}
	case "ceph", "swift":
		path, err := realcloud.ResolveCloudsPath()
		if err == nil && path == "" {
			err = errors.New("no clouds.yaml configured")
		}
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if cloud, err = realcloud.LoadCloud(raw, realcloud.SelectBackends(label)[0].Cloud); err != nil {
			return nil, err
		}
		config = swiftcontrol.Config{AuthVersion: 3, AuthURL: cloud.AuthURL, Username: cloud.Username, APIKey: cloud.Password,
			UserDomain: cloud.UserDomain, ProjectName: cloud.ProjectName, ProjectDomain: cloud.ProjectDomain, Region: cloud.Region}
	default:
		return nil, fmt.Errorf("unknown account %q", label)
	}
	session, err := swiftcontrol.New(config)
	if err != nil {
		return nil, err
	}
	if err := session.Authenticate(ctx); err != nil {
		return nil, err
	}
	owned, err := journeys.NewOwned(session)
	if err != nil {
		return nil, err
	}
	open = openAccount{owned: owned}
	if label != "mock" {
		saved, err := c.bookmarks.Save(ctx, app.BookmarkDTO{
			DisplayName: "journey " + label, AuthVersion: 3, AuthURL: cloud.AuthURL, Username: cloud.Username,
			UserDomain: cloud.UserDomain, ProjectName: cloud.ProjectName, ProjectDomain: cloud.ProjectDomain, Region: cloud.Region,
			TLSMode: "system",
		}, cloud.Password)
		if err != nil {
			return nil, err
		}
		open.bookmark = saved.DisplayName
	}
	c.mu.Lock()
	c.accounts[label] = open
	c.mu.Unlock()
	return map[string]string{"bookmark": open.bookmark}, nil
}

// container creates a container the run owns, and claims its sidecar when
// the journey will write segments, so teardown removes both and nothing else.
func (c *journeyControl) container(ctx context.Context, label, name string, segmented bool) (any, error) {
	c.mu.Lock()
	owned := c.accounts[label].owned
	c.mu.Unlock()
	if owned == nil {
		return nil, fmt.Errorf("no account %q is open", label)
	}
	created, err := owned.CreateContainer(ctx, name)
	if err != nil {
		return nil, err
	}
	if segmented {
		if err := owned.ClaimSidecar(ctx, created); err != nil {
			return nil, err
		}
	}
	return map[string]string{"container": created}, nil
}

// namedContainer creates a container the run owns under the exact name
// another account's container was given, so a journey holds one name on
// two accounts. A name already taken there is refused and not recorded.
func (c *journeyControl) namedContainer(ctx context.Context, label, name string) (any, error) {
	c.mu.Lock()
	owned := c.accounts[label].owned
	c.mu.Unlock()
	if owned == nil {
		return nil, fmt.Errorf("no account %q is open", label)
	}
	created, err := owned.CreateNamedContainer(ctx, name)
	if err != nil {
		return nil, err
	}
	return map[string]string{"container": created}, nil
}

// names lists what one of the run's own containers holds, read by the run's
// own session and not by the application under test.
func (c *journeyControl) names(ctx context.Context, label, container string) (any, error) {
	c.mu.Lock()
	owned := c.accounts[label].owned
	c.mu.Unlock()
	if owned == nil {
		return nil, fmt.Errorf("no account %q is open", label)
	}
	names, err := owned.Names(ctx, container)
	if err != nil {
		return nil, err
	}
	return map[string][]string{"names": names}, nil
}

// put writes an object into one of the run's own containers from the run's
// own session, behind the application's back.
func (c *journeyControl) put(ctx context.Context, label, container, name, body string) (any, error) {
	c.mu.Lock()
	owned := c.accounts[label].owned
	c.mu.Unlock()
	if owned == nil {
		return nil, fmt.Errorf("no account %q is open", label)
	}
	return map[string]bool{"written": true}, owned.Put(ctx, container, name, []byte(body))
}

// teardown removes what the run created on every account it opened and
// reports how many removals failed, not why: a reason can name a host.
func (c *journeyControl) teardown(ctx context.Context) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	failures := 0
	for _, open := range c.accounts {
		failures += len(open.owned.Teardown(ctx))
	}
	c.accounts = map[string]openAccount{}
	return map[string]int{"failures": failures}
}
