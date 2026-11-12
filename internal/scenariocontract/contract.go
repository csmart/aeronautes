// Package scenariocontract is the single, non-test source of truth for the
// war-room action inventory, its required scenarios, and the wire markers
// the coverage report is built from. Both the system tests and the
// cmd/scenariocov reconciler import it directly, so the coverage
// DENOMINATOR is fixed in Go source and cannot be selected, replaced, or
// forged from test-authored output.
//
// This is a cooperating-author contract, not a security boundary: repo
// code and CI are the real trust domain. It makes the denominator sound
// and deterministic; it does not defend against a committer who edits the
// contract itself.
package scenariocontract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Wire markers: a system test emits a claim line tagged with ClaimMarker;
// cmd/scenariocov scans for it. InventoryMarker is retired — the
// denominator comes from All(), never the stream — and is kept here only so
// the reconciler can REJECT any producer that still injects one.
const (
	ClaimMarker     = "AERONAUTES_SCENARIO_CLAIM "
	InventoryMarker = "AERONAUTES_SCENARIO_INVENTORY "
)

// Claim is the wire shape of one emitted (action, scenario) annotation with
// its evidence. It is the single definition both the emitter (systemtest)
// and the reconciler (cmd/scenariocov) marshal against, so the two cannot
// drift.
type Claim struct {
	Action   string `json:"action"`
	Scenario string `json:"scenario"`
	Evidence string `json:"evidence"`
}

// Kind classifies what a bridge method does, which fixes its default
// required scenarios.
type Kind string

const (
	Read        Kind = "read"        // read-only; a happy path
	Session     Kind = "session"     // connection/picker plumbing; a happy path
	Mutate      Kind = "mutate"      // changes one object/container; happy + refusal
	Destructive Kind = "destructive" // one synchronous delete/move; happy + refusal + conflict
	Job         Kind = "job"         // async transfer/delete; full lifecycle
)

// Action is one inventory row.
type Action struct {
	Kind Kind
	// Scenarios, when set, overrides the kind's default required list for an
	// action whose real contract does not fit the five-kind shortcut. Empty
	// means "use the kind default".
	Scenarios []string
}

// Required is the scenario list this action must cover: its explicit
// per-action override if set, otherwise its kind's default.
func (a Action) Required() []string {
	if len(a.Scenarios) > 0 {
		return append([]string(nil), a.Scenarios...)
	}
	return a.Kind.required()
}

