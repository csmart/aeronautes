package systemtest

// The action inventory (§65.10 rule 1): every bound bridge method is a
// row with its kind and the scenarios the war room must exercise. A
// reflection guard (inventory_test.go) fails the build when a bound
// service exports a method absent here — so a new action cannot ship
// without a matrix entry, and "full coverage" is a generated fact.

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
	// MutateService
	"MutateService.CreateContainer":      {Kind: Mutate},
	"MutateService.UpdateContainer":      {Kind: Mutate},
	"MutateService.CreateMarker":         {Kind: Mutate},
	"MutateService.DeleteMarker":         {Kind: Destructive},
	"MutateService.DeleteManifest":       {Kind: Destructive},
	"MutateService.UpdateObjectMetadata": {Kind: Mutate},
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
	// DeleteManifestWithSegments returns its result synchronously (force +
	// typed confirmation, no job/cancel lifecycle), so it is Destructive,
	// not a Job (codex war-room H-03).
	"DeleteService.DeleteManifestWithSegments": {Kind: Destructive},
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

// Covered records which (action, scenario) pairs an actual system test
// exercises — maintained alongside the tests (the standing rule forbids
// a behavior change without its system test, so this stays honest). The
// report test turns required-minus-covered into the gap artifact; a
// future hard gate fails CI when a shipped action has an uncovered
// required scenario.
var Covered = map[string][]string{
	"MutateService.UpdateObjectMetadata":       {"happy", "refusal"}, // metadata_test.go (happy + optimistic-conflict refusal)
	"TransferService.StartCopy":                {"happy", "refusal"}, // copy_test.go + refusal_test.go
	"TransferService.StartMirror":              {"happy"},            // mirror_test.go
	"DeleteService.StartDelete":                {"happy", "refusal"}, // delete_test.go + refusal_test.go
	"DeleteService.DeleteManifestWithSegments": {"refusal"},          // refusal_test.go
}

// CoverageGaps returns, per action, the required scenarios that no
// system test yet covers. An action absent from the map is fully
// covered for its kind.
func CoverageGaps() map[string][]string {
	gaps := map[string][]string{}
	for key, action := range Inventory {
		have := map[string]bool{}
		for _, s := range Covered[key] {
			have[s] = true
		}
		var missing []string
		for _, req := range action.required() {
			if !have[req] {
				missing = append(missing, req)
			}
		}
		if len(missing) > 0 {
			gaps[key] = missing
		}
	}
	return gaps
}
