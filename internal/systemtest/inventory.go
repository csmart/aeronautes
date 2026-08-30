package systemtest

// The action inventory (§65.10 rule 1): every bound bridge method is a
// row with its kind and the scenarios the war room must exercise. Two
// guards keep it honest: a reflection guard fails the build when a bound
// service exports a method with no row (so no action is un-inventoried),
// and TestInventoryIsWellFormed rejects an invalid kind or an empty
// requirement set (so no row can contribute zero to the denominator and
// silently look complete). Together they make the DENOMINATOR sound; they
// do NOT claim the scenarios are COVERED — actual coverage is the
// evidence-based numerator reconciled by cmd/scenariocov from emitted
// claims and terminal test results, and today it is far from full.
//
// The kind defaults are a conservative FLOOR, not DESIGN §65.10's literal
// blanket rule (happy+refusal for all 47, plus conflict/cancel/partial/
// retry for every mutating/destructive action ≈ 162): a synchronous
// atomic mutate has no "partial", so a per-action Scenarios override
// states the real contract where the default is wrong. The floor, the
// literal-162 baseline, and the far larger full matrix (K-*, data class,
// account direction, hostile names, paging, fault points, UI, platform)
// are all above this number — it is a floor to grow from, never a
// completeness claim.

// Kind classifies what a bridge method does, which fixes its required
// scenarios.
type Kind string

const (
	// Read: read-only, needs a happy path (and an empty/boundary case
	// where a listing or preview).
	Read Kind = "read"
	// Session: connection/picker plumbing, needs a happy path.
	Session Kind = "session"
	// Mutate: changes one object/container, needs happy + refusal.
	Mutate Kind = "mutate"
	// Destructive: a single synchronous op that deletes or moves data,
	// needs happy + refusal + conflict.
	Destructive Kind = "destructive"
	// Job: an asynchronous transfer/delete that can partially apply, be
	// cancelled mid-flight, or be retried — needs the full lifecycle
	// (DESIGN §65.10: conflict, cancellation, partial-failure, retry).
	// Kept distinct from Destructive so a bookmark delete is not asked
	// for a nonsensical "partial" scenario (grok war-room P2).
	Job Kind = "job"
)

// Action is one inventory row.
type Action struct {
	Kind Kind
	// Scenarios, when set, overrides the kind's default required list for
	// this specific action — for methods whose real contract does not fit
	// the five-kind shortcut (codex war-room H-03). Empty means "use the
	// kind default".
	Scenarios []string
}

// required is the scenario list this action must cover: its explicit
// per-action override if set, otherwise its kind's default.
func (a Action) required() []string {
	if len(a.Scenarios) > 0 {
		return a.Scenarios
	}
	return a.Kind.requiredScenarios()
}

// requiredScenarios maps a kind to the scenarios every action of that
// kind must have covered before it is "complete".
func (k Kind) requiredScenarios() []string {
	switch k {
	case Read:
		return []string{"happy"}
	case Session:
		return []string{"happy"}
	case Mutate:
		return []string{"happy", "refusal"}
	case Destructive:
		return []string{"happy", "refusal", "conflict"}
	case Job:
		return []string{"happy", "refusal", "conflict", "cancel", "partial", "retry"}
	default:
		return nil
	}
}

