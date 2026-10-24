package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrDeviceDetectionFailed = errors.New("failed to detect a paired physical iOS device")
	ErrDeviceNotFound        = errors.New("physical device not found")
)

const deviceUnlockRetryDelay = 5 * time.Second

type deviceList struct {
	Result struct {
		Devices []struct {
			Identifier      string `json:"identifier"`
			VisibilityClass string `json:"visibilityClass"`
			Properties      struct {
				Connection struct {
					PairingState string `json:"pairingState"`
					State        string `json:"state"`
				} `json:"connection"`
				Hardware struct {
					MarketingName string `json:"marketingName"`
					Platform      string `json:"platform"`
					ProductType   string `json:"productType"`
					Reality       string `json:"reality"`
					UDID          string `json:"udid"`
				} `json:"hardware"`
				Software struct {
					OSVersion struct {
						StringValue string `json:"stringValue"`
					} `json:"osVersionNumber"`
				} `json:"software"`
				State struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"properties"`
		} `json:"devices"`
	} `json:"result"`
}

func ListDevices(ctx context.Context) ([]Target, error) {
	output, err := commandOutput(
		ctx,
		"xcrun", "devicectl", "list", "devices",
		"--quiet", "--json-output", "-", "--omit-deprecated-fields-in-json",
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceDetectionFailed, err)
	}
	return parseDevices(output)
}

func parseDevices(output []byte) ([]Target, error) {
	var data deviceList
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, fmt.Errorf("%w: decode devices: %v", ErrDeviceDetectionFailed, err)
	}

	var devices []Target
	for _, device := range data.Result.Devices {
		properties := device.Properties
		if properties.Hardware.Platform != "iOS" || properties.Hardware.Reality == "simulated" || device.VisibilityClass == "simulators" {
			continue
		}
		id := properties.Hardware.UDID
		if id == "" {
			id = device.Identifier
		}
		model := properties.Hardware.MarketingName
		if model == "" {
			model = properties.Hardware.ProductType
		}
		devices = append(devices, Target{
			Name:        properties.State.Name,
			ID:          id,
			Model:       model,
			OSVersion:   properties.Software.OSVersion.StringValue,
			State:       properties.Connection.State,
			IsAvailable: id != "" && properties.State.Name != "" && properties.Connection.PairingState == "paired" && properties.Connection.State != "unavailable",
		})
	}
	sortTargets(devices)
	return devices, nil
}

func ResolveDevice(ctx context.Context, configured Target) (Target, bool, error) {
	devices, err := ListDevices(ctx)
	if err != nil {
		return Target{}, false, err
	}
	resolved, err := resolveDevice(devices, configured.ID, configured.Name)
	if err == nil {
		return resolved, false, nil
	}
	resolved, recoveryErr := resolveDevice(devices, "", configured.Name)
	if recoveryErr == nil {
		return resolved, true, nil
	}
	return Target{}, false, err
}

func resolveDevice(devices []Target, id, name string) (Target, error) {
	for _, device := range devices {
		if device.IsAvailable && ((id != "" && device.ID == id) || (id == "" && device.Name == name)) {
			return device, nil
		}
	}
	value := id
	if value == "" {
		value = name
	}
	return Target{}, fmt.Errorf("%w: %s", ErrDeviceNotFound, value)
}

func InstallDevice(ctx context.Context, target Target, appPath string) error {
	return installDeviceWith(ctx, target, appPath, commandOutput)
}

func installDeviceWith(ctx context.Context, target Target, appPath string, run outputCommand) error {
	spinner := NewOperation("Installing app", "Installed app")
	spinner.Start()
	if _, err := run(ctx, "xcrun", "devicectl", "device", "install", "app", "--device", target.ID, appPath); err != nil {
		spinner.Stop(false)
		return fmt.Errorf("install app: %w", err)
	}
	spinner.Stop(true)
	return nil
}

func LaunchDevice(ctx context.Context, target Target, bundleID string) error {
	return launchDeviceWithRetry(ctx, target, bundleID, commandOutput, waitForDeviceRetry)
}

func launchDeviceWithRetry(
	ctx context.Context,
	target Target,
	bundleID string,
	run outputCommand,
	wait func(context.Context, time.Duration) error,
) error {
	for {
		spinner := NewActivity("Launching app → " + target.Name)
		spinner.Start()
		_, err := run(ctx, "xcrun", "devicectl", "device", "process", "launch", "--device", target.ID, "--terminate-existing", bundleID)
		if err == nil {
			spinner.Stop(true)
			return nil
		}
		spinner.Stop(false)
		if !isDeviceLockedError(err) {
			return fmt.Errorf("launch app: %w", err)
		}
		fmt.Printf("  Device %s is locked. Unlock it; retrying in %s.\n", target.Name, deviceUnlockRetryDelay)
		if err := wait(ctx, deviceUnlockRetryDelay); err != nil {
			return err
		}
	}
}

func isDeviceLockedError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "locked") || strings.Contains(message, "unlock")
}

func waitForDeviceRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
