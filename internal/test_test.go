package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTestArgumentsUsesConfiguredDestinationAndFilters(t *testing.T) {
	got := testArguments(TestOptions{
		BuildOptions: testBuildOptions(),
		Identifiers: []string{
			"AppTests/LoginTests/testExpiredSession",
			"AppUITests/OnboardingTests",
		},
	}, "/tmp/Tests.xcresult")
	want := []string{
		"-project",
		"/tmp/MyApp.xcodeproj",
		"-scheme",
		"MyApp",
		"-destination",
		"platform=iOS Simulator,id=SIMULATOR-ID",
		"-skipPackagePluginValidation",
		"-resultBundlePath",
		"/tmp/Tests.xcresult",
		"-only-testing:AppTests/LoginTests/testExpiredSession",
		"-only-testing:AppUITests/OnboardingTests",
		"test",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("testArguments() = %#v, want %#v", got, want)
	}
}

func TestParseXcodeTestResults(t *testing.T) {
	got, err := parseXcodeTestResults([]byte(`{
		"startTime": 100.0,
		"finishTime": 112.5,
		"totalTestCount": 5,
		"passedTests": 2,
		"failedTests": 1,
		"skippedTests": 1,
		"expectedFailures": 1,
		"testFailures": [{
			"testName": "testExpiredSession()",
			"targetName": "AppTests",
			"failureText": "Expected login screen",
			"testIdentifierString": "LoginTests/testExpiredSession()"
		}]
	}`))
	if err != nil {
		t.Fatalf("parseXcodeTestResults() error = %v", err)
	}
	want := TestSummary{
		Total:            5,
		Passed:           2,
		Failed:           1,
		Skipped:          1,
		ExpectedFailures: 1,
		Duration:         12.5,
		Failures: []TestFailure{{
			Test:    "LoginTests/testExpiredSession()",
			Target:  "AppTests",
			Message: "Expected login screen",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseXcodeTestResults() = %#v, want %#v", got, want)
	}
}

func TestParseXcodeTestResultsFallsBackToTestName(t *testing.T) {
	got, err := parseXcodeTestResults([]byte(`{
		"totalTestCount": 1,
		"failedTests": 1,
		"testFailures": [{
			"testName": "testFailure()",
			"failureText": "failed"
		}]
	}`))
	if err != nil {
		t.Fatalf("parseXcodeTestResults() error = %v", err)
	}
	if got.Failures[0].Test != "testFailure()" {
		t.Errorf("failure test = %q, want fallback test name", got.Failures[0].Test)
	}
}

func TestRunTestsReturnsParsedTestFailure(t *testing.T) {
	stubTestExecution(t,
		func(_ context.Context, _ []string, _ *diagnosticCollector) error {
			return errors.New("exit status 65")
		},
		func(_ context.Context, _ string) (TestSummary, error) {
			return TestSummary{Total: 2, Passed: 1, Failed: 1}, nil
		},
	)

	summary, err := RunTests(context.Background(), TestOptions{BuildOptions: testBuildOptions()})
	if !errors.Is(err, ErrTestsFailed) {
		t.Fatalf("RunTests() error = %v, want ErrTestsFailed", err)
	}
	if summary.Failed != 1 {
		t.Errorf("RunTests() summary = %#v", summary)
	}
}

func TestRunTestsPreservesResultBundleOnFailure(t *testing.T) {
	stubTestExecution(t,
		func(_ context.Context, args []string, _ *diagnosticCollector) error {
			for index, arg := range args {
				if arg == "-resultBundlePath" && index+1 < len(args) {
					return os.Mkdir(args[index+1], 0o755)
				}
			}
			return errors.New("result bundle path missing")
		},
		func(_ context.Context, _ string) (TestSummary, error) {
			return TestSummary{Total: 1, Failed: 1}, nil
		},
	)

	_, err := RunTests(context.Background(), TestOptions{BuildOptions: testBuildOptions()})
	if !errors.Is(err, ErrTestsFailed) {
		t.Fatalf("RunTests() error = %v, want ErrTestsFailed", err)
	}
	const marker = "test results preserved at "
	index := strings.LastIndex(err.Error(), marker)
	if index < 0 {
		t.Fatalf("RunTests() error = %q, want preserved result path", err)
	}
	resultPath := strings.TrimSpace(err.Error()[index+len(marker):])
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(resultPath)) })
	if info, statErr := os.Stat(resultPath); statErr != nil || !info.IsDir() {
		t.Fatalf("preserved result bundle %q: %v", resultPath, statErr)
	}
}

func TestRunTestsRejectsEmptyTestRun(t *testing.T) {
	stubTestExecution(t,
		func(_ context.Context, _ []string, _ *diagnosticCollector) error { return nil },
		func(_ context.Context, _ string) (TestSummary, error) { return TestSummary{}, nil },
	)

	_, err := RunTests(context.Background(), TestOptions{BuildOptions: testBuildOptions()})
	if !errors.Is(err, ErrNoTests) {
		t.Errorf("RunTests() error = %v, want ErrNoTests", err)
	}
}

func TestRunTestsDoesNotMaskCommandFailureAsEmptyTestRun(t *testing.T) {
	commandFailure := errors.New("exit status 65")
	stubTestExecution(t,
		func(_ context.Context, _ []string, diagnostics *diagnosticCollector) error {
			diagnostics.add("RecountTests/Test.swift:7: error: cannot find value")
			return commandFailure
		},
		func(_ context.Context, _ string) (TestSummary, error) {
			return TestSummary{}, nil
		},
	)

	_, err := RunTests(context.Background(), TestOptions{BuildOptions: testBuildOptions()})
	if !errors.Is(err, ErrTestsFailed) || errors.Is(err, ErrNoTests) {
		t.Errorf("RunTests() error = %v, want command failure instead of ErrNoTests", err)
	}
}

func TestRunTestsReportsResultReadFailure(t *testing.T) {
	resultFailure := errors.New("result unavailable")
	stubTestExecution(t,
		func(_ context.Context, _ []string, _ *diagnosticCollector) error { return nil },
		func(_ context.Context, _ string) (TestSummary, error) {
			return TestSummary{}, resultFailure
		},
	)

	_, err := RunTests(context.Background(), TestOptions{BuildOptions: testBuildOptions()})
	if !errors.Is(err, resultFailure) || !strings.Contains(err.Error(), "read test results") {
		t.Errorf("RunTests() error = %v, want wrapped result failure", err)
	}
}

func TestTestCountSummaryOmitsZeroCategories(t *testing.T) {
	got := testCountSummary(TestSummary{
		Passed:           8,
		Skipped:          2,
		ExpectedFailures: 1,
	})
	if want := "8 passed, 2 skipped, 1 expected failures"; got != want {
		t.Errorf("testCountSummary() = %q, want %q", got, want)
	}
}

func stubTestExecution(
	t *testing.T,
	execute func(context.Context, []string, *diagnosticCollector) error,
	read func(context.Context, string) (TestSummary, error),
) {
	t.Helper()
	originalExecute := executeTestCommand
	originalRead := readTestResults
	t.Cleanup(func() {
		executeTestCommand = originalExecute
		readTestResults = originalRead
	})
	executeTestCommand = execute
	readTestResults = read
}
