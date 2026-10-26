package rclone

// Report is the answer to `aeronautes --engine` (ADR 0089 decision 6):
// the resolution a transfer would make, found the same way a transfer
// finds it, or the refusal it would meet. Producing it opens no state
// and spawns nothing but the version probe, so an installed package can
// be checked without a window or a job.
type Report struct {
	Engine   string `json:"engine"`
	Pin      string `json:"pin"`
	Version  string `json:"version,omitempty"`
	Path     string `json:"path,omitempty"`
	Source   string `json:"source,omitempty"`
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"`
}

// Describe resolves the engine with default options and reports it.
func Describe() Report {
	report := Report{Engine: "rclone", Pin: PinnedVersion}
	resolution, err := Resolve(Options{})
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Version = resolution.Version
	report.Path = resolution.Path
	report.Source = resolution.Source
	report.Verified = resolution.Verified
	return report
}
