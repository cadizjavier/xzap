package internal

import (
	"bytes"
	"testing"
	"time"
)

func TestCapturedProgressPrintsOnlyCompletedResult(t *testing.T) {
	var progressOutput, resultOutput bytes.Buffer
	progress := newOperation("Building MyApp", "Built MyApp", false, false, &progressOutput, &resultOutput)

	progress.Start()
	progress.started = time.Now().Add(-1500 * time.Millisecond)
	progress.Stop(true)

	if got, want := resultOutput.String(), "✓ Built MyApp in 1.5s\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if got := progressOutput.String(); got != "" {
		t.Errorf("captured progress = %q, want no intermediate output", got)
	}
}

func TestAnimatedProgressUsesCarriageReturn(t *testing.T) {
	var output bytes.Buffer
	progress := newProgressStyle("Building MyApp", true, true, true, &output)

	progress.Start()
	progress.Stop(false)

	if got := output.String(); got != "\r\033[2K" {
		t.Errorf("animated output = %q, want cleared progress without a failure result", got)
	}
}

func TestStepProgressOmitsElapsedTime(t *testing.T) {
	var output bytes.Buffer
	progress := newProgressStyle("Discovering projects", false, false, false, &output)
	progress.Start()
	progress.Stop(true)
	if got, want := output.String(), "✓ Discovering projects\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestFormatDuration(t *testing.T) {
	if got := FormatDuration(18432 * time.Millisecond); got != "18.4s" {
		t.Errorf("FormatDuration() = %q, want 18.4s", got)
	}
	if got := FormatDuration(62*time.Second + 340*time.Millisecond); got != "1m 2.3s" {
		t.Errorf("FormatDuration() = %q, want 1m 2.3s", got)
	}
}
