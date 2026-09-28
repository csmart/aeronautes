package app

import (
	"sort"
	"strings"
	"testing"
)

// restoreSwitch puts the process back to an unset switch, so one case
// cannot leak an enabled capability into the next.
func restoreSwitch(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { v1Enabled.Store(nil) })
}

// deferredActionsFor lists the table's actions for one capability.
func deferredActionsFor(capability string) []string {
	var actions []string
	for action, name := range v1DeferredActions {
		if name == capability {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	return actions
}

// ADR 0082: with nothing set, every deferred action refuses exactly as
// it does today, and the switch reports nothing.
func TestCapabilitySwitchDefaultsToNothingEnabled(t *testing.T) {
	forceV1Gate(t)
	restoreSwitch(t)
	names, unrecognised := EnableCapabilities("")
	if len(names) != 0 || len(unrecognised) != 0 {
		t.Fatalf("empty switch = %v %v", names, unrecognised)
	}
	if got, bad := EnabledCapabilitySnapshot(); len(got) != 0 || len(bad) != 0 {
		t.Fatalf("snapshot = %v %v", got, bad)
	}
	for action, capability := range v1DeferredActions {
		assertV1Refusal(t, refuseIfDeferred(action), capability)
	}
}

// One token admits its whole capability and nothing else.
func TestCapabilitySwitchEnablesWholeCapabilities(t *testing.T) {
	forceV1Gate(t)
	restoreSwitch(t)
	names, unrecognised := EnableCapabilities("upload")
	if len(unrecognised) != 0 || len(names) != 1 || names[0] != "Upload" {
		t.Fatalf("upload switch = %v %v", names, unrecognised)
	}
	uploads := deferredActionsFor("Upload")
	if len(uploads) != 3 {
		t.Fatalf("the table names %d upload actions, want 3: %v", len(uploads), uploads)
	}
	for _, action := range uploads {
		if err := refuseIfDeferred(action); err != nil {
			t.Errorf("%s refused although upload is enabled: %v", action, err)
		}
	}
	for action, capability := range v1DeferredActions {
		if capability == "Upload" {
			continue
		}
		assertV1Refusal(t, refuseIfDeferred(action), capability)
	}
}

// Several tokens at once, in any case, with blanks and repeats, and a
// capability enables every action classified under it rather than the
// ones whose names look related. RerunJob is classified Copy.
func TestCapabilitySwitchParsesTheOperatorSpelling(t *testing.T) {
	forceV1Gate(t)
	restoreSwitch(t)
	names, unrecognised := EnableCapabilities(" Upload , ,COPY,upload ")
	if len(unrecognised) != 0 {
		t.Fatalf("unrecognised = %v", unrecognised)
	}
	if strings.Join(names, "|") != "Copy|Upload" {
		t.Fatalf("names = %v", names)
	}
	for _, action := range append(deferredActionsFor("Upload"), deferredActionsFor("Copy")...) {
		if err := refuseIfDeferred(action); err != nil {
			t.Errorf("%s refused although its capability is enabled: %v", action, err)
		}
	}
	if err := refuseIfDeferred("TransferService.RerunJob"); err != nil {
		t.Errorf("RerunJob is classified Copy and should follow it: %v", err)
	}
	assertV1Refusal(t, refuseIfDeferred("DeleteService.StartDelete"), "Bulk delete")
}

// A token that names no capability changes nothing and is reported, so a
// typo is visible rather than silent.
func TestCapabilitySwitchReportsUnrecognisedTokens(t *testing.T) {
	forceV1Gate(t)
	restoreSwitch(t)
	names, unrecognised := EnableCapabilities("uplaod, upload, uplaod")
	if strings.Join(names, "|") != "Upload" {
		t.Fatalf("names = %v", names)
	}
	if strings.Join(unrecognised, "|") != "uplaod" {
		t.Fatalf("unrecognised = %v", unrecognised)
	}
	got, bad := EnabledCapabilitySnapshot()
	if strings.Join(got, "|") != "Upload" || strings.Join(bad, "|") != "uplaod" {
		t.Fatalf("snapshot = %v %v", got, bad)
	}
}

// ADR 0020 refuses Mirror independently of this gate and ADR 0052
// decision 5 keeps that out of scoping, so no value admits it.
func TestCapabilitySwitchCannotEnableMirror(t *testing.T) {
	forceV1Gate(t)
	restoreSwitch(t)
	for _, token := range v1CapabilityTokens {
		if token == "Mirror" {
			t.Fatal("a token maps to Mirror")
		}
	}
	for _, spec := range []string{"mirror", "Mirror", "MIRROR", "TransferService.StartMirror", "upload,mirror", "*", "all"} {
		names, unrecognised := EnableCapabilities(spec)
		for _, name := range names {
			if name == "Mirror" {
				t.Fatalf("%q enabled Mirror", spec)
			}
		}
		if len(unrecognised) == 0 && spec != "upload,mirror" {
			t.Errorf("%q was accepted silently: %v", spec, names)
		}
		assertV1Refusal(t, refuseIfDeferred("TransferService.StartMirror"), "Mirror")
	}
}

// Admission reads the reading taken at startup, never the environment,
// so the switch cannot change under a running process.
func TestCapabilitySwitchIsNotReReadPerCall(t *testing.T) {
	forceV1Gate(t)
	restoreSwitch(t)
	t.Setenv(EnableEnvVar, "")
	if names, _ := ReadEnabledCapabilities(); len(names) != 0 {
		t.Fatalf("startup reading = %v", names)
	}
	t.Setenv(EnableEnvVar, "upload")
	for _, action := range deferredActionsFor("Upload") {
		assertV1Refusal(t, refuseIfDeferred(action), "Upload")
	}
	if names, _ := EnabledCapabilitySnapshot(); len(names) != 0 {
		t.Fatalf("snapshot followed the environment: %v", names)
	}
}

// The Info document carries the reading, so the interface can say what
// this process offers without a new bridge action.
func TestInfoCarriesTheCapabilitySwitch(t *testing.T) {
	restoreSwitch(t)
	service := NewAppService(BuildIdentity{Version: "test"}, "session")
	if info := service.Info(); len(info.EnabledCapabilities) != 0 || len(info.UnrecognisedCapabilities) != 0 {
		t.Fatalf("unset Info = %+v", info)
	}
	EnableCapabilities("upload, nonsense")
	info := service.Info()
	if strings.Join(info.EnabledCapabilities, "|") != "Upload" || strings.Join(info.UnrecognisedCapabilities, "|") != "nonsense" {
		t.Fatalf("Info = %+v", info)
	}
	if about := service.About(); strings.Join(about.EnabledCapabilities, "|") != "Upload" {
		t.Fatalf("About = %+v", about.Info)
	}
}

// ADR 0082 decision 5 promises the operator is TOLD about a token that names
// no capability, and its proof list says that appears in the startup log.
// The lines are built here rather than only in main.go so the sentences can
// be asserted: a promise that the operator will be told is worth no more than
// the wording nobody checked.
func TestStartupLinesNameWhatIsOfferedAndWhatWasIgnored(t *testing.T) {
	restoreSwitch(t)
	for _, fixture := range []struct {
		spec string
		want []string
		none bool
	}{
		{spec: "", none: true},
		{spec: "upload", want: []string{
			"AERONAUTES_ENABLE: offering unproven capabilities in this process: Upload"}},
		{spec: "upload, uplaod", want: []string{
			"AERONAUTES_ENABLE: offering unproven capabilities in this process: Upload",
			"AERONAUTES_ENABLE: ignored, not a capability: uplaod"}},
		{spec: "not-a-capability", want: []string{
			"AERONAUTES_ENABLE: ignored, not a capability: not-a-capability"}},
		// Mirror has no token, so asking for it is ignored and said to be
		// ignored rather than silently dropped.
		{spec: "mirror", want: []string{
			"AERONAUTES_ENABLE: ignored, not a capability: mirror"}},
	} {
		t.Run(fixture.spec, func(t *testing.T) {
			lines := StartupCapabilityLines(EnableCapabilities(fixture.spec))
			if fixture.none {
				if len(lines) != 0 {
					t.Fatalf("an unset switch logged %v", lines)
				}
				return
			}
			if len(lines) != len(fixture.want) {
				t.Fatalf("lines = %v, want %v", lines, fixture.want)
			}
			for i, want := range fixture.want {
				if lines[i] != want {
					t.Errorf("line %d = %q, want %q", i, lines[i], want)
				}
			}
		})
	}
}
