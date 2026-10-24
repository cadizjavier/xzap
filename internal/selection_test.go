package internal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestInteractiveSelection(t *testing.T) {
	values := []string{"iPhone A", "iPhone B", "iPad"}
	for _, test := range []struct {
		name, input, current, want, marker string
	}{
		{name: "down", input: "\x1b[B\r", want: "iPhone B", marker: "> 2. iPhone B"},
		{name: "up wraps", input: "\x1b[A\r", want: "iPad", marker: "> 3. iPad"},
		{name: "current device", input: "\r", current: "iPhone B", want: "iPhone B", marker: "> 2. iPhone B"},
		{name: "filter", input: "B\r", want: "iPhone B", marker: "> 2. iPhone B"},
		{name: "navigate matches", input: "phone\x1b[B\r", want: "iPhone B", marker: "> 2. iPhone B"},
		{name: "number", input: "2\r", want: "iPhone B", marker: "> 2. iPhone B"},
		{name: "navigate after number", input: "2\x1b[B\r", want: "iPad", marker: "> 3. iPad"},
		{name: "clear unmatched search", input: "zz\r\x7f\x7f\r", want: "iPhone A", marker: "> 1. iPhone A"},
		{name: "escape then enter", input: "\x1b\r", want: "iPhone A", marker: "> 1. iPhone A"},
		{name: "application cursor", input: "\x1bOB\r", want: "iPhone B", marker: "> 2. iPhone B"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := selectValueInteractive("Device", values, test.current, strings.NewReader(test.input), &output, 80)
			if err != nil || got != test.want {
				t.Fatalf("selection = %q, %v; want %q", got, err, test.want)
			}
			text := output.String()
			last := text[strings.LastIndex(text, "\r\033[J"):]
			if !strings.Contains(last, test.marker) {
				t.Errorf("final display does not mark the selected device: %q", last)
			}
			if test.name == "filter" && (strings.Contains(last, "iPhone A") || strings.Contains(last, "iPad")) {
				t.Errorf("filtered display contains non-matching devices: %q", last)
			}
			if test.name == "clear unmatched search" && !strings.Contains(text, "No matches") {
				t.Error("unmatched search did not display feedback")
			}
		})
	}
}

func TestInteractiveSelectionCancelsIncompleteEscape(t *testing.T) {
	for _, prefix := range []string{"", "\x1b", "\x1b[", "\x1b[1;", "\x1bO"} {
		t.Run(prefix, func(t *testing.T) {
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			done := make(chan error, 1)
			go func() {
				_, err := selectValueInteractive("Device", []string{"iPhone"}, "", input, io.Discard, 80)
				done <- err
			}()
			if _, err := writer.Write([]byte(prefix + "\x03")); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("selection error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Ctrl-C did not cancel while input remained open")
			}
		})
	}
}

func TestInteractiveSelectionShowsChoicesBeyondFirstPage(t *testing.T) {
	values := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	var output bytes.Buffer
	got, err := selectValueInteractive("Device", values, "", strings.NewReader("\x1b[A\r"), &output, 30)
	if err != nil || got != "ten" {
		t.Fatalf("selection = %q, %v; want ten", got, err)
	}
	text := output.String()
	last := text[strings.LastIndex(text, "\r\033[J"):]
	if !strings.Contains(last, "> 10. ten") || !strings.Contains(last, "10 / 10") {
		t.Errorf("last choice is not visible: %q", last)
	}
	for _, line := range strings.Split(strings.TrimPrefix(last, "\r\033[J"), "\r\n") {
		if len([]rune(line)) >= 30 {
			t.Errorf("display line can wrap: %q", line)
		}
	}
}
