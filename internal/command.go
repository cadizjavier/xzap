package internal

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

const (
	maxCommandErrorLines = 8
	maxCommandErrorRunes = 1600
)

type commandFailure struct {
	cause   error
	details string
}

func (e commandFailure) Error() string {
	if e.details != "" {
		return e.details
	}
	return e.cause.Error()
}

func (e commandFailure) Unwrap() error {
	return e.cause
}

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, actionableCommandError(ctx, err, stderr.String())
	}
	return output, nil
}

func actionableCommandError(ctx context.Context, cause error, stderr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return commandFailure{
		cause:   cause,
		details: compactCommandError(stderr),
	}
}

func compactCommandError(output string) string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	seen := make(map[string]struct{})
	kept := make([]string, 0, maxCommandErrorLines)
	truncated := false

	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if _, exists := seen[line]; exists {
			continue
		}
		if len(kept) == maxCommandErrorLines {
			truncated = true
			break
		}
		seen[line] = struct{}{}
		kept = append(kept, line)
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}

	details := strings.Join(kept, "\n")
	runes := []rune(details)
	if len(runes) > maxCommandErrorRunes {
		details = "…" + string(runes[len(runes)-maxCommandErrorRunes:])
		truncated = false
	}
	if truncated {
		details = "…\n" + details
	}
	return details
}
