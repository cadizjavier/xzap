package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrSimulatorDetectionFailed = errors.New("failed to detect an available iOS simulator")
	ErrSimulatorNotFound        = errors.New("simulator not found")
)

type Runtime struct {
	Identifier string `json:"identifier"`
	Version    string `json:"version"`
}

type Simulator struct {
	UDID         string `json:"udid"`
	Name         string `json:"name"`
	State        string `json:"state"`
	IsAvailable  bool   `json:"isAvailable"`
	Availability string `json:"availability"`
	Runtime      string
}

func detectSimulator(simulators []Simulator) (Simulator, error) {
	for _, simulator := range simulators {
		if simulator.State == "Booted" {
			return simulator, nil
		}
	}
	for _, simulator := range simulators {
		if simulator.IsAvailable && strings.Contains(simulator.Name, "iPhone") {
			return simulator, nil
		}
	}
	for _, simulator := range simulators {
		if simulator.IsAvailable {
			return simulator, nil
		}
	}
	return Simulator{}, ErrSimulatorDetectionFailed
}

func resolveSimulator(simulators []Simulator, id, name string) (Simulator, error) {
	if id != "" {
		for _, simulator := range simulators {
			if simulator.UDID == id && simulator.IsAvailable {
				return simulator, nil
			}
		}
		return Simulator{}, fmt.Errorf("%w: %s", ErrSimulatorNotFound, id)
	}
	if name == "" {
		return detectSimulator(simulators)
	}
	var matches []Simulator
	for _, simulator := range simulators {
		if (simulator.Name == name || simulator.UDID == name) && simulator.IsAvailable {
			matches = append(matches, simulator)
		}
	}
	switch len(matches) {
	case 0:
		return Simulator{}, fmt.Errorf("%w: %s", ErrSimulatorNotFound, name)
	case 1:
		return matches[0], nil
	default:
		sort.Slice(matches, func(i, j int) bool {
			return simulatorLess(matches[i], matches[j])
		})
		return matches[0], nil
	}
}

func listSimulators(ctx context.Context) ([]Simulator, error) {
	runtimeOutput, err := commandOutput(ctx, "xcrun", "simctl", "list", "runtimes", "--json")
	if err != nil {
		return nil, fmt.Errorf("%w: list runtimes: %w", ErrSimulatorDetectionFailed, err)
	}
	var runtimeData struct {
		Runtimes []Runtime `json:"runtimes"`
	}
	if err := json.Unmarshal(runtimeOutput, &runtimeData); err != nil {
		return nil, fmt.Errorf("%w: decode runtimes: %v", ErrSimulatorDetectionFailed, err)
	}
	runtimes := make(map[string]Runtime)
	for _, runtime := range runtimeData.Runtimes {
		if strings.Contains(runtime.Identifier, "iOS") {
			runtimes[runtime.Identifier] = runtime
		}
	}

	deviceOutput, err := commandOutput(ctx, "xcrun", "simctl", "list", "devices", "available", "--json")
	if err != nil {
		return nil, fmt.Errorf("%w: list devices: %w", ErrSimulatorDetectionFailed, err)
	}
	var deviceData struct {
		Devices map[string][]Simulator `json:"devices"`
	}
	if err := json.Unmarshal(deviceOutput, &deviceData); err != nil {
		return nil, fmt.Errorf("%w: decode devices: %v", ErrSimulatorDetectionFailed, err)
	}

	var simulators []Simulator
	for runtimeID, devices := range deviceData.Devices {
		runtime, isIOS := runtimes[runtimeID]
		if !isIOS {
			continue
		}
		for _, simulator := range devices {
			if !simulator.IsAvailable {
				simulator.IsAvailable = !strings.Contains(simulator.Availability, "unavailable")
			}
			simulator.Runtime = runtime.Version
			simulators = append(simulators, simulator)
		}
	}
	sort.Slice(simulators, func(i, j int) bool {
		return simulatorLess(simulators[i], simulators[j])
	})
	return simulators, nil
}

func simulatorLess(first, second Simulator) bool {
	if first.State == "Booted" != (second.State == "Booted") {
		return first.State == "Booted"
	}
	if first.Runtime != second.Runtime {
		return versionGreater(first.Runtime, second.Runtime)
	}
	if first.Name != second.Name {
		return first.Name < second.Name
	}
	return first.UDID < second.UDID
}

