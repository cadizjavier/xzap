package internal

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildCommandContract(t *testing.T) {
	logPath := installRecordingCommand(t, "xcodebuild", "")

	opts := BuildOptions{
		Project: "/tmp/Project With Spaces.xcworkspace",
		Scheme:  "My App Staging",
		Target:  Target{ID: "SIMULATOR-ID", Name: "iPhone"},
	}
	if err := Build(context.Background(), opts); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	want := [][]string{{
		"-workspace", "/tmp/Project With Spaces.xcworkspace",
		"-scheme", "My App Staging",
		"-destination", "platform=iOS Simulator,id=SIMULATOR-ID",
		"-skipPackagePluginValidation",
		"build",
	}}
	assertCommandCalls(t, logPath, want)
}

func TestListSimulatorsCommandContract(t *testing.T) {
	logPath := installRecordingCommand(t, "xcrun", `
case "$*" in
  "simctl list runtimes --json")
    printf '%s\n' '{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-0","version":"18.0"}]}'
    ;;
  "simctl list devices available --json")
    printf '%s\n' '{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"udid":"SIMULATOR-ID","name":"iPhone","state":"Shutdown","isAvailable":true}]}}'
    ;;
esac
`)

	simulators, err := listSimulators(context.Background())
	if err != nil {
		t.Fatalf("listSimulators() error = %v", err)
	}
	if len(simulators) != 1 || simulators[0].UDID != "SIMULATOR-ID" {
		t.Fatalf("listSimulators() = %#v", simulators)
	}

	want := [][]string{
		{"simctl", "list", "runtimes", "--json"},
		{"simctl", "list", "devices", "available", "--json"},
	}
	assertCommandCalls(t, logPath, want)
}

func TestListDevicesCommandContract(t *testing.T) {
	logPath := installRecordingCommand(t, "xcrun", `printf '%s\n' '{"result":{"devices":[]}}'`)

	if _, err := ListDevices(context.Background()); err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}
	assertCommandCalls(t, logPath, [][]string{{
		"devicectl", "list", "devices",
		"--quiet", "--json-output", "-", "--omit-deprecated-fields-in-json",
	}})
}

func TestSimulatorLifecycleCommandContract(t *testing.T) {
	logPath := installRecordingCommand(t, "xcrun", "")
	target := Target{ID: "SIMULATOR-ID", Name: "iPhone", State: "Shutdown"}
	appPath := "/tmp/Build Products/My App.app"

	if err := BootTarget(context.Background(), target); err != nil {
		t.Fatalf("BootTarget() error = %v", err)
	}
	if err := InstallTarget(context.Background(), target, appPath); err != nil {
		t.Fatalf("InstallTarget() error = %v", err)
	}
	if err := LaunchTarget(context.Background(), target, "com.example.my-app"); err != nil {
		t.Fatalf("LaunchTarget() error = %v", err)
	}

	want := [][]string{
		{"simctl", "boot", "SIMULATOR-ID"},
		{"simctl", "bootstatus", "SIMULATOR-ID", "-b"},
		{"simctl", "install", "SIMULATOR-ID", appPath},
		{"simctl", "launch", "SIMULATOR-ID", "com.example.my-app"},
	}
	assertCommandCalls(t, logPath, want)
}

func TestBootStopsBeforeBootStatusAfterFailure(t *testing.T) {
	logPath := installRecordingCommand(t, "xcrun", `
if [ "$1" = "simctl" ] && [ "$2" = "boot" ]; then
  printf '%s\n' 'boot failed' >&2
  exit 1
fi
`)

	err := BootTarget(context.Background(), Target{ID: "SIMULATOR-ID", Name: "iPhone"})
	if err == nil || !strings.Contains(err.Error(), "boot simulator") {
		t.Fatalf("BootTarget() error = %v, want boot failure", err)
	}
	assertCommandCalls(t, logPath, [][]string{{"simctl", "boot", "SIMULATOR-ID"}})
}

func TestReadTestResultsCommandContract(t *testing.T) {
	logPath := installRecordingCommand(t, "xcrun", `printf '%s\n' '{"totalTestCount":2,"passedTests":2}'`)
	resultPath := "/tmp/Result Bundles/Tests.xcresult"

	summary, err := readXcodeTestResults(context.Background(), resultPath)
	if err != nil {
		t.Fatalf("readXcodeTestResults() error = %v", err)
	}
	if summary.Total != 2 || summary.Passed != 2 {
		t.Errorf("summary = %#v", summary)
	}
	assertCommandCalls(t, logPath, [][]string{{
		"xcresulttool", "get", "test-results", "summary",
		"--path", resultPath,
		"--compact",
	}})
}

func installRecordingCommand(t *testing.T, name, behavior string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, name+".log")
	script := `#!/bin/sh
{
  printf '%s\n' 'CALL'
  printf '%s\n' "$@"
  printf '%s\n' 'END'
} >> ` + shellQuote(logPath) + "\n" + behavior + "\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func assertCommandCalls(t *testing.T, logPath string, want [][]string) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	var got [][]string
	var current []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		switch line {
		case "CALL":
			current = nil
		case "END":
			got = append(got, current)
		default:
			current = append(current, line)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %#v, want %#v", got, want)
	}
}
