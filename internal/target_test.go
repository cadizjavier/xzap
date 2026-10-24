package internal

import (
	"bytes"
	"testing"
)

func TestTargetDestination(t *testing.T) {
	tests := []struct {
		target Target
		want   string
	}{
		{
			target: Target{ID: "SIMULATOR-ID"},
			want:   "platform=iOS Simulator,id=SIMULATOR-ID",
		},
	}
	for _, test := range tests {
		if got := TargetDestination(test.target); got != test.want {
			t.Errorf("TargetDestination(%q) = %q, want %q", test.target.ID, got, test.want)
		}
	}
}

func TestDeviceDestination(t *testing.T) {
	device := Target{ID: "DEVICE-ID"}
	if got, want := DeviceDestination(device), "platform=iOS,id=DEVICE-ID"; got != want {
		t.Errorf("DeviceDestination() = %q, want %q", got, want)
	}
}

func TestResolveTargetsFromOneSimulatorSnapshot(t *testing.T) {
	simulators := []Simulator{
		{UDID: "APP", Name: "iPhone", Runtime: "18.0", IsAvailable: true},
		{UDID: "NEW-TEST", Name: "iPad", Runtime: "18.0", IsAvailable: true},
	}
	app, appRecovered, err := resolveTargetFromSimulators(simulators, Target{ID: "APP", Name: "iPhone"})
	if err != nil || app.ID != "APP" || appRecovered {
		t.Fatalf("app resolution = %#v, %t, %v", app, appRecovered, err)
	}
	test, testRecovered, err := resolveTargetFromSimulators(simulators, Target{ID: "STALE", Name: "iPad"})
	if err != nil || test.ID != "NEW-TEST" || !testRecovered {
		t.Fatalf("test resolution = %#v, %t, %v", test, testRecovered, err)
	}
}

func TestMatchDeviceRejectsAmbiguousNames(t *testing.T) {
	devices := []Device{
		{Kind: SimulatorDevice, ID: "ONE", Name: "iPhone", IsAvailable: true},
		{Kind: SimulatorDevice, ID: "TWO", Name: "iPhone", IsAvailable: true},
	}
	if _, err := MatchDevice(devices, "iPhone", SimulatorDevice); err == nil {
		t.Fatal("MatchDevice() error = nil, want ambiguity error")
	}
	got, err := MatchDevice(devices, "TWO", SimulatorDevice)
	if err != nil || got.ID != "TWO" {
		t.Fatalf("MatchDevice() = %#v, %v", got, err)
	}
}

func TestSelectConfiguredDeviceKeepsCurrent(t *testing.T) {
	devices := []Device{
		{Kind: SimulatorDevice, ID: "ONE", Name: "iPhone", IsAvailable: true},
		{Kind: PhysicalDevice, ID: "TWO", Name: "iPhone", IsAvailable: true},
	}
	got, err := SelectConfiguredDevice(devices, devices[1], bytes.NewBufferString("\n"), &bytes.Buffer{})
	if err != nil || got.Kind != PhysicalDevice || got.ID != "TWO" {
		t.Fatalf("SelectConfiguredDevice() = %#v, %v", got, err)
	}
}
