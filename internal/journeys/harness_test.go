//go:build realcloud

package journeys

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/realcloud"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// account is one of the two real clouds as the journeys use it: the
// bookmark the application connects with, a separate session that checks
// every result independently, and the record of what the run created.
type account struct {
	label      string
	bookmarkID string
	check      *swiftcontrol.Session
	owned      *Owned
}

// world is the application as main.go wires it, over one temporary
// directory, with the two clouds as bookmarks (docs/journeys.md, Harness).
type world struct {
	t         *testing.T
	ctx       context.Context
	dir       string
	picker    *picker
	bookmarks *app.BookmarkService
	browse    *app.BrowseService
	inspector *app.InspectorService
	mutate    *app.MutateService
	transfer  *app.TransferService
	delete    *app.DeleteService
	jobs      *app.JobService
	settings  *app.SettingsService
	accounts  map[string]*account
}

// picker stands in for the native file dialogs; a step sets what it returns.
type picker struct {
	files []string
	dir   string
}

func (p *picker) PickFiles(string) ([]string, error)   { return append([]string(nil), p.files...), nil }
func (p *picker) PickDirectory(string) (string, error) { return p.dir, nil }

// yes answers every native confirmation as the operator would to go on.
type yes struct{}

func (yes) ConfirmOverwrite(context.Context, app.OverwriteConfirmation) (bool, error) {
	return true, nil
}
func (yes) ConfirmPublicRead(context.Context, string, string) (bool, error) { return true, nil }

