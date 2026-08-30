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
	// Destructive: deletes or moves data, or runs a job that can, needs
	// happy + refusal + conflict + cancel/partial where a job.
	Destructive Kind = "destructive"
)

// Action is one inventory row.
type Action struct {
	Kind      Kind
	Scenarios []string // required scenario tags for this action
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
	"TransferService.StartCopy":         {Kind: Destructive},
	"TransferService.StartMirror":       {Kind: Destructive},
	"TransferService.StartUpload":       {Kind: Destructive},
	"TransferService.StartDownload":     {Kind: Destructive},
	"TransferService.RerunJob":          {Kind: Destructive},
	"TransferService.CancelJob":         {Kind: Session},
	// DeleteService
	"DeleteService.PreviewDelete":              {Kind: Read},
	"DeleteService.CheckSegmentOwnership":      {Kind: Read},
	"DeleteService.StartDelete":                {Kind: Destructive},
	"DeleteService.DeleteManifestWithSegments": {Kind: Destructive},
	// JobService
	"JobService.Get":  {Kind: Read},
	"JobService.List": {Kind: Read},
	// FindService
	"FindService.StartFind":  {Kind: Read},
	"FindService.FindStatus": {Kind: Read},
	"FindService.CancelFind": {Kind: Session},
	// SettingsService
	"SettingsService.Get": {Kind: Read},
	"SettingsService.Set": {Kind: Mutate},
	// BenchmarkService
	"BenchmarkService.Latest":         {Kind: Read},
	"BenchmarkService.StartBenchmark": {Kind: Destructive},
}