func versionGreater(first, second string) bool {
	left, right := strings.Split(first, "."), strings.Split(second, ".")
	for index := 0; index < len(left) || index < len(right); index++ {
		var a, b int
		if index < len(left) {
			a, _ = strconv.Atoi(left[index])
		}
		if index < len(right) {
			b, _ = strconv.Atoi(right[index])
		}
		if a != b {
			return a > b
		}
	}
	return false
}

func BootTarget(ctx context.Context, target Target) error {
	return boot(ctx, target, true)
}

func BootTargetQuiet(ctx context.Context, target Target) error {
	return boot(ctx, target, false)
}

func boot(ctx context.Context, target Target, showProgress bool) error {
	if target.State == "Booted" {
		return nil
	}
	var spinner *Spinner
	if showProgress {
		spinner = NewOperation("Booting "+target.Name, "Booted "+target.Name)
		spinner.Start()
	}
	if _, err := commandOutput(ctx, "xcrun", "simctl", "boot", target.ID); err != nil {
		if spinner != nil {
			spinner.Stop(false)
		}
		return fmt.Errorf("boot simulator: %w", err)
	}
	if _, err := commandOutput(ctx, "xcrun", "simctl", "bootstatus", target.ID, "-b"); err != nil {
		if spinner != nil {
			spinner.Stop(false)
		}
		return fmt.Errorf("wait for simulator: %w", err)
	}
	if spinner != nil {
		spinner.Stop(true)
	}
	return nil
}

func BundleIDFromApp(ctx context.Context, appPath string) (string, error) {
	output, err := commandOutput(ctx, "plutil", "-extract", "CFBundleIdentifier", "raw", appPath+"/Info.plist")
	if err != nil {
		return "", fmt.Errorf("read bundle ID: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func InstallTarget(ctx context.Context, target Target, appPath string) error {
	spinner := NewOperation("Installing app", "Installed app")
	spinner.Start()
	if _, err := commandOutput(ctx, "xcrun", "simctl", "install", target.ID, appPath); err != nil {
		spinner.Stop(false)
		return fmt.Errorf("install app: %w", err)
	}
	spinner.Stop(true)
	return nil
}

func LaunchTarget(ctx context.Context, target Target, bundleID string) error {
	spinner := NewActivity("Launching app → " + target.Name)
	spinner.Start()
	if _, err := commandOutput(ctx, "xcrun", "simctl", "launch", target.ID, bundleID); err != nil {
		spinner.Stop(false)
		return fmt.Errorf("launch app: %w", err)
	}
	spinner.Stop(true)
	return nil
}

type outputCommand func(context.Context, string, ...string) ([]byte, error)

func ShowTarget(ctx context.Context, target Target) error {
	output, err := commandOutput(ctx, "xcodebuild", "-version")
	if err != nil {
		return fmt.Errorf("detect Xcode version: %w", err)
	}
	majorVersion, err := xcodeMajorVersion(string(output))
	if err != nil {
		return err
	}
	if majorVersion >= 27 {
		deviceURL := url.URL{
			Scheme:   "devices",
			Host:     "device",
			Path:     "/open",
			RawQuery: url.Values{"id": {target.ID}}.Encode(),
		}
		if _, err := commandOutput(ctx, "open", deviceURL.String()); err != nil {
			return fmt.Errorf("show simulator in Device Hub: %w", err)
		}
		return nil
	}
	if _, err := commandOutput(ctx, "open", "-a", "Simulator"); err != nil {
		return fmt.Errorf("show simulator: %w", err)
	}
	return nil
}

func xcodeMajorVersion(output string) (int, error) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "Xcode ") {
			continue
		}
		version := strings.TrimSpace(strings.TrimPrefix(line, "Xcode "))
		major, _, _ := strings.Cut(version, ".")
		value, err := strconv.Atoi(major)
		if err != nil {
			break
		}
		return value, nil
	}
	return 0, fmt.Errorf("detect Xcode version: unexpected output %q", strings.TrimSpace(output))
}
