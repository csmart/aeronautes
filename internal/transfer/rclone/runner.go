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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// Remote captures an upstream endpoint. The runner keeps its credentials
// and TLS policy in the parent; the child receives a scoped local gateway.
type Remote struct {
	// Name is the fixed internal remote name ("aeronautes_src"/"_dst").
	Name       string
	StorageURL string
	Token      string
	// CACertFile is a path to the captured upstream CA bundle.
	CACertFile string
	// InsecureTLS disables verification only on this upstream transport.
	InsecureTLS bool
	// AdmitRequest stays in the parent so a captured gateway cannot keep
	// using authority revoked after this remote was constructed.
	AdmitRequest func(context.Context) error `json:"-"`
}

// checkPositionals rejects paths that could read as flags — defense in
// depth beside the "--" separator on every child argv.
func checkPositionals(paths ...string) error {
	for _, p := range paths {
		if strings.HasPrefix(p, "-") {
			return fmt.Errorf("path %q starts with '-'; refused (argv hygiene)", p)
		}
	}
	return nil
}

// StageNamespace is the path every job stages under, beneath its destination
// prefix: "<prefix>.aeronautes/stage/<job>/".
const StageNamespace = ".aeronautes/stage/"

// InStageNamespace reports whether an object name lies in a staging area: the
// components ".aeronautes/stage/" at its start or after a "/". Such a name is,
// or will be, some job's unpublished copy, so no transfer publishes there and
// no deletion job sweeps an active one (ADR 0105 decision 3).
func InStageNamespace(name string) bool {
	return strings.HasPrefix(name, StageNamespace) || strings.Contains(name, "/"+StageNamespace)
}

// checkRemoteDest is the LAST boundary for a REMOTE destination (our fixed
// remote names). A live destination is the names themselves: an upload
// writes each file at its name (ADR 0122 decision 3). Any other must live
// under an .aeronautes staging namespace (K-XFER-014), which a copy still
// uses. Neither may hold a ".." segment. Local filesystem destinations
// (downloads) are governed by the local stage-and-place path instead.
func checkRemoteDest(dst string, live bool) error {
	if !strings.HasPrefix(dst, "aeronautes_") {
		return nil
	}
	_, path, _ := strings.Cut(dst, ":")
	segments := strings.Split(path, "/")
	// The staging marker must be a real path SEGMENT, not a substring of
	// a live user name, and must not smuggle a "\.\." escape back out of
	// the namespace (grok follow-up 3 P2).
	staged := false
	for i := 0; i+2 < len(segments); i++ {
		if segments[i] == ".aeronautes" && segments[i+1] == "stage" && segments[i+2] != "" {
			staged = true
			break
		}
	}
	if !live && !staged {
		return fmt.Errorf("remote destination %q is a LIVE name: this rclone write goes through an .aeronautes/stage/<job>/ path segment and commits natively (§28.6, K-XFER-014)", dst)
	}
	for _, seg := range segments {
		if seg == ".." {
			return fmt.Errorf("remote destination %q contains a '..' segment; refused", dst)
		}
	}
	return nil
}