// Inventory is the authoritative bridge-action list, keyed
// "Service.Method". Editing a bound service's surface without updating
// this map fails the guard.
var Inventory = map[string]Action{
	// AppService
	"AppService.About": {Kind: Read},
	"AppService.Info":  {Kind: Read},
	// BookmarkService
	"BookmarkService.List":   {Kind: Read},
	"BookmarkService.Save":   {Kind: Mutate},
	"BookmarkService.Delete": {Kind: Destructive},
	// BrowseService
	"BrowseService.Connect":      {Kind: Session},
	"BrowseService.Account":      {Kind: Read},
	"BrowseService.Capabilities": {Kind: Read},
	"BrowseService.Containers":   {Kind: Read},
	"BrowseService.Objects":      {Kind: Read},
	// InspectorService
	"InspectorService.Container":   {Kind: Read},
	"InspectorService.Object":      {Kind: Read},
	"InspectorService.Prefix":      {Kind: Read},
	"InspectorService.DLOSegments": {Kind: Read},
	"InspectorService.SLOManifest": {Kind: Read},
	// MutateService — the UPDATE mutates carry optimistic-concurrency, so
	// their explicit contract adds conflict (codex war-room F-03).
	"MutateService.CreateContainer":      {Kind: Mutate},
	"MutateService.UpdateContainer":      {Kind: Mutate, Scenarios: []string{"happy", "refusal", "conflict"}},
	"MutateService.CreateMarker":         {Kind: Mutate},
	"MutateService.DeleteMarker":         {Kind: Destructive},
	"MutateService.DeleteManifest":       {Kind: Destructive},
	"MutateService.UpdateObjectMetadata": {Kind: Mutate, Scenarios: []string{"happy", "refusal", "conflict"}},
	// AuditService
	"AuditService.Recent": {Kind: Read},
	"AuditService.Search": {Kind: Read},
	// TransferService
	"TransferService.PlanCopy":          {Kind: Read},
	"TransferService.PreflightUpload":   {Kind: Read},
	"TransferService.PreflightDownload": {Kind: Read},
	"TransferService.PickUploadFiles":   {Kind: Session},
	"TransferService.PickDownloadDir":   {Kind: Session},
	"TransferService.StartCopy":         {Kind: Job},
	"TransferService.StartMirror":       {Kind: Job},
	"TransferService.StartUpload":       {Kind: Job},
	"TransferService.StartDownload":     {Kind: Job},
	"TransferService.RerunJob":          {Kind: Job},
	"TransferService.CancelJob":         {Kind: Session},
	// DeleteService
	"DeleteService.PreviewDelete":         {Kind: Read},
	"DeleteService.CheckSegmentOwnership": {Kind: Read},
	"DeleteService.StartDelete":           {Kind: Job},
	// DeleteManifestWithSegments returns synchronously (no Job runner/state
	// machine), but it DOES have partial-result and retry semantics — it
	// reports failed/remaining segments, instructs the operator to retry,
	// and context cancellation can interrupt it mid-request. So it keeps
	// partial/retry/cancel through an explicit per-action contract; a
	// synchronous return shape does not erase those semantics (codex
	// war-room F-03).
	"DeleteService.DeleteManifestWithSegments": {Kind: Destructive, Scenarios: []string{"happy", "refusal", "conflict", "partial", "retry", "cancel"}},
	// JobService
	"JobService.Get":  {Kind: Read},
	"JobService.List": {Kind: Read},
	// FindService — StartFind launches a cancellable async scan (read-only,
	// but not a plain synchronous read), so it needs a cancel scenario.
	"FindService.StartFind":  {Kind: Read, Scenarios: []string{"happy", "cancel"}},
	"FindService.FindStatus": {Kind: Read},
	"FindService.CancelFind": {Kind: Session},
	// SettingsService
	"SettingsService.Get": {Kind: Read},
	"SettingsService.Set": {Kind: Mutate},
	// BenchmarkService
	"BenchmarkService.Latest":         {Kind: Read},
	"BenchmarkService.StartBenchmark": {Kind: Job},
}

// Coverage is EVIDENCE-BASED, not a hand map: a (action, scenario) pair
// counts only when a system test EMITS a claim through a single-use
// invocation Receipt (harness.go) whose Credit* method validated the
// scenario against that call's real outcome, AND cmd/scenariocov sees the
// emitting test terminally PASS in the `go test -json` stream. A future
// hard gate can fail CI when a shipped action has an uncovered required
// scenario; today the report is visibly incomplete scaffolding.
