package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
)

var ErrTargetNotFound = errors.New("target not found")

type Target struct {
	Name string `json:"name"`
	ID   string `json:"id"`

	Model       string `json:"-"`
	OSVersion   string `json:"-"`
	State       string `json:"-"`
	IsAvailable bool   `json:"-"`
}

func ListTargets(ctx context.Context) ([]Target, error) {
	simulators, err := listSimulators(ctx)
	if err != nil {
		return nil, err
	}
	targets := make([]Target, 0, len(simulators))
	for _, simulator := range simulators {
		if simulator.IsAvailable {
			targets = append(targets, targetFromSimulator(simulator))
		}
	}
	return targets, nil
}

func ListAvailableDevices(ctx context.Context) ([]Device, error) {
	targets, err := ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	physical, err := ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(targets)+len(physical))
	for _, target := range targets {
		devices = append(devices, DeviceFromTarget(SimulatorDevice, target))
	}
	for _, target := range physical {
		if target.IsAvailable {
			devices = append(devices, DeviceFromTarget(PhysicalDevice, target))
		}
	}
	return devices, nil
}

func DeviceFromTarget(kind DeviceKind, target Target) Device {
	return Device{
		Kind: kind, Name: target.Name, ID: target.ID,
		Model: target.Model, OSVersion: target.OSVersion, State: target.State,
		IsAvailable: target.IsAvailable,
	}
}

func (device Device) Target() Target {
	return Target{
		Name: device.Name, ID: device.ID, Model: device.Model,
		OSVersion: device.OSVersion, State: device.State, IsAvailable: device.IsAvailable,
	}
}

func ResolveConfiguredDevice(ctx context.Context, configured Device) (Device, bool, error) {
	var resolved Target
	var recovered bool
	var err error
	if configured.Kind == PhysicalDevice {
		resolved, recovered, err = ResolveDevice(ctx, configured.Target())
	} else {
		resolved, recovered, err = ResolveTarget(ctx, configured.Target())
	}
	if err != nil {
		return Device{}, false, err
	}
	return DeviceFromTarget(configured.Kind, resolved), recovered, nil
}

func MatchDevice(devices []Device, query string, kind DeviceKind) (Device, error) {
	var matches []Device
	for _, device := range devices {
		if kind != "" && device.Kind != kind {
			continue
		}
		if device.IsAvailable && (device.ID == query || device.Name == query || DeviceDescription(device) == query) {
			matches = append(matches, device)
		}
	}
	if len(matches) == 0 {
		return Device{}, fmt.Errorf("%w: %s", ErrTargetNotFound, query)
	}
	if len(matches) > 1 {
		return Device{}, fmt.Errorf("device %q matches multiple devices; use its ID", query)
	}
	return matches[0], nil
}

func SelectConfiguredDevice(devices []Device, current Device, input io.Reader, output io.Writer) (Device, error) {
	if len(devices) == 0 {
		return Device{}, ErrTargetNotFound
	}
	labels := make([]string, len(devices))
	byLabel := make(map[string]Device, len(devices))
	defaultLabel := ""
	for index, device := range devices {
		label := DeviceDescription(device)
		if _, exists := byLabel[label]; exists {
			label += " [" + device.ID + "]"
		}
		if device.Kind == current.Kind && device.ID == current.ID {
			label += " [current]"
			defaultLabel = label
		}
		labels[index] = label
		byLabel[label] = device
	}
	selected, err := selectValueDefault("Device", labels, defaultLabel, input, output)
	if err != nil {
		return Device{}, err
	}
	return byLabel[selected], nil
}

func DeviceDescription(device Device) string {
	kind := "Simulator"
	if device.Kind == PhysicalDevice {
		kind = "Physical"
	}
	return fmt.Sprintf("%-9s %s", kind, TargetDescription(device.Target()))
}

func ResolveTarget(ctx context.Context, configured Target) (Target, bool, error) {
	simulators, err := listSimulators(ctx)
	if err != nil {
		return Target{}, false, err
	}
	return resolveTargetFromSimulators(simulators, configured)
}

func resolveTargetFromSimulators(simulators []Simulator, configured Target) (Target, bool, error) {
	resolved, err := resolveSimulator(simulators, configured.ID, configured.Name)
	if err == nil {
		return targetFromSimulator(resolved), false, nil
	}
	resolved, recoveryErr := resolveSimulator(simulators, "", configured.Name)
	if recoveryErr == nil {
		return targetFromSimulator(resolved), true, nil
	}
	return Target{}, false, err
}

func TargetDescription(target Target) string {
	detail := target.Model
	if target.OSVersion != "" {
		if detail != "" {
			detail += ", "
		}
		detail += "iOS " + target.OSVersion
	}
	if detail == "" {
		return target.Name
	}
	return fmt.Sprintf("%s (%s)", target.Name, detail)
}

func TargetDestination(target Target) string {
	return fmt.Sprintf("platform=iOS Simulator,id=%s", target.ID)
}

func DeviceDestination(device Target) string {
	return fmt.Sprintf("platform=iOS,id=%s", device.ID)
}

func targetFromSimulator(simulator Simulator) Target {
	return Target{
		Name:        simulator.Name,
		ID:          simulator.UDID,
		OSVersion:   simulator.Runtime,
		State:       simulator.State,
		IsAvailable: simulator.IsAvailable,
	}
}

func sortTargets(targets []Target) {
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].IsAvailable != targets[j].IsAvailable {
			return targets[i].IsAvailable
		}
		if targets[i].Name != targets[j].Name {
			return targets[i].Name < targets[j].Name
		}
		return targets[i].ID < targets[j].ID
	})
}
