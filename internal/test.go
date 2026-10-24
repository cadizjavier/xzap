package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrTestsFailed = errors.New("tests failed")
	ErrNoTests     = errors.New("no tests ran; check the scheme's Test action")
)

type TestOptions struct {
	BuildOptions
	Identifiers []string
}

type TestFailure struct {
	Test    string
	Target  string
	Message string
}

type TestSummary struct {
	Total            int
	Passed           int
	Failed           int
	Skipped          int
	ExpectedFailures int
	Duration         float64
	Failures         []TestFailure
}

type xcodeTestSummary struct {
	StartTime        float64            `json:"startTime"`
	FinishTime       float64            `json:"finishTime"`
	Total            int                `json:"totalTestCount"`
	Passed           int                `json:"passedTests"`
	Failed           int                `json:"failedTests"`
	Skipped          int                `json:"skippedTests"`
	ExpectedFailures int                `json:"expectedFailures"`
	Failures         []xcodeTestFailure `json:"testFailures"`
}

type xcodeTestFailure struct {
	TestName             string `json:"testName"`
	TargetName           string `json:"targetName"`
	FailureText          string `json:"failureText"`
	TestIdentifierString string `json:"testIdentifierString"`
}

var executeTestCommand = executeXcodebuildTests
var readTestResults = readXcodeTestResults

func RunTests(ctx context.Context, opts TestOptions) (TestSummary, error) {
	tempDir, err := os.MkdirTemp("", "xzap-tests-")
	if err != nil {
		return TestSummary{}, fmt.Errorf("create test result directory: %w", err)
	}
	removeResults := true
	defer func() {
		if removeResults {
			_ = os.RemoveAll(tempDir)
		}
	}()
	resultPath := filepath.Join(tempDir, "Tests.xcresult")
	preserveFailure := func(cause error) error {
		if _, statErr := os.Stat(resultPath); statErr == nil {
			removeResults = false
			return fmt.Errorf("%w; test results preserved at %s", cause, resultPath)
		}
		return cause
	}

	progress := NewActivity(fmt.Sprintf("Testing %s → %s", opts.Scheme, TargetDescription(opts.Target)))
	progress.Start()
	diagnostics := newDiagnostics(filepath.Dir(opts.Project))
	runErr := executeTestCommand(ctx, testArguments(opts, resultPath), diagnostics)
	summary, resultErr := readTestResults(ctx, resultPath)

	switch {
	case resultErr == nil && summary.Failed > 0:
		progress.Stop(false)
		return summary, preserveFailure(ErrTestsFailed)
	case runErr != nil:
		progress.Stop(false)
		printTestCommandFailure(diagnostics)
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}
		return summary, preserveFailure(fmt.Errorf("%w: %v", ErrTestsFailed, runErr))
	case resultErr != nil:
		progress.Stop(false)
		printTestCommandFailure(diagnostics)
		return summary, preserveFailure(fmt.Errorf("read test results: %w", resultErr))
	case summary.Total == 0:
		progress.Stop(false)
		return summary, preserveFailure(ErrNoTests)
	case runErr == nil && resultErr == nil:
		progress.Stop(true)
		if !opts.OnlyErrors {
			printDiagnostics(diagnostics.byType("warning"))
		}
		return summary, nil
	}
	panic("unreachable")
}

func testArguments(opts TestOptions, resultPath string) []string {
	args := append(buildArguments(opts.BuildOptions),
		"-resultBundlePath", resultPath,
	)
	for _, identifier := range opts.Identifiers {
		args = append(args, "-only-testing:"+identifier)
	}
	return append(args, "test")
}

func executeXcodebuildTests(ctx context.Context, args []string, diagnostics *diagnosticCollector) error {
	cmd := exec.CommandContext(ctx, "xcodebuild", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tests: %w", err)
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go streamBuildOutput(stdout, diagnostics, &waitGroup)
	go streamBuildOutput(stderr, diagnostics, &waitGroup)
	waitGroup.Wait()

	waitErr := cmd.Wait()
	if err := diagnostics.outputError(); err != nil {
		return fmt.Errorf("read test output: %w", err)
	}
	return waitErr
}

func readXcodeTestResults(ctx context.Context, resultPath string) (TestSummary, error) {
	output, err := commandOutput(
		ctx,
		"xcrun", "xcresulttool", "get", "test-results", "summary",
		"--path", resultPath,
		"--compact",
	)
	if err != nil {
		return TestSummary{}, err
	}
	return parseXcodeTestResults(output)
}

func parseXcodeTestResults(output []byte) (TestSummary, error) {
	var result xcodeTestSummary
	if err := json.Unmarshal(output, &result); err != nil {
		return TestSummary{}, fmt.Errorf("decode test results: %w", err)
	}

	summary := TestSummary{
		Total:            result.Total,
		Passed:           result.Passed,
		Failed:           result.Failed,
		Skipped:          result.Skipped,
		ExpectedFailures: result.ExpectedFailures,
		Duration:         result.FinishTime - result.StartTime,
		Failures:         make([]TestFailure, 0, len(result.Failures)),
	}
	for _, failure := range result.Failures {
		test := failure.TestIdentifierString
		if test == "" {
			test = failure.TestName
		}
		summary.Failures = append(summary.Failures, TestFailure{
			Test:    test,
			Target:  failure.TargetName,
			Message: failure.FailureText,
		})
	}
	return summary, nil
}

func printTestCommandFailure(diagnostics *diagnosticCollector) {
	buildErrors := diagnostics.byType("error")
	printDiagnostics(buildErrors)
	if len(buildErrors) == 0 {
		printBuildFallback(diagnostics.fallback())
	}
}

func PrintTestSummary(summary TestSummary, target Target) {
	icon := Green("✓")
	if summary.Failed > 0 {
		icon = Red("✗")
	}
	for _, failure := range summary.Failures {
		fmt.Fprintln(os.Stderr, failure.Test)
		if failure.Target != "" {
			fmt.Fprintf(os.Stderr, "  target: %s\n", failure.Target)
		}
		for _, line := range strings.Split(strings.TrimSpace(failure.Message), "\n") {
			fmt.Fprintf(os.Stderr, "  %s\n", line)
		}
	}
	fmt.Printf(
		"%s %s in %s on %s\n",
		icon,
		testCountSummary(summary),
		formatTestDuration(summary.Duration),
		TargetDescription(target),
	)
}

func testCountSummary(summary TestSummary) string {
	parts := []string{
		fmt.Sprintf("%d passed", summary.Passed),
	}
	if summary.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", summary.Failed))
	}
	if summary.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", summary.Skipped))
	}
	if summary.ExpectedFailures > 0 {
		parts = append(parts, fmt.Sprintf("%d expected failures", summary.ExpectedFailures))
	}
	return strings.Join(parts, ", ")
}

func formatTestDuration(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	return strconv.FormatFloat(seconds, 'f', 1, 64) + "s"
}
