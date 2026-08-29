// Package rclone is the pinned-subprocess manager (§26, ADR 0003): it
// spawns rclone with exactly the enumerated docs/rclone.md surface —
// tokens in the environment and never argv, a job-scoped config that
// carries no secrets, the selected-set contract, bounded retries — and
// streams the child's JSON log for progress, errors, and the generated
// segment prefixes that feed ownership evidence (§26.4).
package rclone

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// PinnedVersion is the §26.3 pin; a different binary refuses unless the
// caller explicitly allows it (the future compatibility matrix).
const PinnedVersion = "v1.74.3"

// Remote is one endpoint of a child. The token travels in the child
// environment only; when TLS settings are present the remote's
// NON-SECRET keys move into the job config file (override.* is honoured
// only there, §12.3) while the token stays in the environment.
type Remote struct {
	// Name is the fixed internal remote name ("aeronautes_src"/"_dst").
	Name       string
	StorageURL string
	Token      string
	// CACertFile is a path to a PEM bundle for override.ca_cert.
	CACertFile string
	// InsecureTLS sets override.no_check_certificate.
	InsecureTLS bool
}

func (r Remote) needsConfigFile() bool {
	return r.CACertFile != "" || r.InsecureTLS
}

// Options configures a Runner.
type Options struct {
	// Binary "" resolves rclone from PATH.
	Binary string
	// AllowVersionMismatch skips the pin check (compatibility matrix
	// only; never the default).
	AllowVersionMismatch bool
	// ChunkSize is the §25 single knob (e.g. "5Gi"); "" leaves rclone's
	// default in place.
	ChunkSize string
	// Transfers/Checkers/TPSLimit/BWLimit are the §40 pacing surface;
	// zero values are omitted.
	Transfers int
	Checkers  int
	TPSLimit  float64
	BWLimit   string
	// Retries/LowLevelRetries bound the failure budget (§26.2); zero
	// means the measured-fast defaults 1/2.
	Retries         int
	LowLevelRetries int
}

// Runner spawns rclone children.
type Runner struct {
	binary  string
	version string
	opts    Options
}

var versionPattern = regexp.MustCompile(`rclone (v[0-9][^\s]*)`)

// NewRunner resolves and pin-checks the binary.
func NewRunner(opts Options) (*Runner, error) {
	binary := opts.Binary
	if binary == "" {
		found, err := exec.LookPath("rclone")
		if err != nil {
			return nil, fmt.Errorf("pinned rclone not found on PATH: %w", err)
		}
		binary = found
	}
	out, err := exec.Command(binary, "version").Output()
	if err != nil {
		return nil, fmt.Errorf("rclone version check failed: %w", err)
	}
	m := versionPattern.FindSubmatch(out)
	if m == nil {
		return nil, fmt.Errorf("cannot parse rclone version from %q", strings.SplitN(string(out), "\n", 2)[0])
	}
	version := string(m[1])
	if version != PinnedVersion && !opts.AllowVersionMismatch {
		return nil, fmt.Errorf("rclone %s does not match the pin %s (§26.3); refusing", version, PinnedVersion)
	}
	return &Runner{binary: binary, version: version, opts: opts}, nil
}

// Version reports the resolved binary's version.
func (r *Runner) Version() string { return r.version }

// SegmentPrefix is one captured generated prefix (§26.4).
type SegmentPrefix struct {
	Container string
	Prefix    string
}

// Stats is the latest progress snapshot from the child.
type Stats struct {
	Bytes          int64
	TotalBytes     int64
	Transfers      int64
	TotalTransfers int64
	Speed          float64
	Errors         int64
}

// Event is one significant child log line.
type Event struct {
	Level  string
	Msg    string
	Object string
	// Stats is non-nil on periodic stats lines.
	Stats *Stats
}

// Result summarises a finished child.
type Result struct {
	ExitCode int
	// Cancelled reports that the context ended the child.
	Cancelled bool
	// SegmentPrefixes are the unique generated prefixes observed.
	SegmentPrefixes []SegmentPrefix
	// Errors are the child's error-level messages (bounded).
	Errors []string
	Stats  Stats
}

