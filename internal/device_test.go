package internal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseDevicesFiltersSimulatorsAndMarksAvailability(t *testing.T) {
	devices, err := parseDevices([]byte(`{
		"result": {"devices": [
			{
				"identifier": "CORE-DEVICE",
				"visibilityClass": "default",
				"properties": {
					"connection": {"pairingState": "paired", "state": "disconnected"},
					"hardware": {"marketingName": "iPhone 15 Pro", "platform": "iOS", "reality": "physical", "udid": "DEVICE-UDID"},
					"software": {"osVersionNumber": {"stringValue": "27.0"}},
					"state": {"name": "Javico"}
				}
			},
			{
				"identifier": "SIMULATOR-ID",
				"visibilityClass": "simulators",
				"properties": {
					"connection": {"pairingState": "paired", "state": "connected"},
					"hardware": {"marketingName": "iPhone 17 Pro", "platform": "iOS", "reality": "simulated", "udid": "SIMULATOR-ID"},
					"state": {"name": "iPhone 17 Pro"}
				}
			},
			{
				"identifier": "OLD-DEVICE",
				"visibilityClass": "default",
				"properties": {
					"connection": {"pairingState": "paired", "state": "unavailable"},
					"hardware": {"productType": "iPhone14,7", "platform": "iOS", "reality": "physical", "udid": "OLD-UDID"},
					"state": {"name": "Old iPhone"}
				}
			}
		]}
	}`))
	if err != nil {
		t.Fatalf("parseDevices() error = %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("parseDevices() returned %d devices, want 2: %#v", len(devices), devices)
	}
	if got := devices[0]; got.ID != "DEVICE-UDID" || got.Name != "Javico" || got.Model != "iPhone 15 Pro" || got.OSVersion != "27.0" || !got.IsAvailable {
		t.Errorf("available device = %#v", got)
	}
	if devices[1].IsAvailable {
		t.Errorf("unavailable device = %#v", devices[1])
	}
}

func TestResolveDeviceUsesStableIDAndRecoversByName(t *testing.T) {
	devices := []Target{
		{ID: "FIRST", Name: "iPhone", IsAvailable: true},
		{ID: "SAVED", Name: "iPhone", IsAvailable: true},
	}
	got, err := resolveDevice(devices, "SAVED", "iPhone")
	if err != nil || got.ID != "SAVED" {
		t.Fatalf("resolveDevice() = %#v, %v", got, err)
	}
	got, err = resolveDevice(devices, "", "iPhone")
	if err != nil || got.ID != "FIRST" {
		t.Fatalf("resolveDevice() recovery = %#v, %v", got, err)
	}
}

func TestPhysicalDeviceInstallAndLaunchCommands(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}
	target := Target{ID: "DEVICE-UDID", Name: "Javico"}
	if err := installDeviceWith(context.Background(), target, "/tmp/MyApp.app", run); err != nil {
		t.Fatalf("installDeviceWith() error = %v", err)
	}
	if err := launchDeviceWithRetry(context.Background(), target, "com.example.MyApp", run, waitForDeviceRetry); err != nil {
		t.Fatalf("launchDeviceWithRetry() error = %v", err)
	}
	want := [][]string{
		{"xcrun", "devicectl", "device", "install", "app", "--device", "DEVICE-UDID", "/tmp/MyApp.app"},
		{"xcrun", "devicectl", "device", "process", "launch", "--device", "DEVICE-UDID", "--terminate-existing", "com.example.MyApp"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("commands = %#v, want %#v", calls, want)
	}
}

func TestLaunchDeviceRetriesWhileLocked(t *testing.T) {
	attempts := 0
	waits := 0
	err := launchDeviceWithRetry(
		context.Background(),
		Target{ID: "DEVICE-UDID", Name: "Javico"},
		"com.example.MyApp",
		func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("the device must be unlocked")
			}
			return nil, nil
		},
		func(_ context.Context, delay time.Duration) error {
			waits++
			if delay != 5*time.Second {
				t.Errorf("retry delay = %s", delay)
			}
			return nil
		},
	)
	if err != nil || attempts != 2 || waits != 1 {
		t.Errorf("result = %v, attempts = %d, waits = %d", err, attempts, waits)
	}
}

func TestLaunchDeviceDoesNotRetryOtherFailures(t *testing.T) {
	err := launchDeviceWithRetry(
		context.Background(),
		Target{ID: "DEVICE-UDID", Name: "Javico"},
		"com.example.MyApp",
		func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("application is not signed")
		},
		func(_ context.Context, _ time.Duration) error {
			t.Fatal("unexpected retry")
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "application is not signed") {
		t.Errorf("launchDeviceWithRetry() error = %v", err)
	}
}
