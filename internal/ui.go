package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

const (
	ansiReset = "\033[0m"
	ansiRed   = "\033[31m"
	ansiGreen = "\033[32m"
	ansiCyan  = "\033[36m"
)

func Red(value string) string   { return paint(ansiRed, value) }
func Green(value string) string { return paint(ansiGreen, value) }
func Cyan(value string) string  { return paint(ansiCyan, value) }

func paint(color, value string) string {
	if colorDisabled.Load() || !term.IsTerminal(int(os.Stdout.Fd())) {
		return value
	}
	return color + value + ansiReset
}

type Spinner struct {
	message         string
	completed       string
	prefix          string
	isTTY           bool
	showElapsed     bool
	printCompletion bool
	started         time.Time
	stop            chan struct{}
	once            sync.Once
	done            sync.WaitGroup
	progressWriter  io.Writer
	resultWriter    io.Writer
}

func NewOperation(message, completed string) *Spinner {
	return newOperation(message, completed, true, term.IsTerminal(int(os.Stderr.Fd())), os.Stderr, os.Stdout)
}

func newOperation(message, completed string, animate, isTTY bool, progressWriter, resultWriter io.Writer) *Spinner {
	progress := newProgressStyle(message, animate, isTTY, true, progressWriter)
	progress.completed = completed
	progress.resultWriter = resultWriter
	return progress
}

func NewActivity(message string) *Spinner {
	progress := newProgressStyle(message, true, term.IsTerminal(int(os.Stderr.Fd())), false, os.Stderr)
	progress.printCompletion = false
	return progress
}

func NewStep(message string) *Spinner {
	step := newProgressStyle(message, true, term.IsTerminal(int(os.Stderr.Fd())), false, os.Stderr)
	step.prefix = "  "
	return step
}

func newProgressStyle(message string, animate, isTTY, showElapsed bool, writer io.Writer) *Spinner {
	return &Spinner{
		message:         message,
		completed:       message,
		isTTY:           animate && isTTY,
		showElapsed:     showElapsed,
		printCompletion: true,
		started:         time.Now(),
		stop:            make(chan struct{}),
		progressWriter:  writer,
		resultWriter:    writer,
	}
}

func (s *Spinner) Start() {
	s.started = time.Now()
	if !s.isTTY {
		return
	}
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				fmt.Fprintf(s.progressWriter, "\r%s%s %s", s.prefix, Cyan(frames[index]), s.message)
				index = (index + 1) % len(frames)
			}
		}
	}()
}

func (s *Spinner) Stop(success bool) {
	s.once.Do(func() {
		if s.isTTY {
			close(s.stop)
			s.done.Wait()
			fmt.Fprint(s.progressWriter, "\r\033[2K")
		}
		if !s.printCompletion || !success {
			return
		}
		message := fmt.Sprintf("%s%s %s", s.prefix, Green("✓"), s.completed)
		if s.showElapsed {
			message += " in " + FormatDuration(time.Since(s.started))
		}
		fmt.Fprintln(s.resultWriter, message)
	})
}

func FormatDuration(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	minutes := int(duration / time.Minute)
	seconds := duration.Seconds() - float64(minutes*60)
	return fmt.Sprintf("%dm %.1fs", minutes, seconds)
}

var colorDisabled atomic.Bool

func SetColorEnabled(enabled bool) { colorDisabled.Store(!enabled) }

func SelectProject(projects []string, input io.Reader, output io.Writer) (string, error) {
	return selectValue("Project", projects, input, output)
}

func selectValue(prompt string, values []string, input io.Reader, output io.Writer) (string, error) {
	return selectValueDefault(prompt, values, "", input, output)
}

func selectValueDefault(prompt string, values []string, defaultValue string, input io.Reader, output io.Writer) (string, error) {
	if inputFile, inputOK := input.(*os.File); inputOK {
		if outputFile, outputOK := output.(*os.File); outputOK && term.IsTerminal(int(inputFile.Fd())) && term.IsTerminal(int(outputFile.Fd())) {
			return selectValueRaw("  "+prompt, values, defaultValue, inputFile, outputFile)
		}
	}

	printChoices(output, values)
	choices := values
	for {
		hint := fmt.Sprintf("[1-%d]", len(choices))
		if defaultValue != "" {
			hint += " (Enter keeps current)"
		}
		fmt.Fprintf(output, "  %s number or search %s: ", prompt, hint)
		line, err := readSelectionLine(input)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", fmt.Errorf("read %s selection: %w", strings.ToLower(prompt), err)
			}
			return "", fmt.Errorf("no %s selected", strings.ToLower(prompt))
		}
		query := strings.TrimSpace(line)
		if query == "" && defaultValue != "" {
			return defaultValue, nil
		}
		choice, err := strconv.Atoi(query)
		if err == nil && choice >= 1 && choice <= len(choices) {
			return choices[choice-1], nil
		}

		matches := filterChoices(values, query)
		switch len(matches) {
		case 0:
			fmt.Fprintln(output, "  No matches. Enter a number or search text.")
			choices = values
		case 1:
			return matches[0], nil
		default:
			choices = matches
			fmt.Fprintln(output, "  Matches:")
			printChoices(output, choices)
		}
	}
}

