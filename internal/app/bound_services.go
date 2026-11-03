package app

// BoundService describes one service exposed to the Wails webview bridge.
type BoundService struct {
	Name        string // struct type name, e.g. "TransferService"
	Constructor string // the constructor main.go calls, e.g. "NewTransferServiceWithRunner"
	Prototype   any    // zero-value instance, for reflecting over its methods
}

// BoundServices is the single source of truth for which services main.go
// binds to the bridge (§65.10 rule 1). The war-room inventory gate
// reflects over the prototypes to demand an inventory row per method, and
// main_test.go's TestMainBindsExactlyBoundServices AST-checks that
// main.go's Services list calls exactly these constructors — so a newly
// bound service can never be silently un-inventoried (grok war-room P2:
// the gate used to reflect over a *second* handwritten list that could
// drift from main.go).
func BoundServices() []BoundService {
	return []BoundService{
		{"AppService", "NewAppService", &AppService{}},
		{"BookmarkService", "NewBookmarkService", &BookmarkService{}},
		{"BrowseService", "NewBrowseService", &BrowseService{}},
		{"InspectorService", "NewInspectorService", &InspectorService{}},
		{"MutateService", "NewMutateService", &MutateService{}},
		{"AuditService", "NewAuditService", &AuditService{}},
		{"TransferService", "NewTransferServiceWithRunner", &TransferService{}},
		{"DeleteService", "NewDeleteService", &DeleteService{}},
		{"JobService", "NewJobService", &JobService{}},
		{"FindService", "NewFindService", &FindService{}},
		{"SettingsService", "NewSettingsService", &SettingsService{}},
		{"BenchmarkService", "NewBenchmarkService", &BenchmarkService{}},
		{"BackupService", "NewBackupService", &BackupService{}},
	}
}
