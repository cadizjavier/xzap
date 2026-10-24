package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cadizjavier/xzap/internal"
)

func TestResolveRunContextUsesOneSimulatorInventory(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "MyApp.xcodeproj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	config := internal.Config{
		Project: "MyApp.xcodeproj",
		Devices: map[string]internal.Device{
			"main": {Kind: internal.SimulatorDevice, ID: "APP", Name: "iPhone"},
			"test": {Kind: internal.SimulatorDevice, ID: "TEST", Name: "iPad"},
		},
		Defaults: internal.DeviceDefaults{Run: "main", Build: "main", Test: "test"},
	}
	if _, err := internal.SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "xcrun.log")
	script := `#!/bin/sh
echo "$*" >> "` + logPath + `"
if [ "$3" = "runtimes" ]; then
  echo '{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-0","version":"18.0"}]}'
else
  echo '{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"udid":"APP","name":"iPhone","state":"Shutdown","isAvailable":true},{"udid":"TEST","name":"iPad","state":"Shutdown","isAvailable":true}]}}'
fi
`
	if err := os.WriteFile(filepath.Join(bin, "xcrun"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	resolved, err := resolveRunContext(context.Background(), commandRun, "")
	if err != nil {
		t.Fatalf("resolveRunContext() error = %v", err)
	}
	if resolved.device.ID != "APP" || resolved.alias != "main" {
		t.Fatalf("resolved context = %#v", resolved)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if calls := strings.Count(strings.TrimSpace(string(logData)), "\n") + 1; calls != 2 {
		t.Fatalf("xcrun calls = %d, want one runtimes and one devices query", calls)
	}
}

func TestConcurrentRunErrorPreservesBuildFailureAfterInternalCancellation(t *testing.T) {
	buildErr := internal.ErrBuildFailed
	bootErr := errors.Join(errors.New("wait for simulator"), context.Canceled)

	got := concurrentRunError(context.Background(), buildErr, bootErr)
	if !errors.Is(got, buildErr) || errors.Is(got, context.Canceled) {
		t.Fatalf("concurrentRunError() = %v, want only build failure", got)
	}
	if code := exitCode(got); code != exitBuild {
		t.Fatalf("exitCode() = %d, want %d", code, exitBuild)
	}
}

func TestConcurrentRunErrorPreservesBootFailureAfterInternalCancellation(t *testing.T) {
	bootErr := errors.New("boot failed")

	got := concurrentRunError(context.Background(), context.Canceled, bootErr)
	if !errors.Is(got, bootErr) || errors.Is(got, context.Canceled) {
		t.Fatalf("concurrentRunError() = %v, want only boot failure", got)
	}
}

func TestConcurrentRunErrorHonorsUserCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := concurrentRunError(ctx, internal.ErrBuildFailed, errors.New("boot failed"))
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("concurrentRunError() = %v, want context cancellation", got)
	}
}

func TestInteractiveInitSelectsAppAndTestSimulators(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "MyApp.xcodeproj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := internal.SaveConfig(root, internal.Config{
		Project: "MyApp.xcodeproj",
		Devices: map[string]internal.Device{
			"old": {Kind: internal.PhysicalDevice, ID: "DEVICE", Name: "Javier's iPhone"},
		},
		Defaults: internal.DeviceDefaults{Run: "old", Build: "old", Test: "old"},
	}); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$3" = "runtimes" ]; then
  echo '{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-18-0","version":"18.0"}]}'
else
  echo '{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"udid":"APP","name":"iPhone","state":"Booted","isAvailable":true},{"udid":"TEST","name":"iPad","state":"Shutdown","isAvailable":true}]}}'