func readSelectionLine(input io.Reader) (string, error) {
	var line []byte
	buffer := make([]byte, 1)
	for {
		count, err := input.Read(buffer)
		if count > 0 {
			if buffer[0] == '\n' {
				return string(line), nil
			}
			if buffer[0] != '\r' {
				line = append(line, buffer[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return string(line), nil
			}
			return "", err
		}
	}
}

func selectValueRaw(prompt string, values []string, defaultValue string, input, output *os.File) (string, error) {
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(input.Fd()), state)

	width, _, err := term.GetSize(int(output.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	return selectValueInteractive(prompt, values, defaultValue, input, output, width)
}

func selectValueInteractive(prompt string, values []string, defaultValue string, input io.Reader, output io.Writer, width int) (string, error) {
	defer finishRawLine(output)
	choices := values
	selected := 0
	for index, value := range choices {
		if value == defaultValue {
			selected = index
		}
	}
	query := ""
	escape := byte(0)
	previousLines := 0
	render := func() {
		if previousLines > 1 {
			fmt.Fprintf(output, "\033[%dA", previousLines-1)
		}
		fmt.Fprint(output, "\r\033[J")
		lines := []string{fmt.Sprintf("%s (↑/↓, number or search, Enter): %s", prompt, query)}
		if len(choices) == 0 {
			lines = append(lines, "  No matches")
		} else {
			// Keep the selected item visible without filling the terminal.
			start := max(0, selected-7)
			end := min(len(choices), start+8)
			for index := start; index < end; index++ {
				marker := " "
				if index == selected {
					marker = ">"
				}
				lines = append(lines, fmt.Sprintf("  %s %d. %s", marker, slices.Index(values, choices[index])+1, choices[index]))
			}
			if len(choices) > 8 {
				lines = append(lines, fmt.Sprintf("  %d / %d", selected+1, len(choices)))
			}
		}
		for index, line := range lines {
			if index > 0 {
				fmt.Fprint(output, "\r\n")
			}
			runes := []rune(line)
			if width > 1 && len(runes) >= width {
				line = string(runes[:width-2]) + "…"
			}
			fmt.Fprint(output, line)
		}
		previousLines = len(lines)
	}
	render()
	buffer := make([]byte, 1)
	for {
		if _, err := io.ReadFull(input, buffer); err != nil {
			return "", err
		}
		// Parse escape sequences one byte at a time. A standalone Escape must
		// not swallow Ctrl-C or wait for two more bytes before handling it.
		if buffer[0] == 3 {
			return "", context.Canceled
		}
		if escape == 27 {
			escape = 0
			if buffer[0] == '[' || buffer[0] == 'O' {
				escape = '['
				continue
			}
		} else if escape == '[' {
			if buffer[0] >= 0x40 && buffer[0] <= 0x7e {
				escape = 0
				if len(choices) > 0 {
					switch buffer[0] {
					case 'A':
						selected = (selected - 1 + len(choices)) % len(choices)
					case 'B':
						selected = (selected + 1) % len(choices)
					}
				}
				render()
			}
			continue
		}
		switch buffer[0] {
		case '\r', '\n':
			if len(choices) > 0 {
				return choices[selected], nil
			}
		case 127, 8:
			if len(query) > 0 {
				query = query[:len(query)-1]
			}
		case 27:
			escape = 27
			continue
		default:
			if buffer[0] >= 32 && buffer[0] < 127 {
				query += string(buffer[0])
			}
		}
		choices = values
		selected = 0
		if numeric, err := strconv.Atoi(query); err == nil && numeric >= 1 && numeric <= len(values) {
			// Numeric input refers to the unfiltered, numbered list.
			selected = numeric - 1
		} else if query != "" {
			choices = filterChoices(values, query)
		}
		render()
	}
}

func finishRawLine(output io.Writer) {
	fmt.Fprint(output, "\r\n")
}

func printChoices(output io.Writer, values []string) {
	width := len(strconv.Itoa(len(values)))
	for index, value := range values {
		fmt.Fprintf(output, "  %*d. %s\n", width, index+1, value)
	}
}

func filterChoices(values []string, query string) []string {
	if query == "" {
		return nil
	}
	normalized := strings.ToLower(query)
	var matches []string
	for _, value := range values {
		if strings.EqualFold(value, query) {
			return []string{value}
		}
		if strings.Contains(strings.ToLower(value), normalized) {
			matches = append(matches, value)
		}
	}
	return matches
}