// newWorld builds the application and connects both clouds, or skips when
// no clouds.yaml or either entry is configured. The application's state
// stays in the test's temporary directory: the settings file and the
// database are given explicit paths in it, and the secret store lives for
// the session. The harness's own files outside it are the private log and
// job error files under the user cache directory; a transfer's engine
// scratch directory is the application's, removed when the job returns.
func newWorld(t *testing.T) *world {
	t.Helper()
	path, err := realcloud.ResolveCloudsPath()
	if err != nil {
		t.Fatalf("clouds.yaml source: %v", err)
	}
	if path == "" {
		t.Skip("no clouds.yaml configured (set ORACLE_CLOUDS); skipping the journeys")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the selected clouds.yaml could not be read (%s)", realcloud.Failure(err))
	}
	clouds := map[string]realcloud.Cloud{}
	for _, b := range realcloud.Backends {
		c, err := realcloud.LoadCloud(raw, b.Cloud)
		if err != nil {
			t.Skipf("the %s entry is not usable (%s); the journeys need both clouds", b.Label, realcloud.Failure(err))
		}
		clouds[b.Label] = c
	}

	// The application's own log names the hosts it talks to, the storage
	// gateway's retry warnings among them, so it goes to a private file
	// beside the job errors and never into the journey output.
	logPath, err := privateFile("app.log")
	if err != nil {
		t.Fatalf("the application log has nowhere private to go (%s)", realcloud.Failure(err))
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("the application log has nowhere private to go (%s)", realcloud.Failure(err))
	}
	restoreLog := applog.Redirect(logFile)
	t.Cleanup(func() {
		restoreLog()
		logFile.Close()
	})
	t.Logf("the application log is kept in %s", logPath)

	ctx := context.Background()
	dir := t.TempDir()
	database, err := db.Open(ctx, filepath.Join(dir, "aeronautes.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store := bookmarks.NewStore(database, secrets.NewSessionStore())
	connections := app.NewConnections(store)
	app.WireRememberedSettings(connections, database)
	ev := evidence.NewStore(database)
	auditStore := audit.NewStore(database)
	jobStore := jobs.NewStore(database)
	runner := app.NewJobRunner(jobStore, auditStore)
	transfer := app.NewTransferServiceWithRunner(connections, jobStore, runner)
	app.WireEvidenceStore(transfer, ev)
	settings := app.NewSettingsService(filepath.Join(dir, "settings.json"), runner)
	app.WireSettings(transfer, settings)
	pick := &picker{}
	app.WireFilePicker(transfer, pick)
	app.WireOverwriteConfirmer(transfer, yes{})
	mutate := app.NewMutateService(connections, auditStore, ev)
	app.WireConfirmer(mutate, yes{})
	deletes := app.NewDeleteService(connections, jobStore, runner, ev)
	app.WireDeleteSettings(deletes, settings)
	w := &world{
		t: t, ctx: ctx, dir: dir, picker: pick,
		bookmarks: app.NewBookmarkService(store, connections, jobStore),
		browse:    app.NewBrowseService(connections, ev),
		inspector: app.NewInspectorService(connections, ev),
		mutate:    mutate,
		transfer:  transfer,
		delete:    deletes,
		jobs:      app.NewJobServiceWithRunner(jobStore, runner),
		settings:  settings,
		accounts:  map[string]*account{},
	}
	for _, b := range realcloud.Backends {
		w.accounts[b.Label] = w.connect(b.Label, clouds[b.Label])
	}
	return w
}

func (w *world) connect(label string, c realcloud.Cloud) *account {
	w.t.Helper()
	check, err := swiftcontrol.New(swiftcontrol.Config{
		AuthVersion: 3, AuthURL: c.AuthURL, Username: c.Username, APIKey: c.Password,
		UserDomain: c.UserDomain, ProjectName: c.ProjectName, ProjectDomain: c.ProjectDomain, Region: c.Region,
	})
	if err != nil {
		w.t.Fatalf("%s: check session: %s", label, realcloud.Failure(err))
	}
	w.t.Cleanup(func() { check.Close() })
	if err := check.Authenticate(w.ctx); err != nil {
		w.t.Fatalf("%s: check session sign-in: %s", label, realcloud.Failure(err))
	}
	owned, err := NewOwned(check)
	if err != nil {
		w.t.Fatal(err)
	}
	// Registered before anything is created, so a failure at any later step
	// still removes what the run made, on a context of its own.
	w.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		for _, err := range owned.Teardown(ctx) {
			w.t.Errorf("%s teardown: %s", label, realcloud.Failure(err))
		}
	})
	saved, err := w.bookmarks.Save(w.ctx, app.BookmarkDTO{
		DisplayName: "journey " + label, AuthVersion: 3, AuthURL: c.AuthURL, Username: c.Username,
		UserDomain: c.UserDomain, ProjectName: c.ProjectName, ProjectDomain: c.ProjectDomain, Region: c.Region,
		TLSMode: "system",
	}, c.Password)
	if err != nil {
		w.t.Fatalf("%s: bookmark: %s", label, realcloud.Failure(err))
	}
	return &account{label: label, bookmarkID: saved.ID, check: check, owned: owned}
}

// container creates a container on the account for this run.
func (w *world) container(a *account, label string) string {
	w.t.Helper()
	name, err := a.owned.CreateContainer(w.ctx, label)
	if err != nil {
		w.t.Fatalf("%s: create container: %s", a.label, realcloud.Failure(err))
	}
	return name
}

// containerRows lists every container of the connected account, page by
// page, as the container column loads them.
func (w *world) containerRows(a *account) []app.ContainerRowDTO {
	w.t.Helper()
	var rows []app.ContainerRowDTO
	marker := ""
	for {
		page, err := w.browse.Containers(w.ctx, a.bookmarkID, marker, 1000)
		if err != nil {
			w.t.Fatalf("%s: list containers: %s", a.label, realcloud.Failure(err))
		}
		rows = append(rows, page.Rows...)
		if page.NextMarker == "" {
			return rows
		}
		marker = page.NextMarker
	}
}

