package internal

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestCommandOutputIncludesCleanStderr(t *testing.T) {
	_, err := commandOutput(
		context.Background(),
		"/bin/sh",
		"-c",
		"printf 'duplicate\\nduplicate\\nactionable detail\\n' >&2; exit 7",
	)
	if err == nil {
		t.Fatal("commandOutput() error = nil, want an error")
	}
	if got, want := err.Error(), "duplicate\nactionable detail"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Errorf("error does not wrap exec.ExitError: %v", err)
	}
}

func TestActionableCommandErrorPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := actionableCommandError(ctx, errors.New("signal: killed"), "misleading subprocess output")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), "misleading") {
		t.Errorf("cancellation included subprocess output: %v", err)
	}
}

func TestCompactCommandErrorKeepsBoundedTail(t *testing.T) {
	var output strings.Builder
	for index := 0; index < maxCommandErrorLines+3; index++ {
		output.WriteString("line ")
		output.WriteRune(rune('A' + index))
		output.WriteByte('\n')
	}

	got := compactCommandError(output.String())
	if !strings.HasPrefix(got, "…\n") {
		t.Errorf("compact output = %q, want truncation marker", got)
	}
	if strings.Contains(got, "line A") {
		t.Errorf("compact output retained oldest line: %q", got)
	}
	if !strings.Contains(got, "line K") {
		t.Errorf("compact output omitted newest line: %q", got)
	}
	if lines := strings.Count(got, "\n") + 1; lines != maxCommandErrorLines+1 {
		t.Errorf("compact output has %d lines, want %d including marker", lines, maxCommandErrorLines+1)
	}
}

func TestActionableCommandErrorFallsBackToCause(t *testing.T) {
	cause := errors.New("exit status 1")
	err := actionableCommandError(context.Background(), cause, "\n")
	if got, want := err.Error(), cause.Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Errorf("error does not wrap cause: %v", err)
	}
}
