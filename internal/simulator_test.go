package internal

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestResolveSimulatorPrefersExactID(t *testing.T) {
	simulators := []Simulator{
		{UDID: "NEW", Name: "iPhone", Runtime: "18.0", IsAvailable: true},
		{UDID: "SAVED", Name: "iPhone", Runtime: "17.0", IsAvailable: true},
	}

	got, err := resolveSimulator(simulators, "SAVED", "iPhone")
	if err != nil {
		t.Fatalf("resolveSimulator() error = %v", err)
	}
	if got.UDID != "SAVED" {
		t.Errorf("resolveSimulator() UDID = %q, want SAVED", got.UDID)
	}
}

func TestResolveSimulatorRecoveryPrefersBootedMatch(t *testing.T) {
	simulators := []Simulator{
		{UDID: "NEW", Name: "iPhone", Runtime: "18.0", State: "Shutdown", IsAvailable: true},
		{UDID: "BOOTED", Name: "iPhone", Runtime: "17.0", State: "Booted", IsAvailable: true},
	}

	got, err := resolveSimulator(simulators, "", "iPhone")
	if err != nil {
		t.Fatalf("resolveSimulator() error = %v", err)
	}
	if got.UDID != "BOOTED" {
		t.Errorf("resolveSimulator() UDID = %q, want BOOTED", got.UDID)
	}
}

func TestResolveSimulatorRecoveryPrefersNewestRuntime(t *testing.T) {
	simulators := []Simulator{
		{UDID: "OLDER", Name: "iPhone", Runtime: "17.5", State: "Shutdown", IsAvailable: true},
		{UDID: "NEWEST", Name: "iPhone", Runtime: "18.2", State: "Shutdown", IsAvailable: true},
		{UDID: "MIDDLE", Name: "iPhone", Runtime: "18.1", State: "Shutdown", IsAvailable: true},
	}

	got, err := resolveSimulator(simulators, "", "iPhone")
	if err != nil {
		t.Fatalf("resolveSimulator() error = %v", err)
	}
	if got.UDID != "NEWEST" {
		t.Errorf("resolveSimulator() UDID = %q, want NEWEST", got.UDID)
	}
}

func TestResolveSimulatorIgnoresUnavailableMatches(t *testing.T) {
	simulators := []Simulator{
		{UDID: "UNAVAILABLE", Name: "iPhone", Runtime: "18.0"},
		{UDID: "AVAILABLE", Name: "iPhone", Runtime: "17.0", IsAvailable: true},
	}

	got, err := resolveSimulator(simulators, "", "iPhone")
	if err != nil {
		t.Fatalf("resolveSimulator() error = %v", err)
	}
	if got.UDID != "AVAILABLE" {
		t.Errorf("resolveSimulator() UDID = %q, want AVAILABLE", got.UDID)
	}
}

func TestDetectSimulatorFallsBackToAvailableNonPhone(t *testing.T) {
	got, err := detectSimulator([]Simulator{
		{UDID: "IPAD", Name: "iPad Pro", Runtime: "18.0", IsAvailable: true},
	})
	if err != nil {
		t.Fatalf("detectSimulator() error = %v", err)
	}
	if got.UDID != "IPAD" {
		t.Errorf("detectSimulator() UDID = %q, want IPAD", got.UDID)
	}
}

func TestShowTarget(t *testing.T) {
	const id = "01234567-89AB-CDEF-0123-456789ABCDEF"
	for _, test := range []struct {
		name, version, wantError string
		openArgs                 []string
		failOpen                 bool
	}{
		{name: "Device Hub", version: "Xcode 27.0\nBuild version 27A5228h", openArgs: []string{"devices://device/open?id=" + id}},
		{name: "Simulator app", version: "Xcode 26.4\nBuild version 17E214", openArgs: []string{"-a", "Simulator"}},
		{name: "invalid version", version: "not Xcode version output", wantError: "unexpected output"},
		{name: "open failure", version: "Xcode 27.0", openArgs: []string{"devices://device/open?id=" + id}, failOpen: true, wantError: "Device Hub"},
	} {
		t.Run(test.name, func(t *testing.T) {
			versionLog := installRecordingCommand(t, "xcodebuild", "printf '%s\\n' "+shellQuote(test.version))
			behavior := ""
			if test.failOpen {
				behavior = "echo 'open failed' >&2; exit 1"
			}
			openLog := installRecordingCommand(t, "open", behavior)
			err := ShowTarget(context.Background(), Target{ID: id})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("ShowTarget() error = %v, want %q", err, test.wantError)
				}
			} else if err != nil {
				t.Fatalf("ShowTarget() error = %v", err)
			}
			assertCommandCalls(t, versionLog, [][]string{{"-version"}})
			if test.openArgs != nil {
				assertCommandCalls(t, openLog, [][]string{test.openArgs})
			} else if _, err := os.Stat(openLog); !os.IsNotExist(err) {
				t.Fatalf("open called after invalid Xcode version: %v", err)
			}
		})
	}
}