// TrustIdentity normalizes a TLS trust configuration into a comparable
// identity: "system", "insecure", or a content hash of the CA bundle
// (the same bundle at two paths is the same trust). The pairing rule
// remains in force after moving TLS into the gateway: relaxing it
// requires a separate compatibility decision (§12.3, ADR 0014).
func TrustIdentity(insecure bool, caCertFile string) (string, error) {
	switch {
	case insecure && caCertFile != "":
		h, err := hashFile(caCertFile)
		if err != nil {
			return "", err
		}
		return "insecure+ca:" + h, nil
	case insecure:
		return "insecure", nil
	case caCertFile != "":
		h, err := hashFile(caCertFile)
		if err != nil {
			return "", err
		}
		return "ca:" + h, nil
	default:
		return "system", nil
	}
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("CA bundle %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Options configures a Runner.
type Options struct {
	// Binary "" resolves rclone from PATH.
	Binary string
	// AllowVersionMismatch skips the pin check (compatibility matrix
	// only; never the default).
	AllowVersionMismatch bool
	// NoGzipEncoding keeps stored representations opaque for Copy;
	// other callers retain the child's default transport policy.
	NoGzipEncoding bool
	// LiveDestination says that Copy's remote destination is the names
	// themselves, with no staging path: an upload writes each file at its
	// name (ADR 0122 decision 3). It is false for a copy, which still
	// stages, and has no meaning for a local destination.
	LiveDestination bool
	// KeepReplacedSegments refuses every DELETE the child sends to its
	// destination. The engine, writing over a large object, deletes that
	// object's old segments, and it does so in a container with a versions
	// location, where the store's archived version still uses them
	// (measured on the pinned engine). An upload sets this where the
	// container keeps versions or the operator's setting keeps replaced
	// segments. The engine's own removal of the segments of an upload that
	// failed is refused with the rest, so those stay as well.
	KeepReplacedSegments bool
	// IgnoreSize turns off the child's one size check after a transfer.
	// For a segmented upload that check reads the dynamic manifest's
	// size through the segment listing, once, and a listing that lags
	// behind the last segment fails a complete upload (F35); the store
	// checks each segment against the static manifest instead (ADR 0111
	// decision 2).
	IgnoreSize bool
	// ChunkSize is the §25 single knob (e.g. "5Gi"); "" leaves rclone's
	// default in place.
	ChunkSize string
	// Transfers/Checkers/TPSLimit/BWLimit are the §40 pacing surface;
	// zero values are omitted.
	Transfers int
	Checkers  int
	TPSLimit  float64
	BWLimit   string
	// BufferSize drives --buffer-size, the child's per-transfer read
	// buffer (ADR 0074 decision 3); "" leaves rclone's default in place.
	BufferSize string
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

// Resolution is the engine the runner will spawn and how it was chosen.
type Resolution struct {
	Path    string
	Version string
	// Source is one of SourceExplicit, SourceBeside, SourceLibexec or
	// SourcePath.
	Source string
	// Verified reports that the binary's sha256 matched the embedded
	// manifest, which only a sidecar is checked against; an explicit or
	// PATH binary is pinned by version alone.
	Verified bool
}

// Resolve finds and pin-checks the engine without constructing a
// Runner. Resolution order (§61.1, ADR 0089): an explicit
// Options.Binary; else a bundled sidecar — beside the executable, then
// on Linux in ../libexec/aeronautes — integrity-verified against the
// embedded manifest (a failing sidecar is an error, never a silent
// fallback); else PATH under the version pin.
func Resolve(opts Options) (Resolution, error) {
	resolution := Resolution{Path: opts.Binary, Source: SourceExplicit}
	if resolution.Path == "" {
		if dir, err := executableDir(); err == nil {
			path, source, err := resolveBundled(dir)
			if err != nil {
				return Resolution{}, err
			}
			if path != "" {
				resolution = Resolution{Path: path, Source: source, Verified: true}
			}
		}
	}
	if resolution.Path == "" {
		found, err := exec.LookPath("rclone")
		if err != nil {
			return Resolution{}, fmt.Errorf("no bundled engine beside the executable or in ../libexec/aeronautes, and pinned rclone not found on PATH: %w", err)
		}
		resolution = Resolution{Path: found, Source: SourcePath}
	}
	// The probe runs with a deadline and a minimal environment (PATH
	// plus an empty rclone config so ~/.config/rclone is never read):
	// a hung or hostile PATH binary must not hang job creation or pick
	// up ambient credentials.
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProbe()
	probe := exec.CommandContext(probeCtx, resolution.Path, "version")
	probe.Env = []string{"PATH=" + os.Getenv("PATH"), "RCLONE_CONFIG=" + os.DevNull}
	out, err := probe.Output()
	if err != nil {
		return Resolution{}, fmt.Errorf("rclone version check failed: %w", err)
	}
	m := versionPattern.FindSubmatch(out)
	if m == nil {
		return Resolution{}, fmt.Errorf("cannot parse rclone version from %q", strings.SplitN(string(out), "\n", 2)[0])
	}
	resolution.Version = string(m[1])
	if resolution.Version != PinnedVersion && !opts.AllowVersionMismatch {
		return Resolution{}, fmt.Errorf("rclone %s does not match the pin %s (§26.3); refusing", resolution.Version, PinnedVersion)
	}
	return resolution, nil
}

// NewRunner resolves and pin-checks the binary (see Resolve) and wraps
// it for spawning.
func NewRunner(opts Options) (*Runner, error) {
	resolution, err := Resolve(opts)
	if err != nil {
		return nil, err
	}
	return &Runner{binary: resolution.Path, version: resolution.Version, opts: opts}, nil
}

// Version reports the resolved binary's version.
func (r *Runner) Version() string { return r.version }

// SegmentPrefix is one captured generated prefix (§26.4).
type SegmentPrefix struct {
	Container string
	Prefix    string
}

// Canonical returns the prefix in the ONE canonical form every store
// and comparison uses: slash-terminated. rclone's X-Object-Manifest
// omits the trailing slash the log-derived capture carries; a key that
// depends on which source it came from is how the review wave's
// C01-slash sequence stayed reachable.
func (p SegmentPrefix) Canonical() SegmentPrefix {
	if !strings.HasSuffix(p.Prefix, "/") {
		p.Prefix += "/"
	}
	return p
}

// PrefixFromEvent extracts the generated segment prefix from one child
// log event, when the line is a segment-upload notice — the streamed
// form of capture, so callers can persist the fact AT OBSERVATION TIME
// instead of only after child exit (§65.9 item 5).
func PrefixFromEvent(event Event) (SegmentPrefix, bool) {
	m := segmentPattern.FindStringSubmatch(event.Msg)
	if m == nil {
		return SegmentPrefix{}, false
	}
	name := m[1]
	slash := strings.LastIndex(name, "/")
	if slash <= 0 {
		return SegmentPrefix{}, false
	}
	return SegmentPrefix{Container: m[2], Prefix: name[:slash+1]}, true
}

// CopiedObject reports the object a child log line says the engine wrote:
// its own line for one file it copied, new or over an existing object, named
// as the file was listed to it. A listed file with no such line was not
// written, whatever the child's exit status: measured against the pinned
// engine, a listed name that is missing at the source gets no line and the
// child still exits 0.
func CopiedObject(event Event) (string, bool) {
	if event.Level != "info" || event.Object == "" || !strings.HasPrefix(event.Msg, "Copied (") {
		return "", false
	}
	return event.Object, true
}

// Answers is what the engine said about the objects of one copy, by the name
// each was listed to it under: the ones it reported writing, and the error it
// reported for one it could not write.
type Answers struct {
	Copied  map[string]bool
	Refused map[string]string
}

func NewAnswers() Answers {
	return Answers{Copied: map[string]bool{}, Refused: map[string]string{}}
}

// Hear takes one child log event. An object's first error line is the one
// kept.
func (a Answers) Hear(event Event) {
	if name, ok := CopiedObject(event); ok {
		a.Copied[name] = true
		return
	}
	if event.Level == "error" && event.Object != "" && a.Refused[event.Object] == "" {
		a.Refused[event.Object] = event.Msg
	}
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
	// LogStreamErr is a stderr scanner failure: the prefix-capture
	// stream may be INCOMPLETE, so callers must treat the child as
	// unverified (§26.4).
	LogStreamErr string
	Stats        Stats
}

// Copy runs one bounded batch: `rclone copy src dst` over exactly the
// listed names (§28.4 selected-set contract). Every name must be
// expressible in a newline-delimited raw list — the planner routes
// newline-bearing names natively, and the runner refuses them outright.
func (r *Runner) Copy(ctx context.Context, remotes []Remote, src, dst string, names []string, onEvent func(Event)) (*Result, error) {
	return r.copy(ctx, remotes, src, dst, names, onEvent, nil)
}

// CopyInto runs the same bounded batch with the child's destination
// anchored to a held directory: the child inherits the descriptor and
// resolves every write through it, so renaming any ancestor while the
// copy runs cannot redirect the child. AnchorSupported says whether
// this platform provides the anchor; elsewhere the call refuses.
func (r *Runner) CopyInto(ctx context.Context, remotes []Remote, src string, anchor *os.File, names []string, onEvent func(Event)) (*Result, error) {
	if !AnchorSupported {
		return nil, fmt.Errorf("descriptor-anchored destinations are not available on this platform")
	}
	if anchor == nil {
		return nil, fmt.Errorf("anchored copy needs the held destination directory")
	}
	return r.copy(ctx, remotes, src, anchoredDestination, names, onEvent, anchor)
}

func (r *Runner) copy(ctx context.Context, remotes []Remote, src, dst string, names []string, onEvent func(Event), anchor *os.File) (*Result, error) {
	// Refuse-closed: an empty selected set would make rclone copy the
	// ENTIRE source scope — the caller must always say exactly what
	// moves (§28.4).
	if len(names) == 0 {
		return nil, fmt.Errorf("empty selected set: rclone copy without --files-from-raw would copy the whole scope; refuse (§28.4)")
	}
	if err := checkPositionals(src, dst); err != nil {
		return nil, err
	}
	if err := checkRemoteDest(dst, r.opts.LiveDestination); err != nil {
		return nil, err
	}
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

	localRemotes, closeGateways, err := startGateways(ctx, remotes, dst,
		destinationRules{live: r.opts.LiveDestination, keepsObjects: r.opts.KeepReplacedSegments}, r.gatewayCapacity())
	if err != nil {
		return nil, err
	}
	defer closeGateways()
	configPath, env, err := buildEnvironment(workDir, localRemotes)
	if err != nil {
		return nil, err
	}

	listPath := filepath.Join(workDir, "files-from-raw")
	if err := os.WriteFile(listPath, []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	// A filtered source traversal can lose planned names on short pages;
	// files-from-raw plus no-traverse keeps the application's selection.
	args := []string{"copy", "--no-traverse", "--ignore-times", "--use-json-log", "-vv", "--stats", "1s",
		"--files-from-raw", listPath, "--config", configPath}
	args = append(args, r.pacingArgs()...)
	if r.opts.NoGzipEncoding {
		args = append(args, "--no-gzip-encoding")
	}
	if r.opts.IgnoreSize {
		args = append(args, "--ignore-size")
	}
	// Flags close before "--": nothing in a path can ever be parsed as
	// a flag, whatever it starts with.
	args = append(args, "--", src, dst)

	var inherited []*os.File
	if anchor != nil {
		inherited = []*os.File{anchor}
	}
	return r.run(ctx, args, env, onEvent, inherited)
}

// CopyTo runs `rclone copyto src dst` for ONE object under a different
// destination name — the §28.6 staging write (the staged name is always
// Aeronautes-generated and unique, so rclone's Update never runs against
// a live name). Same hygiene surface as Copy.
func (r *Runner) CopyTo(ctx context.Context, remotes []Remote, src, dst string, onEvent func(Event)) (*Result, error) {
	if strings.ContainsAny(src+dst, "\n\r") {
		return nil, fmt.Errorf("newline-bearing path cannot ride rclone argv; route it natively (§14.3)")
	}
	if err := checkPositionals(src, dst); err != nil {
		return nil, err
	}
	if err := checkRemoteDest(dst, false); err != nil {
		return nil, err
	}
	workDir, err := os.MkdirTemp("", "aeronautes-job-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	localRemotes, closeGateways, err := startGateways(ctx, remotes, dst, destinationRules{exact: true}, r.gatewayCapacity())
	if err != nil {
		return nil, err
	}
	defer closeGateways()
	configPath, env, err := buildEnvironment(workDir, localRemotes)
	if err != nil {
		return nil, err
	}
	args := []string{"copyto", "--ignore-times", "--use-json-log", "-vv", "--stats", "1s", "--config", configPath}
	args = append(args, r.pacingArgs()...)
	if r.opts.NoGzipEncoding {
		args = append(args, "--no-gzip-encoding")
	}
	if r.opts.IgnoreSize {
		args = append(args, "--ignore-size")
	}
	args = append(args, "--", src, dst)
	return r.run(ctx, args, env, onEvent, nil)
}

func (r *Runner) gatewayCapacity() int {
	return min(1024, max(64, 2*(max(0, r.opts.Transfers)+max(0, r.opts.Checkers))+16))
}

// DefaultLowLevelRetries is the value pacingArgs passes to
// --low-level-retries when the caller sets none.
//
// It bounds the TOTAL attempts the engine makes at one request, not
// retries on top of a first try, which the flag's name suggests and the
// pinned engine does not do. Measured against rclone v1.74.3: with 2, one
// injected refusal produced two PUTs and two or three injected refusals
// still produced only two; with 1, one injected refusal produced a single
// PUT and the copy failed. So 2 means one try and at most one retry, and 1
// means one try and none. A test that counts the engine's writes bounds
// them by this number itself rather than by one more than it.
const DefaultLowLevelRetries = 2

func (r *Runner) pacingArgs() []string {
	o := r.opts
	retries, lowLevel := o.Retries, o.LowLevelRetries
	if retries <= 0 {
		retries = 1
	}
	if lowLevel <= 0 {
		lowLevel = DefaultLowLevelRetries
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
	if o.BufferSize != "" {
		args = append(args, "--buffer-size", o.BufferSize)
	}
	return args
}

// The child sees only loopback remotes and local tokens; an empty config
// prevents it from discovering the operator's own configured remotes.
func buildEnvironment(workDir string, remotes []Remote) (configPath string, env []string, err error) {
	// Whitelist, never inherit: the operator's environment must not
	// steer the child (§26.2).
	env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + workDir}

	for _, remote := range remotes {
		if remote.Name == "" || remote.StorageURL == "" || remote.Token == "" {
			return "", nil, fmt.Errorf("remote needs name, storage URL, and token")
		}
		upper := strings.ToUpper(remote.Name)
		env = append(env,
			fmt.Sprintf("RCLONE_CONFIG_%s_AUTH_TOKEN=%s", upper, remote.Token),
			fmt.Sprintf("RCLONE_CONFIG_%s_TYPE=swift", upper),
			fmt.Sprintf("RCLONE_CONFIG_%s_ENV_AUTH=false", upper),
			fmt.Sprintf("RCLONE_CONFIG_%s_STORAGE_URL=%s", upper, remote.StorageURL),
		)
	}

	configPath = filepath.Join(workDir, "rclone.conf")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		return "", nil, err
	}
	return configPath, env, nil
}

// segmentPattern matches the §26.4 capture line at -vv.
var segmentPattern = regexp.MustCompile(`Uploading segment file "(.+)" into "(.+)"`)

const maxCollectedErrors = 50

const childCancelGrace = 10 * time.Second

// run spawns, streams, and reaps one child.
func (r *Runner) run(ctx context.Context, args, env []string, onEvent func(Event), inherited []*os.File) (*Result, error) {
	// The child gets its own lifetime: cancellation is OUR signal
	// escalation (SIGINT then SIGKILL), not a blunt kill from exec.
	cmd := exec.Command(r.binary, args...)
	cmd.SysProcAttr = childSysProcAttr()
	cmd.Env = env
	// Inherited descriptors start at 3 in the child, in this order.
	cmd.ExtraFiles = inherited
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
		defer func() {
			if scanErr := scanner.Err(); scanErr != nil {
				mu.Lock()
				result.LogStreamErr = scanErr.Error()
				result.Errors = append(result.Errors, "log stream: "+scanErr.Error())
				mu.Unlock()
				// Keep DRAINING: a stopped reader would block the child
				// on a full pipe and Wait would never run (review F-14).
				_, _ = io.Copy(io.Discard, stderr)
			}
		}()
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
			if p, ok := PrefixFromEvent(*event); ok {
				prefixes[p] = true
			}
			mu.Unlock()
			if onEvent != nil {
				onEvent(*event)
			}
		}
	}()

	// cmd.Wait closes the stderr pipe, so it must not run until the
	// scanner has drained to EOF — otherwise the tail of the log stream
	// (segment-prefix events included) is silently lost. The child's
	// exit (natural or killed) EOFs the pipe and releases the scanner.
	waitDone := make(chan error, 1)
	go func() {
		<-parseDone
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-ctx.Done():
		result.Cancelled = true
		// Graceful first (§26.4: rclone cleans up on interrupt — best
		// effort), escalate if it lingers.
		signalChild(cmd, syscall.SIGINT)
		select {
		case waitErr = <-waitDone:
		case <-time.After(childCancelGrace):
			killChild(cmd)
			waitErr = <-waitDone
		}
	}
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
