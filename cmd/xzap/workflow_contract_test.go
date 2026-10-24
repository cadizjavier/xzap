package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cadizjavier/xzap/internal"
)

func TestCLIExecutableContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "xzap")
	buildBinary := exec.Command("go", "build", "-o", binary, ".")
	if output, err := buildBinary.CombinedOutput(); err != nil {
		t.Fatalf("build xzap: %v\n%s", err, output)
	}

	fixture := newWorkflowFixture(t, true)
	xcodebuildLog := fixture.installCommand("xcodebuild", "")

	run := func(args ...string) (string, error) {
		command := exec.Command(binary, args...)
		command.Dir = fixture.root
		command.Env = os.Environ()
		output, err := command.CombinedOutput()
		return string(output), err
	}

	output, err := run("build", "Qonto", "--no-color")
	if err != nil || !strings.Contains(output, "Built Qonto") || strings.Contains(output, "Building Qonto") {
		t.Fatalf("xzap build output = %q, error = %v", output, err)
	}
	if strings.Contains(strings.TrimSpace(output), "\n") {
		t.Errorf("captured build output = %q, want one result line", output)
	}
	output, err = run("test", "Qonto", "AppTests/LoginTests", "--no-color")
	if err != nil || !strings.Contains(output, "2 passed") {
		t.Fatalf("xzap test output = %q, error = %v", output, err)
	}

	calls := readWorkflowCalls(t, xcodebuildLog)
	if len(calls) != 2 {
		t.Fatalf("xcodebuild calls = %#v, want build and test", calls)
	}
	for index, call := range calls {
		if !containsArguments(call, "-scheme", "Qonto") {
			t.Errorf("xcodebuild call %d = %#v, want explicit scheme", index, call)
		}
	}
	if !containsArguments(calls[1], "-only-testing:AppTests/LoginTests") {
		t.Errorf("test call = %#v, want test identifier", calls[1])
	}

	output, err = run("build", "--no-color")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitUsage {
		t.Fatalf("xzap build without scheme error = %v, output = %q", err, output)
	}
	if !strings.Contains(output, "Error:") || !strings.Contains(output, "requires exactly one scheme") {
		t.Errorf("usage output = %q", output)
	}
	output, err = run("build", "Qonto", "--json")
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitUsage || !strings.Contains(output, "flag provided but not defined") {
		t.Errorf("removed --json output = %q, error = %v", output, err)
	}

	staleConfig := internal.Config{
		Project: "My App.xcodeproj",
		Devices: map[string]internal.Device{
			"main": {Kind: internal.SimulatorDevice, ID: "STALE-APP", Name: "iPhone"},
			"test": {Kind: internal.SimulatorDevice, ID: "STALE-TEST", Name: "iPad"},
		},
		Defaults: internal.DeviceDefaults{Run: "main", Build: "main", Test: "test"},
	}
	if _, err := internal.SaveConfig(fixture.root, staleConfig); err != nil {
		t.Fatal(err)
	}
	output, err = run("doctor", "--no-color")
	if err != nil || strings.Contains(output, "✗") || !strings.Contains(output, "Device main") || !strings.Contains(output, "Device test") {
		t.Errorf("doctor with recoverable Simulator IDs output = %q, error = %v", output, err)
	}
}

func containsArguments(arguments []string, sequence ...string) bool {
	for start := 0; start+len(sequence) <= len(arguments); start++ {
		if reflect.DeepEqual(arguments[start:start+len(sequence)], sequence) {
			return true
		}
	}
	return false
}