// Copy runs one bounded batch: `rclone copy src dst` over exactly the
// listed names (§28.4 selected-set contract). Every name must be
// expressible in a newline-delimited raw list — the planner routes
// newline-bearing names natively, and the runner refuses them outright.
func (r *Runner) Copy(ctx context.Context, remotes []Remote, src, dst string, names []string, onEvent func(Event)) (*Result, error) {
	for _, name := range names {
		if strings.ContainsAny(name, "\n\r") {
			return nil, fmt.Errorf("name %q cannot ride an rclone list (newline); route it natively (§14.3)", name)
		}
	}

	workDir, err := os.MkdirTemp("", "aeronautes-job-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	configPath, env, err := buildEnvironment(workDir, remotes)
	if err != nil {
		return nil, err
	}

	args := []string{"copy", src, dst, "--ignore-times", "--use-json-log", "-vv", "--stats", "1s"}
	if len(names) > 0 {
		listPath := filepath.Join(workDir, "files-from-raw")
		if err := os.WriteFile(listPath, []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--files-from-raw", listPath)
	}
	args = append(args, "--config", configPath)
	args = append(args, r.pacingArgs()...)

	return r.run(ctx, args, env, onEvent)
}

func (r *Runner) pacingArgs() []string {
	o := r.opts
	retries, lowLevel := o.Retries, o.LowLevelRetries
	if retries <= 0 {
		retries = 1
	}
	if lowLevel <= 0 {
		lowLevel = 2
	}
	args := []string{
		"--retries", fmt.Sprint(retries),
		"--low-level-retries", fmt.Sprint(lowLevel),
	}
	if o.ChunkSize != "" {
		args = append(args, "--swift-chunk-size", o.ChunkSize)
	}
	if o.Transfers > 0 {
		args = append(args, "--transfers", fmt.Sprint(o.Transfers))
	}
	if o.Checkers > 0 {
		args = append(args, "--checkers", fmt.Sprint(o.Checkers))
	}
	if o.TPSLimit > 0 {
		args = append(args, "--tpslimit", fmt.Sprint(o.TPSLimit))
	}
	if o.BWLimit != "" {
		args = append(args, "--bwlimit", o.BWLimit)
	}
	return args
}

// buildEnvironment assembles the whitelisted child environment and the
// job config file. Remotes without TLS needs are pure env; a remote
// with TLS settings puts its NON-SECRET keys in the config file
// (override.* is config-file-only, §12.3) — its token still travels in
// the environment, merged by rclone.
func buildEnvironment(workDir string, remotes []Remote) (configPath string, env []string, err error) {
	// Whitelist, never inherit: the operator's environment must not
	// steer the child (§26.2).
	env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + workDir}

	var configBody strings.Builder
	needsFile := false
	for _, remote := range remotes {
		if remote.Name == "" || remote.StorageURL == "" || remote.Token == "" {
			return "", nil, fmt.Errorf("remote needs name, storage URL, and token")
		}
		upper := strings.ToUpper(remote.Name)
		env = append(env, fmt.Sprintf("RCLONE_CONFIG_%s_AUTH_TOKEN=%s", upper, remote.Token))
		if remote.needsConfigFile() {
			needsFile = true
			fmt.Fprintf(&configBody, "[%s]\ntype = swift\nenv_auth = false\nstorage_url = %s\n", remote.Name, remote.StorageURL)
			if remote.CACertFile != "" {
				fmt.Fprintf(&configBody, "override.ca_cert = %s\n", remote.CACertFile)
			}
			if remote.InsecureTLS {
				configBody.WriteString("override.no_check_certificate = true\n")
			}
			configBody.WriteString("\n")
		} else {
			env = append(env,
				fmt.Sprintf("RCLONE_CONFIG_%s_TYPE=swift", upper),
				fmt.Sprintf("RCLONE_CONFIG_%s_ENV_AUTH=false", upper),
				fmt.Sprintf("RCLONE_CONFIG_%s_STORAGE_URL=%s", upper, remote.StorageURL),
			)
		}
	}

	// Mixed TLS trust in one child is forbidden (§12.3): the override
	// leaks. The caller (planner) splits such jobs; this is the last line.
	tlsRemotes := 0
	for _, remote := range remotes {
		if remote.needsConfigFile() {
			tlsRemotes++
		}
	}
	if tlsRemotes > 0 && tlsRemotes != len(remotes) {
		return "", nil, fmt.Errorf("mixed TLS trust in one child is forbidden (§12.3): split into two single-remote children")
	}

	configPath = filepath.Join(workDir, "rclone.conf")
	if needsFile {
		if err := os.WriteFile(configPath, []byte(configBody.String()), 0o600); err != nil {
			return "", nil, err
		}
	} else {
		// An empty file, not /dev/null: rclone must see a config that
		// exists and contains nothing of the operator's.
		if err := os.WriteFile(configPath, nil, 0o600); err != nil {
			return "", nil, err
		}
	}
	return configPath, env, nil
}

// segmentPattern matches the §26.4 capture line at -vv.
var segmentPattern = regexp.MustCompile(`Uploading segment file "(.+)" into "(.+)"`)

const maxCollectedErrors = 50

// run spawns, streams, and reaps one child.
func (r *Runner) run(ctx context.Context, args, env []string, onEvent func(Event)) (*Result, error) {
	// The child gets its own lifetime: cancellation is OUR signal
	// escalation (SIGINT then SIGKILL), not a blunt kill from exec.
	cmd := exec.Command(r.binary, args...)
	cmd.Env = env
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	result := &Result{}
	prefixes := map[SegmentPrefix]bool{}
	var mu sync.Mutex

	parseDone := make(chan struct{})
	go func() {
		defer close(parseDone)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			event := parseLine(scanner.Bytes())
			if event == nil {
				continue
			}
			mu.Lock()
			if event.Stats != nil {
				result.Stats = *event.Stats
			}
			if event.Level == "error" && len(result.Errors) < maxCollectedErrors {
				result.Errors = append(result.Errors, event.Msg)
			}
			if m := segmentPattern.FindStringSubmatch(event.Msg); m != nil {
				name := m[1]
				if slash := strings.LastIndex(name, "/"); slash > 0 {
					prefixes[SegmentPrefix{Container: m[2], Prefix: name[:slash+1]}] = true
				}
			}
			mu.Unlock()
			if onEvent != nil {
				onEvent(*event)
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-ctx.Done():
		result.Cancelled = true
		// Graceful first (§26.4: rclone cleans up on interrupt — best
		// effort), escalate if it lingers.
		_ = cmd.Process.Signal(syscall.SIGINT)
		select {
		case waitErr = <-waitDone:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			waitErr = <-waitDone
		}
	}
	<-parseDone

	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	} else if waitErr != nil {
		return result, waitErr
	}
	for prefix := range prefixes {
		result.SegmentPrefixes = append(result.SegmentPrefixes, prefix)
	}
	return result, nil
}

// parseLine decodes one JSON log line; non-JSON lines are ignored.
func parseLine(line []byte) *Event {
	var raw struct {
		Level  string `json:"level"`
		Msg    string `json:"msg"`
		Object string `json:"object"`
		Stats  *struct {
			Bytes          int64   `json:"bytes"`
			TotalBytes     int64   `json:"totalBytes"`
			Transfers      int64   `json:"transfers"`
			TotalTransfers int64   `json:"totalTransfers"`
			Speed          float64 `json:"speed"`
			Errors         int64   `json:"errors"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}
	event := &Event{Level: raw.Level, Msg: raw.Msg, Object: raw.Object}
	if raw.Stats != nil {
		event.Stats = &Stats{
			Bytes:          raw.Stats.Bytes,
			TotalBytes:     raw.Stats.TotalBytes,
			Transfers:      raw.Stats.Transfers,
			TotalTransfers: raw.Stats.TotalTransfers,
			Speed:          raw.Stats.Speed,
			Errors:         raw.Stats.Errors,
		}
	}
	return event
}