fi
`
	if err := os.WriteFile(filepath.Join(bin, "xcrun"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previousDirectory)

	input, err := os.CreateTemp(root, "input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString("1\n2\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(root, "output")
	if err != nil {
		t.Fatal(err)
	}
	previousInput, previousOutput := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = input, output
	defer func() { os.Stdin, os.Stdout = previousInput, previousOutput }()

	if err := initialize(context.Background(), initOptions{}); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}
	config, err := internal.LoadConfig(filepath.Join(root, internal.CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if config.Devices["main"].ID != "APP" || config.Devices["test"].ID != "TEST" {
		t.Fatalf("configured devices = %#v", config.Devices)
	}
	if config.Defaults.Run != "main" || config.Defaults.Test != "test" {
		t.Fatalf("configured defaults = %#v", config.Defaults)
	}
	if _, exists := config.Devices["old"]; exists {
		t.Fatalf("init retained disposable old device: %#v", config.Devices)
	}
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	outputData, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(outputData)
	for _, step := range []string{"1/3 Project", "2/3 Default device", "3/3 Test device"} {
		if !strings.Contains(text, step) {
			t.Errorf("init output omitted %q:\n%s", step, text)
		}
	}
	if strings.Contains(text, "[00:") {
		t.Errorf("init output contains elapsed timer:\n%s", text)
	}
	for _, verbose := range []string{"Set up xzap for", "Selected:", "Initialized "} {
		if strings.Contains(text, verbose) {
			t.Errorf("init output contains verbose copy %q:\n%s", verbose, text)
		}
	}
}

func TestDeviceAddSelectsAndSavesPhysicalDevice(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "MyApp.xcodeproj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := internal.SaveConfig(root, internal.Config{
		Project: "MyApp.xcodeproj",
		Devices: map[string]internal.Device{
			"main": {Kind: internal.SimulatorDevice, ID: "APP-SIMULATOR", Name: "iPhone"},
		},
		Defaults:   internal.DeviceDefaults{Run: "main", Build: "main", Test: "main"},
		OnlyErrors: true,
	}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = "devicectl" ]; then
  echo '{"result":{"devices":[{"identifier":"FIRST","properties":{"connection":{"pairingState":"paired","state":"connected"},"hardware":{"marketingName":"iPhone 15","platform":"iOS","reality":"physical","udid":"FIRST"},"state":{"name":"First iPhone"}}},{"identifier":"SECOND","properties":{"connection":{"pairingState":"paired","state":"connected"},"hardware":{"marketingName":"iPhone 16","platform":"iOS","reality":"physical","udid":"SECOND"},"state":{"name":"Second iPhone"}}}]}}'
else
  echo 'unexpected Simulator discovery' >&2
  exit 91
fi
`
	if err := os.WriteFile(filepath.Join(bin, "xcrun"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previousDirectory)

	output, err := os.CreateTemp(root, "output")
	if err != nil {
		t.Fatal(err)
	}
	previousOutput := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previousOutput }()

	if err := manageDevice(context.Background(), command{
		action: commandDevice, deviceAction: "add", deviceName: "phone", physical: "Second iPhone",
	}); err != nil {
		t.Fatalf("manageDevice() error = %v", err)
	}
	config, err := internal.LoadConfig(filepath.Join(root, internal.CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if config.Devices["phone"].ID != "SECOND" || config.Devices["phone"].Kind != internal.PhysicalDevice {
		t.Fatalf("configured device = %#v, want physical SECOND", config.Devices["phone"])
	}
	if config.Devices["main"].ID != "APP-SIMULATOR" || !config.OnlyErrors || config.Defaults.Run != "main" {
		t.Fatalf("device add changed existing configuration: %#v", config)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Added phone") || !strings.Contains(string(data), "Physical") {
		t.Errorf("device add output = %s", data)
	}
}

func TestResolveRunContextRejectsUnknownDevice(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "MyApp.xcodeproj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	config := internal.Config{
		Project: "MyApp.xcodeproj",
		Devices: map[string]internal.Device{
			"main": {Kind: internal.SimulatorDevice, Name: "iPhone", ID: "APP"},
		},
		Defaults: internal.DeviceDefaults{Run: "main", Build: "main", Test: "main"},
	}
	if _, err := internal.SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previousDirectory)

	_, err = resolveRunContext(context.Background(), commandRun, "missing")
	if err == nil || !strings.Contains(err.Error(), `unknown device "missing"`) {
		t.Errorf("resolveRunContext() error = %v", err)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    command
		wantErr string
	}{
		{
			name:    "run requires scheme",
			wantErr: "xzap run requires exactly one scheme",
		},
		{name: "run output flags", args: []string{"run", "Qonto", "--no-color"}, want: command{action: commandRun, scheme: "Qonto", noColor: true}},
		{
			name: "initialize",
			args: []string{"init"},
			want: command{action: commandInit},
		},
		{
			name: "initialize with preset project and devices",
			args: []string{"init", "--project", "MyApp.xcodeproj", "--default-device", "iPhone", "--test-device", "iPad"},
			want: command{action: commandInit, project: "MyApp.xcodeproj", defaultDevice: "iPhone", testDevice: "iPad"},
		},
		{name: "doctor", args: []string{"doctor"}, want: command{action: commandDoctor}},
		{
			name: "help",
			args: []string{"--help"},
			want: command{action: commandRun, help: true},
		},
		{
			name: "initialize help",
			args: []string{"init", "-h"},
			want: command{action: commandInit, help: true},
		},
		{
			name: "version",
			args: []string{"-V"},
			want: command{action: commandRun, version: true},
		},
		{
			name:    "test requires scheme",
			args:    []string{"test"},
			wantErr: "xzap test requires exactly one scheme",
		},
		{
			name: "test identifiers",
			args: []string{"test", "Qonto", "AppTests/LoginTests/testExpiredSession", "AppUITests/OnboardingTests"},
			want: command{
				action: commandTest,
				scheme: "Qonto",
				testIdentifiers: []string{
					"AppTests/LoginTests/testExpiredSession",
					"AppUITests/OnboardingTests",
				},
			},
		},
		{name: "device list", args: []string{"device", "list"}, want: command{action: commandDevice, deviceAction: "list"}},
		{name: "device add simulator", args: []string{"device", "add", "main", "--simulator", "iPhone"}, want: command{action: commandDevice, deviceAction: "add", deviceName: "main", simulator: "iPhone"}},
		{name: "device add physical", args: []string{"device", "add", "phone", "--physical", "Javier's iPhone"}, want: command{action: commandDevice, deviceAction: "add", deviceName: "phone", physical: "Javier's iPhone"}},
		{name: "device remove", args: []string{"device", "remove", "phone"}, want: command{action: commandDevice, deviceAction: "remove", deviceName: "phone"}},
		{name: "device test default", args: []string{"device", "default", "ipad", "--for", "test"}, want: command{action: commandDevice, deviceAction: "default", deviceName: "ipad", defaultFor: "test"}},
		{name: "device requires subcommand", args: []string{"device"}, wantErr: "xzap device requires a command: list, add, remove, or default"},
		{name: "device add requires name", args: []string{"device", "add"}, wantErr: "xzap device add requires exactly one name"},
		{name: "device selectors conflict", args: []string{"device", "add", "phone", "--simulator", "iPhone", "--physical", "Phone"}, wantErr: "--simulator and --physical cannot be combined"},
		{
			name:    "build requires scheme",
			args:    []string{"build"},
			wantErr: "xzap build requires exactly one scheme",
		},
		{name: "removed scheme flag", args: []string{"run", "--scheme", "MyApp"}, wantErr: "flag provided but not defined: -scheme"},
		{name: "build explicit scheme", args: []string{"build", "Qonto"}, want: command{action: commandBuild, scheme: "Qonto"}},
		{name: "build on device", args: []string{"build", "Qonto", "--on", "phone"}, want: command{action: commandBuild, scheme: "Qonto", on: "phone"}},
		{name: "clean requires scheme", args: []string{"clean"}, wantErr: "xzap clean requires exactly one scheme"},
		{name: "clean explicit scheme", args: []string{"clean", "Qonto"}, want: command{action: commandClean, scheme: "Qonto"}},
		{name: "clean on device", args: []string{"clean", "Qonto", "--on", "phone"}, want: command{action: commandClean, scheme: "Qonto", on: "phone"}},
		{name: "doctor rejects on", args: []string{"doctor", "--on", "phone"}, wantErr: "--on is only valid with build, clean, run, or test"},
		{name: "run explicit scheme", args: []string{"run", "Qonto"}, want: command{action: commandRun, scheme: "Qonto"}},
		{name: "run on device", args: []string{"run", "Qonto", "-o", "phone"}, want: command{action: commandRun, scheme: "Qonto", on: "phone"}},
		{name: "test on device", args: []string{"test", "Qonto", "--on", "ipad"}, want: command{action: commandTest, scheme: "Qonto", on: "ipad", testIdentifiers: []string{}}},
		{name: "removed device flag", args: []string{"run", "Qonto", "--device"}, wantErr: "flag provided but not defined: -device"},
		{name: "JSON removed", args: []string{"run", "Qonto", "--json"}, wantErr: "flag provided but not defined: -json"},
		{name: "completion command removed", args: []string{"completion"}, wantErr: `unknown command "completion"`},
		{name: "target command removed", args: []string{"target"}, wantErr: `unknown command "target"`},
		{name: "scheme command removed", args: []string{"scheme"}, wantErr: `unknown command "scheme"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCommand(test.args)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Errorf("parseCommand() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCommand() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseCommand() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name          string
		linkedVersion string
		moduleVersion string
		want          string
	}{
		{name: "release installed with go install", moduleVersion: "v0.3.0", want: "v0.3.0"},
		{name: "local development build", moduleVersion: "(devel)", want: "dev"},
		{name: "release binary", linkedVersion: "v0.3.0", moduleVersion: "(devel)", want: "v0.3.0"},
		{name: "explicit development build", linkedVersion: "dev", moduleVersion: "v0.3.0", want: "dev"},
		{name: "linked version takes precedence", linkedVersion: "v0.4.0", moduleVersion: "v0.3.0", want: "v0.4.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveVersion(test.linkedVersion, test.moduleVersion); got != test.want {
				t.Errorf("resolveVersion(%q, %q) = %q, want %q", test.linkedVersion, test.moduleVersion, got, test.want)
			}
		})
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{usageError{"bad usage"}, exitUsage},
		{internal.ErrBuildFailed, exitBuild},
		{internal.ErrCleanFailed, exitBuild},
		{timedError{cause: internal.ErrBuildFailed, duration: 7200 * time.Millisecond}, exitBuild},
		{internal.ErrTestsFailed, exitTests},
		{context.Canceled, exitInterrupted},
		{os.ErrNotExist, exitRuntime},
	}
	for _, test := range tests {
		if got := exitCode(test.err); got != test.want {
			t.Errorf("exitCode(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestTimedErrorIncludesDuration(t *testing.T) {
	err := timedError{cause: internal.ErrBuildFailed, duration: 7200 * time.Millisecond}
	if got, want := err.Error(), "build failed after 7.2s"; got != want {
		t.Errorf("timed error = %q, want %q", got, want)
	}
}