func TestRunResolvesCurrentAppProduct(t *testing.T) {
	for _, failSettings := range []bool{false, true} {
		name := "renamed product"
		if failSettings {
			name = "settings failure does not install cached product"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newWorkflowFixture(t, true)
			oldApp := filepath.Join(fixture.root, "Old App.app")
			newApp := filepath.Join(fixture.root, "New App.app")
			for _, app := range []string{oldApp, newApp} {
				if err := os.Mkdir(app, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte("plist"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			config, err := internal.LoadConfig(filepath.Join(fixture.root, internal.CacheFile))
			if err != nil {
				t.Fatal(err)
			}
			config.Devices["phone"] = internal.Device{Kind: internal.PhysicalDevice, ID: "PHYSICAL-DEVICE", Name: "Javier's iPhone"}
			// Older configurations can still contain the obsolete product cache.
			legacyConfig := struct {
				internal.Config
				Products map[string]string `json:"products"`
			}{config, map[string]string{"MyApp@device": oldApp}}
			configData, err := json.Marshal(legacyConfig)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(fixture.root, internal.CacheFile)
			if err := os.WriteFile(configPath, configData, 0o644); err != nil {
				t.Fatal(err)
			}
			settings, err := json.Marshal([]map[string]any{{
				"target": "MyApp",
				"buildSettings": map[string]string{
					"PRODUCT_TYPE":     "com.apple.product-type.application",
					"TARGET_BUILD_DIR": fixture.root,
					"WRAPPER_NAME":     "New App.app",
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			settingsCommand := "printf '%s\\n' " + workflowShellQuote(string(settings))
			if failSettings {
				settingsCommand = "printf '%s\\n' 'read settings failed' >&2; exit 1"
			}
			xcodebuildLog := fixture.installCommand("xcodebuild", `
case "$*" in
  *-showBuildSettings*) `+settingsCommand+` ;;
esac
`)
			fixture.installCommand("plutil", "printf '%s\\n' com.example.new")

			err = run(context.Background(), "MyApp", "phone")
			if failSettings {
				if err == nil || !strings.Contains(err.Error(), "read settings failed") {
					t.Fatalf("run() error = %v, want settings failure", err)
				}
			} else if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			calls := readWorkflowCalls(t, xcodebuildLog)
			if len(calls) != 2 || !containsArguments(calls[1], "-showBuildSettings", "-json") ||
				!containsArguments(calls[1], "-destination", "platform=iOS,id=PHYSICAL-DEVICE") {
				t.Fatalf("xcodebuild calls = %#v, want build and current destination's settings", calls)
			}
			want := [][]string{{
				"devicectl", "list", "devices", "--quiet", "--json-output", "-", "--omit-deprecated-fields-in-json",
			}}
			if !failSettings {
				want = append(want,
					[]string{"devicectl", "device", "install", "app", "--device", "PHYSICAL-DEVICE", newApp},
					[]string{"devicectl", "device", "process", "launch", "--device", "PHYSICAL-DEVICE", "--terminate-existing", "com.example.new"},
				)
			}
			assertWorkflowCalls(t, fixture.xcrunLog, want)
			unchanged, err := os.ReadFile(configPath)
			if err != nil || string(unchanged) != string(configData) {
				t.Fatalf("run changed the configuration: %q, %v", unchanged, err)
			}
		})
	}
}

func TestBuildWorkflowCommandContract(t *testing.T) {
	fixture := newWorkflowFixture(t, false)
	cwdLog := filepath.Join(fixture.root, "xcodebuild.cwd")
	xcodebuildLog := fixture.installCommand("xcodebuild", `printf '%s\n' "$PWD" > `+workflowShellQuote(cwdLog))

	if err := build(context.Background(), "Explicit Scheme", ""); err != nil {
		t.Fatalf("build() error = %v", err)
	}

	assertWorkflowCalls(t, fixture.xcrunLog, simulatorInventoryCalls())
	assertWorkflowCalls(t, xcodebuildLog, [][]string{{
		"-project", fixture.project,
		"-scheme", "Explicit Scheme",
		"-destination", "platform=iOS Simulator,id=APP-SIMULATOR",
		"-skipPackagePluginValidation",
		"build",
	}})
	cwd, err := os.ReadFile(cwdLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(cwd)); got != fixture.root {
		t.Errorf("xcodebuild working directory = %q, want %q", got, fixture.root)
	}
}

func TestCleanWorkflowCommandContract(t *testing.T) {
	fixture := newWorkflowFixture(t, false)
	xcodebuildLog := fixture.installCommand("xcodebuild", "")

	if err := clean(context.Background(), "Explicit Scheme", ""); err != nil {
		t.Fatalf("clean() error = %v", err)
	}

	assertWorkflowCalls(t, fixture.xcrunLog, simulatorInventoryCalls())
	assertWorkflowCalls(t, xcodebuildLog, [][]string{{
		"-project", fixture.project,
		"-scheme", "Explicit Scheme",
		"-destination", "platform=iOS Simulator,id=APP-SIMULATOR",
		"-skipPackagePluginValidation",
		"clean",
	}})
}

func TestDeviceBuildWorkflowCommandContract(t *testing.T) {
	fixture := newWorkflowFixture(t, false)
	config, err := internal.LoadConfig(filepath.Join(fixture.root, internal.CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	config.Devices["phone"] = internal.Device{Kind: internal.PhysicalDevice, ID: "PHYSICAL-DEVICE", Name: "Javier's iPhone"}
	if _, err := internal.SaveConfig(fixture.root, config); err != nil {
		t.Fatal(err)
	}
	xcodebuildLog := fixture.installCommand("xcodebuild", "")

	if err := build(context.Background(), "Explicit Scheme", "phone"); err != nil {
		t.Fatalf("device build() error = %v", err)
	}

	assertWorkflowCalls(t, fixture.xcrunLog, [][]string{{
		"devicectl", "list", "devices",
		"--quiet", "--json-output", "-", "--omit-deprecated-fields-in-json",
	}})
	assertWorkflowCalls(t, xcodebuildLog, [][]string{{
		"-project", fixture.project,
		"-scheme", "Explicit Scheme",
		"-destination", "platform=iOS,id=PHYSICAL-DEVICE",
		"-skipPackagePluginValidation",
		"build",
	}})
}

func TestTestWorkflowCommandContract(t *testing.T) {
	fixture := newWorkflowFixture(t, true)
	xcodebuildLog := fixture.installCommand("xcodebuild", "")
	identifiers := []string{
		"AppTests/Login Tests/testExpiredSession",
		"AppUITests/OnboardingTests",
	}

	if err := test(context.Background(), "Explicit Scheme", identifiers, ""); err != nil {
		t.Fatalf("test() error = %v", err)
	}

	xcodebuildCalls := readWorkflowCalls(t, xcodebuildLog)
	if len(xcodebuildCalls) != 1 {
		t.Fatalf("xcodebuild calls = %#v, want one call", xcodebuildCalls)
	}
	call := xcodebuildCalls[0]
	if len(call) != 12 {
		t.Fatalf("xcodebuild arguments = %#v, want 12 arguments", call)
	}
	resultPath := call[8]
	if !strings.HasSuffix(resultPath, filepath.Join("Tests.xcresult")) {
		t.Errorf("result path = %q, want Tests.xcresult", resultPath)
	}
	wantXcodebuild := []string{
		"-project", fixture.project,
		"-scheme", "Explicit Scheme",
		"-destination", "platform=iOS Simulator,id=TEST-SIMULATOR",
		"-skipPackagePluginValidation",
		"-resultBundlePath", resultPath,
		"-only-testing:AppTests/Login Tests/testExpiredSession",
		"-only-testing:AppUITests/OnboardingTests",
		"test",
	}
	if !reflect.DeepEqual(call, wantXcodebuild) {
		t.Errorf("xcodebuild arguments = %#v, want %#v", call, wantXcodebuild)
	}
	wantXcrun := append(simulatorInventoryCalls(), []string{
		"xcresulttool", "get", "test-results", "summary",
		"--path", resultPath,
		"--compact",
	})
	assertWorkflowCalls(t, fixture.xcrunLog, wantXcrun)
}

func TestPhysicalDeviceTestWorkflowCommandContract(t *testing.T) {
	fixture := newWorkflowFixture(t, false)
	config, err := internal.LoadConfig(filepath.Join(fixture.root, internal.CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	config.Devices["phone"] = internal.Device{
		Kind: internal.PhysicalDevice, ID: "PHYSICAL-DEVICE", Name: "Javier's iPhone",
	}
	if _, err := internal.SaveConfig(fixture.root, config); err != nil {
		t.Fatal(err)
	}
	xcodebuildLog := fixture.installCommand("xcodebuild", "")

	if err := test(context.Background(), "Explicit Scheme", nil, "phone"); err != nil {
		t.Fatalf("physical test() error = %v", err)
	}

	xcodebuildCalls := readWorkflowCalls(t, xcodebuildLog)
	if len(xcodebuildCalls) != 1 {
		t.Fatalf("xcodebuild calls = %#v, want one call", xcodebuildCalls)
	}
	call := xcodebuildCalls[0]
	if len(call) != 10 {
		t.Fatalf("xcodebuild arguments = %#v, want 10 arguments", call)
	}
	resultPath := call[8]
	wantXcodebuild := []string{
		"-project", fixture.project,
		"-scheme", "Explicit Scheme",
		"-destination", "platform=iOS,id=PHYSICAL-DEVICE",
		"-skipPackagePluginValidation",
		"-resultBundlePath", resultPath,
		"test",
	}
	if !reflect.DeepEqual(call, wantXcodebuild) {
		t.Errorf("xcodebuild arguments = %#v, want %#v", call, wantXcodebuild)
	}
	wantXcrun := [][]string{{
		"devicectl", "list", "devices",
		"--quiet", "--json-output", "-", "--omit-deprecated-fields-in-json",
	}, {
		"xcresulttool", "get", "test-results", "summary",
		"--path", resultPath,
		"--compact",
	}}
	assertWorkflowCalls(t, fixture.xcrunLog, wantXcrun)
}

type workflowFixture struct {
	t        *testing.T
	root     string
	bin      string
	project  string
	xcrunLog string
}

func newWorkflowFixture(t *testing.T, testTargetBooted bool) *workflowFixture {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "My App.xcodeproj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	config := internal.Config{
		Project: "My App.xcodeproj",
		Devices: map[string]internal.Device{
			"main": {Kind: internal.SimulatorDevice, ID: "APP-SIMULATOR", Name: "iPhone"},
			"test": {Kind: internal.SimulatorDevice, ID: "TEST-SIMULATOR", Name: "iPad"},
		},
		Defaults: internal.DeviceDefaults{Run: "main", Build: "main", Test: "test"},
	}
	if _, err := internal.SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "fake-bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fixture := &workflowFixture{t: t, root: root, bin: bin, project: project}
	testState := "Shutdown"
	if testTargetBooted {
		testState = "Booted"
	}
	fixture.xcrunLog = fixture.installCommand("xcrun", `
case "$*" in
	"devicectl list devices --quiet --json-output - --omit-deprecated-fields-in-json")
		printf '%s\n' '{"result":{"devices":[{"identifier":"PHYSICAL-DEVICE","properties":{"connection":{"pairingState":"paired","state":"connected"},"hardware":{"marketingName":"iPhone","platform":"iOS","reality":"physical","udid":"PHYSICAL-DEVICE"},"state":{"name":"Javier'"'"'s iPhone"}}}]}}'
		;;
  "simctl list runtimes --json")
    printf '%s\n' '{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-0","version":"18.0"}]}'
    ;;
  "simctl list devices available --json")
    printf '%s\n' '{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"udid":"APP-SIMULATOR","name":"iPhone","state":"Booted","isAvailable":true},{"udid":"TEST-SIMULATOR","name":"iPad","state":"`+testState+`","isAvailable":true}]}}'
    ;;
  "xcresulttool get test-results summary "*)
    printf '%s\n' '{"totalTestCount":2,"passedTests":2}'
    ;;
esac
`)
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })
	return fixture
}

func (f *workflowFixture) installCommand(name, behavior string) string {
	f.t.Helper()
	logPath := filepath.Join(f.root, name+".log")
	script := `#!/bin/sh
{
  printf '%s\n' 'CALL'
  printf '%s\n' "$@"
  printf '%s\n' 'END'
} >> ` + workflowShellQuote(logPath) + "\n" + behavior + "\n"
	if err := os.WriteFile(filepath.Join(f.bin, name), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return logPath
}

func simulatorInventoryCalls() [][]string {
	return [][]string{
		{"simctl", "list", "runtimes", "--json"},
		{"simctl", "list", "devices", "available", "--json"},
	}
}

func workflowShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func readWorkflowCalls(t *testing.T, logPath string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	var calls [][]string
	var current []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		switch line {
		case "CALL":
			current = nil
		case "END":
			calls = append(calls, current)
		default:
			current = append(current, line)
		}
	}
	return calls
}

func assertWorkflowCalls(t *testing.T, logPath string, want [][]string) {
	t.Helper()
	if got := readWorkflowCalls(t, logPath); !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %#v, want %#v", got, want)
	}
}