// await waits for a job to end and returns it.
func (w *world) await(a *account, jobID string) *app.JobDTO {
	w.t.Helper()
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		job, err := w.jobs.Get(w.ctx, a.bookmarkID, jobID)
		if err != nil {
			w.t.Fatalf("%s: job: %s", a.label, realcloud.Failure(err))
		}
		switch job.State {
		case "complete", "partial", "failed", "cancelled":
			return job
		}
		time.Sleep(500 * time.Millisecond)
	}
	w.t.Fatalf("%s: job %s never ended", a.label, jobID)
	return nil
}

// privateFile names a file in a private directory under the user's cache
// directory, outside the repository, for what the journey output must not
// print.
func privateFile(name string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "aeronautes-journeys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, time.Now().UTC().Format("20060102T150405Z")+"-"+name), nil
}

// ended fails the step when a job did not complete. The job's own error can
// name a provider's host, so it is not printed: it is kept in a private
// file, and the line names only the step, the state and that file.
func (w *world) ended(step string, job *app.JobDTO, want string) {
	w.t.Helper()
	if job.State == want {
		return
	}
	file, err := privateFile(job.ID + ".txt")
	if err == nil {
		err = os.WriteFile(file, []byte(job.Error+"\n"), 0o600)
	}
	if err != nil {
		w.t.Fatalf("%s ended %s; its error could not be kept (%s)", step, job.State, realcloud.Failure(err))
	}
	w.t.Fatalf("%s ended %s, not %s; its error is kept in %s", step, job.State, want, file)
}

// fixture writes count files of the given sizes into a fresh folder and
// returns it with each name's digest.
func (w *world) fixture(sizes ...int) (string, map[string][32]byte) {
	w.t.Helper()
	dir := filepath.Join(w.dir, "fixture-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	digests := map[string][32]byte{}
	for i, size := range sizes {
		name := fmt.Sprintf("file%02d.bin", i)
		body := make([]byte, size)
		for j := range body {
			body[j] = byte((i*31 + j*7) % 251)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			w.t.Fatal(err)
		}
		digests[name] = sha256.Sum256(body)
	}
	return dir, digests
}

// stored reads every object under prefix in a container through the
// independent session and returns each name, relative to prefix, with the
// digest of its content.
func (w *world) stored(a *account, container, prefix string) map[string][32]byte {
	w.t.Helper()
	out := map[string][32]byte{}
	var names []string
	for row, err := range a.check.Objects(w.ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			w.t.Fatalf("%s: list: %s", a.label, realcloud.Failure(err))
		}
		if object, ok := row.(swiftcontrol.ObjectEntry); ok {
			names = append(names, object.Name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := a.check.OpenObjectContent(w.ctx, container, name)
		if swiftcontrol.IsNotFound(err) {
			// Listed but not yet readable, or already gone: a lagging
			// listing, which the caller's settled check reads again.
			continue
		}
		if err != nil {
			w.t.Fatalf("%s: read: %s", a.label, realcloud.Failure(err))
		}
		hash := sha256.New()
		_, err = io.Copy(hash, body)
		body.Close()
		if err != nil {
			w.t.Fatalf("%s: read: %s", a.label, realcloud.Failure(err))
		}
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		out[name[len(prefix):]] = sum
	}
	return out
}

// settled repeats a check that reads a listing until it reports no problem
// or a minute passes, and returns the last problem. Listings are eventually
// consistent (AGENTS.md: verify with bounded retries), so the first read can
// lag behind a write that has already completed.
func (w *world) settled(check func() string) string {
	deadline := time.Now().Add(time.Minute)
	for {
		problem := check()
		if problem == "" || time.Now().After(deadline) {
			return problem
		}
		time.Sleep(2 * time.Second)
	}
}

// same reports how two name-to-digest sets differ, or "" when they match;
// it names files, which the run made, never content.
func same(got, want map[string][32]byte) string {
	var problems []string
	for name, digest := range want {
		if got[name] != digest {
			problems = append(problems, name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			problems = append(problems, "extra "+name)
		}
	}
	sort.Strings(problems)
	if len(problems) == 0 {
		return ""
	}
	return fmt.Sprint(problems)
}