func (k Kind) required() []string {
	switch k {
	case Read, Session:
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

// knownKinds and knownScenarios are the closed vocabularies validate checks
// against, so a typo cannot silently create a zero-requirement action or an
// unrecognised scenario.
var (
	knownKinds     = map[Kind]bool{Read: true, Session: true, Mutate: true, Destructive: true, Job: true}
	knownScenarios = map[string]bool{"happy": true, "refusal": true, "conflict": true, "cancel": true, "partial": true, "retry": true}
)

// inventory is the authoritative bridge-action list, keyed "Service.Method".
// It is unexported; callers read a defensive copy via All(), so no consumer
// can mutate the shared contract.
//
// The kind defaults are a conservative FLOOR, not DESIGN §65.10's literal
// blanket rule (~162): a synchronous atomic mutate has no "partial", so a
// per-action Scenarios override states the real contract where the default
// is wrong. This number is a floor to grow from, never a completeness claim.
var inventory = map[string]Action{
	// AppService
	"AppService.About": {Kind: Read},
	"AppService.Info":  {Kind: Read},
	// BookmarkService
	"BookmarkService.List":             {Kind: Read},
	"BookmarkService.Save":             {Kind: Mutate},
	"BookmarkService.Delete":           {Kind: Destructive},
	"BookmarkService.CredentialStatus": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	// Verify authenticates typed details and persists nothing: a read of
	// the endpoint's answer, with the refusal of a wrong key as its other
	// required cell.
	"BookmarkService.Verify": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	// BrowseService
	"BrowseService.Connect":      {Kind: Session, Scenarios: []string{"happy", "refusal"}},
	"BrowseService.Account":      {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"BrowseService.Capabilities": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"BrowseService.Containers":   {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"BrowseService.Objects":      {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	// InspectorService
	"InspectorService.Container":   {Kind: Read},
	"InspectorService.Object":      {Kind: Read},
	"InspectorService.Prefix":      {Kind: Read},
	"InspectorService.DLOSegments": {Kind: Read},
	"InspectorService.SLOManifest": {Kind: Read},
	// MutateService — the UPDATE mutates carry optimistic-concurrency.
	"MutateService.CreateContainer": {Kind: Mutate},
	// A copy's missing destination, created on the bookmark the preview
	// found it missing on (ADR 0115).
	"MutateService.CreateDestinationContainer": {Kind: Mutate},
	"MutateService.UpdateContainer":            {Kind: Mutate, Scenarios: []string{"happy", "refusal", "conflict"}},
	"MutateService.CreateMarker":               {Kind: Mutate},
	"MutateService.DeleteMarker":               {Kind: Destructive},
	"MutateService.DeleteManifest":             {Kind: Destructive},
	"MutateService.DeleteObject":               {Kind: Destructive},
	"MutateService.UpdateObjectMetadata":       {Kind: Mutate, Scenarios: []string{"happy", "refusal", "conflict"}},
	"MutateService.UpdateAccountMetadata":      {Kind: Mutate, Scenarios: []string{"happy", "refusal", "conflict"}},
	"MutateService.UpdateTempURLKey":           {Kind: Mutate, Scenarios: []string{"happy", "refusal", "conflict"}},
	// AuditService
	"AuditService.Recent":      {Kind: Read, Scenarios: []string{"refusal"}},
	"AuditService.RecentFor":   {Kind: Read},
	"AuditService.Search":      {Kind: Read},
	"AuditService.ExportAudit": {Kind: Read, Scenarios: []string{"happy", "refusal"}},

	// BackupService
	// The §51/P28 settings export writes the operator's configuration to a
	// file they choose; it reads config and writes a file, gating nothing.
	"BackupService.ExportSettings": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	// The restore reads a chosen file and merges the operator's
	// configuration — creating bookmarks and replacing settings, never
	// deleting or overwriting — so it is a mutate.
	"BackupService.ImportSettings": {Kind: Mutate, Scenarios: []string{"happy", "refusal"}},

	// BookmarkImportService (ADR 0114)
	// The preview opens the file picker and holds what it read behind a
	// token, writing nothing, as the upload picks do.
	"BookmarkImportService.PreviewImport": {Kind: Session},
	// The save creates bookmarks and never changes or deletes one.
	"BookmarkImportService.SaveImport": {Kind: Mutate},
	// The status reads the latest save's outcomes.
	"BookmarkImportService.ImportStatus": {Kind: Read},
	// TransferService
	"TransferService.PlanCopy": {Kind: Read},
	// The copy destination's container names, suggested as the operator
	// types (ADR 0115).
	"TransferService.DestinationContainers": {Kind: Read},
	"TransferService.PreflightUpload":       {Kind: Read},
	"TransferService.PreflightDownload":     {Kind: Read},
	"TransferService.PickUploadFiles":       {Kind: Session},
	// The folder pick of ADR 0103 is a session action like the file pick: it
	// binds a selection to a scope and writes nothing.
	"TransferService.PickUploadDirectory": {Kind: Session},
	"TransferService.PickDownloadDir":     {Kind: Session},
	"TransferService.StartCopy":           {Kind: Job},
	"TransferService.StartMirror":         {Kind: Job},
	"TransferService.StartUpload":         {Kind: Job},
	"TransferService.StartDownload":       {Kind: Job},
	"TransferService.RerunJob":            {Kind: Job},
	"TransferService.CancelJob":           {Kind: Session},
	"TransferService.PauseJob":            {Kind: Session},
	"TransferService.ResumeJob":           {Kind: Session},
	// DeleteService
	"DeleteService.PreviewDelete":         {Kind: Read},
	"DeleteService.CheckSegmentOwnership": {Kind: Read},
	// ADR 0093 decision 6 and ADR 0095 decision 9: a deletion job never
	// retries a destructive request, so it has no retry scenario; its real
	// contract is the five below. The bulk deletion became this job in ADR
	// 0095.
	"DeleteService.StartDelete":         {Kind: Job, Scenarios: []string{"happy", "refusal", "conflict", "cancel", "partial"}},
	"DeleteService.StartSelectedDelete": {Kind: Job, Scenarios: []string{"happy", "refusal", "conflict", "cancel", "partial"}},
	// ADR 0095 decision 8 made DeleteManifestWithSegments a job that owns the
	// reviewed manifest and its segments; decision 9 drops its retry for the
	// same reason, so its contract is the five a deletion job keeps.
	"DeleteService.DeleteManifestWithSegments": {Kind: Job, Scenarios: []string{"happy", "refusal", "conflict", "cancel", "partial"}},
	// JobService
	"JobService.Get":            {Kind: Read},
	"JobService.List":           {Kind: Read},
	"JobService.History":        {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"JobService.Items":          {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"JobService.ItemTotals":     {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"JobService.RecoveryStatus": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	// ADR 0088: the interrupted-job notice read and its dismissal. The read is
	// unavailable-or-happy like RecoveryStatus; the dismissal is a local mutate
	// that either applies or refuses (decision 7).
	"JobService.InterruptedDisclosures": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"JobService.DismissInterrupted":     {Kind: Mutate},
	// FindService — StartFind launches a cancellable async scan.
	"FindService.StartFind":  {Kind: Read, Scenarios: []string{"happy", "cancel", "refusal"}},
	"FindService.FindStatus": {Kind: Read, Scenarios: []string{"happy", "refusal"}},
	"FindService.CancelFind": {Kind: Session},
	// SettingsService
	"SettingsService.Get": {Kind: Read},
	"SettingsService.Set": {Kind: Mutate},
	// BenchmarkService
	"BenchmarkService.Latest":         {Kind: Read},
	"BenchmarkService.StartBenchmark": {Kind: Job},
}

// All returns a defensive copy of the contract, keyed "Service.Method".
func All() map[string]Action {
	out := make(map[string]Action, len(inventory))
	for k, v := range inventory {
		out[k] = Action{Kind: v.Kind, Scenarios: append([]string(nil), v.Scenarios...)}
	}
	return out
}

// Requires reports whether scenario is one the named action requires.
func Requires(action, scenario string) bool {
	a, ok := inventory[action]
	if !ok {
		return false
	}
	for _, r := range a.Required() {
		if r == scenario {
			return true
		}
	}
	return false
}

// Validate checks the shipped contract is well-formed. A malformed contract
// is a build-time failure, so no row can contribute zero to the denominator
// and silently look complete.
func Validate() error { return validateInventory(inventory) }

// validateInventory is the row-by-row check, split out so its rejection
// branches (unknown kind, unknown/duplicate scenario, invalid baseline) have
// negative unit tests against synthetic contracts.
func validateInventory(inv map[string]Action) error {
	keys := make([]string, 0, len(inv))
	for k := range inv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		a := inv[key]
		if !knownKinds[a.Kind] {
			return fmt.Errorf("%s: unknown kind %q", key, a.Kind)
		}
		// An empty Scenarios override falls back to the kind default, so a
		// known kind always yields a non-empty list.
		seen := map[string]bool{}
		happy := false
		for _, s := range a.Required() {
			if !knownScenarios[s] {
				return fmt.Errorf("%s: unknown scenario %q", key, s)
			}
			if seen[s] {
				return fmt.Errorf("%s: duplicate scenario %q", key, s)
			}
			seen[s] = true
			happy = happy || s == "happy"
		}
		// ADR 0059 makes this legacy unscoped read always refuse. ADR 0068
		// keeps every other happy baseline, including deferred operations.
		if key == "AuditService.Recent" {
			if a.Kind != Read || len(seen) != 1 || !seen["refusal"] {
				return fmt.Errorf("%s: requires read kind and only refusal", key)
			}
		} else if !happy {
			return fmt.Errorf("%s: missing the happy baseline", key)
		}
	}
	return nil
}

// Hash is a short, deterministic fingerprint of the whole contract, so the
// report and the reconciler can prove they used the same denominator.
func Hash() string {
	h := sha256.New()
	for _, key := range sortedKeys() {
		a := inventory[key]
		fmt.Fprintf(h, "%s|%s|%s\n", key, a.Kind, strings.Join(a.Required(), ","))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func sortedKeys() []string {
	keys := make([]string, 0, len(inventory))
	for k := range inventory {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
