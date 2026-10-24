package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestFindProjectsPrefersWorkspacesAndSorts(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"App.xcodeproj",
		"Zeta.xcworkspace",
		"Alpha.xcworkspace",
	} {
		if err := os.Mkdir(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := FindProjects(root)
	if err != nil {
		t.Fatalf("FindProjects() error = %v", err)
	}
	want := []string{
		filepath.Join(root, "Alpha.xcworkspace"),
		filepath.Join(root, "Zeta.xcworkspace"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindProjects() = %v, want %v", got, want)
	}
}

func TestBuildArgumentsUsePhysicalDeviceDestination(t *testing.T) {
	args := buildArguments(BuildOptions{
		Project: "/tmp/MyApp.xcodeproj",
		Scheme:  "MyApp",
		Target:  Target{ID: "DEVICE-ID", Name: "Javier's iPhone"},
		Device:  true,
	})
	if got, want := argumentAfter(args, "-destination"), "platform=iOS,id=DEVICE-ID"; got != want {
		t.Errorf("destination = %q, want %q", got, want)
	}
}

func TestSelectProject(t *testing.T) {
	var output bytes.Buffer
	got, err := SelectProject(
		[]string{"MyApp Development", "MyApp Staging"},
		strings.NewReader("wrong\n3\n2\n"),
		&output,
	)
	if err != nil {
		t.Fatalf("SelectProject() error = %v", err)
	}
	if got != "MyApp Staging" {
		t.Errorf("SelectProject() = %q, want MyApp Staging", got)
	}
	if !strings.Contains(output.String(), "No matches. Enter a number or search text.") {
		t.Errorf("selector output did not explain invalid input:\n%s", output.String())
	}
}

func TestSelectProjectFiltersByText(t *testing.T) {
	var output bytes.Buffer
	got, err := SelectProject(
		[]string{"MyApp", "MyApp Staging", "MyApp Staging Tests"},
		strings.NewReader("staging\n2\n"),
		&output,
	)
	if err != nil {
		t.Fatalf("SelectProject() error = %v", err)
	}
	if got != "MyApp Staging Tests" {
		t.Errorf("SelectProject() = %q, want MyApp Staging Tests", got)
	}
	if !strings.Contains(output.String(), "Matches:") {
		t.Errorf("selector output did not show filtered matches:\n%s", output.String())
	}
}

func TestSelectProjectWithoutInput(t *testing.T) {
	var output bytes.Buffer
	_, err := SelectProject([]string{"MyApp", "MyApp Staging"}, strings.NewReader(""), &output)
	if err == nil {
		t.Fatal("SelectProject() error = nil, want an error")
	}
	if got, want := err.Error(), "no project selected"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestWriteDiagnosticsUsesCompilerFormat(t *testing.T) {
	var output bytes.Buffer
	writeDiagnostics(&output, []Diagnostic{
		{File: "Sources/Login.swift", Line: "42", Column: "17", Type: "error", Message: "Cannot find session"},
		{Type: "warning", Message: "Run script has no outputs"},
	})
	want := "Sources/Login.swift:42:17: error: Cannot find session\nwarning: Run script has no outputs\n"
	if got := output.String(); got != want {
		t.Errorf("diagnostics = %q, want %q", got, want)
	}
}

func TestPrintChoicesAlignsDoubleDigitNumbers(t *testing.T) {
	var output bytes.Buffer
	printChoices(&output, []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"})
	lines := strings.Split(output.String(), "\n")
	if !strings.HasPrefix(lines[0], "   1.") || !strings.HasPrefix(lines[9], "  10.") {
		t.Errorf("choices are not aligned:\n%s", output.String())
	}
}

func TestFinishRawLineReturnsToFirstColumn(t *testing.T) {
	var output bytes.Buffer
	finishRawLine(&output)
	if got := output.String(); got != "\r\n" {
		t.Errorf("finishRawLine() = %q, want CRLF", got)
	}
}

func TestAppPathSelectsApplicationProduct(t *testing.T) {
	buildDir := t.TempDir()
	appPath := filepath.Join(buildDir, "MyApp.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appPath, "Info.plist"), []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubBuildSettings(t, []targetBuildSettings{
		{
			BuildSettings: map[string]string{
				"PRODUCT_TYPE":     "com.apple.product-type.bundle.unit-test",
				"TARGET_BUILD_DIR": buildDir,
				"WRAPPER_NAME":     "MyAppTests.xctest",
			},
		},
		{
			BuildSettings: map[string]string{
				"PRODUCT_TYPE":     "com.apple.product-type.application",
				"TARGET_BUILD_DIR": buildDir,
				"WRAPPER_NAME":     "MyApp.app",
			},
		},
		{
			BuildSettings: map[string]string{
				"PRODUCT_TYPE":     "com.apple.product-type.application",
				"TARGET_BUILD_DIR": buildDir,
				"WRAPPER_NAME":     "MyApp.app",
			},
		},
	})

	got, err := AppPath(context.Background(), testBuildOptions())
	if err != nil {
		t.Fatalf("AppPath() error = %v", err)
	}
	if got != appPath {
		t.Errorf("AppPath() = %q, want %q", got, appPath)
	}
}

func TestAppPathRejectsMultipleApplications(t *testing.T) {
	buildDir := t.TempDir()
	stubBuildSettings(t, []targetBuildSettings{
		{
			BuildSettings: map[string]string{
				"PRODUCT_TYPE":     "com.apple.product-type.application",
				"TARGET_BUILD_DIR": buildDir,
				"WRAPPER_NAME":     "First.app",
			},
		},
		{
			BuildSettings: map[string]string{
				"PRODUCT_TYPE":     "com.apple.product-type.application",
				"TARGET_BUILD_DIR": buildDir,
				"WRAPPER_NAME":     "Second.app",
			},
		},
	})

	_, err := AppPath(context.Background(), testBuildOptions())
	if err == nil {
		t.Fatal("AppPath() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `scheme produces multiple application products`) {
		t.Errorf("error = %q, want ambiguity context", err)
	}
}

func TestAppPathRejectsMissingProduct(t *testing.T) {
	buildDir := t.TempDir()
	stubBuildSettings(t, []targetBuildSettings{{
		BuildSettings: map[string]string{
			"PRODUCT_TYPE":     "com.apple.product-type.application",
			"TARGET_BUILD_DIR": buildDir,
			"WRAPPER_NAME":     "MyApp.app",
		},
	}})

	_, err := AppPath(context.Background(), testBuildOptions())
	if err == nil {
		t.Fatal("AppPath() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "app product does not exist") {
		t.Errorf("error = %q, want missing product context", err)
	}
}

func TestAppPathRejectsMissingInfoPlist(t *testing.T) {
	buildDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(buildDir, "MyApp.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubBuildSettings(t, []targetBuildSettings{{
		BuildSettings: map[string]string{
			"PRODUCT_TYPE":     "com.apple.product-type.application",
			"TARGET_BUILD_DIR": buildDir,
			"WRAPPER_NAME":     "MyApp.app",
		},
	}})

	_, err := AppPath(context.Background(), testBuildOptions())
	if err == nil {
		t.Fatal("AppPath() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "app product is missing Info.plist") {
		t.Errorf("error = %q, want missing Info.plist context", err)
	}
}

func TestParseDiagnosticFormats(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	tests := []struct {
		name string
		line string
		want Diagnostic
		ok   bool
	}{
		{
			name: "file with line and column",
			line: filepath.Join(root, "Sources", "Feature", "Screen.swift") + ":12:34: error: cannot find value",
			want: Diagnostic{
				File:    filepath.Join("Sources", "Feature", "Screen.swift"),
				Line:    "12",
				Column:  "34",
				Type:    "error",
				Message: "cannot find value",
			},
			ok: true,
		},
		{
			name: "file without column",
			line: filepath.Join(root, "Sources", "App.swift") + ":8: warning: unused value",
			want: Diagnostic{
				File:    filepath.Join("Sources", "App.swift"),
				Line:    "8",
				Type:    "warning",
				Message: "unused value",
			},
			ok: true,
		},
		{
			name: "linker error",
			line: "ld: error: undefined symbol: _Example",
			want: Diagnostic{Type: "error", Message: "undefined symbol: _Example"},
			ok:   true,
		},
		{
			name: "xcodebuild error",
			line: "xcodebuild: error: The workspace does not contain a scheme.",
			want: Diagnostic{Type: "error", Message: "The workspace does not contain a scheme."},
			ok:   true,
		},
		{
			name: "unrelated output",
			line: "CompileSwift normal arm64",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseDiagnostic(test.line, root)
			if ok != test.ok {
				t.Fatalf("parseDiagnostic() ok = %v, want %v", ok, test.ok)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseDiagnostic() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDiagnosticCollectorDistinguishesMatchingBasenames(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	collector := newDiagnostics(root)
	collector.add(filepath.Join(root, "Sources", "One", "View.swift") + ":4:2: error: broken")
	collector.add(filepath.Join(root, "Sources", "Two", "View.swift") + ":4:2: error: broken")
	collector.add(filepath.Join(root, "Sources", "One", "View.swift") + ":4:2: error: broken")

	got := collector.byType("error")
	if len(got) != 2 {
		t.Fatalf("collector returned %d errors, want 2: %#v", len(got), got)
	}
	if got[0].File == got[1].File {
		t.Errorf("collector collapsed distinct paths: %#v", got)
	}
}

func TestStreamBuildOutputRetainsReadError(t *testing.T) {
	collector := newDiagnostics("/repo")
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	streamBuildOutput(failingReader{}, collector, &waitGroup)
	waitGroup.Wait()

	if err := collector.outputError(); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Errorf("outputError() = %v, want read failure", err)
	}
	if got := collector.fallback(); !strings.Contains(got, "read build output: read failed") {
		t.Errorf("fallback() = %q, want read failure", got)
	}
}

type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read failed")
}

func stubBuildSettings(t *testing.T, settings []targetBuildSettings) {
	t.Helper()
	original := runXcodebuild
	t.Cleanup(func() { runXcodebuild = original })
	runXcodebuild = func(_ context.Context, _ ...string) ([]byte, error) {
		return json.Marshal(settings)
	}
}

func testBuildOptions() BuildOptions {
	return BuildOptions{
		Project: "/tmp/MyApp.xcodeproj",
		Scheme:  "MyApp",
		Target:  Target{ID: "SIMULATOR-ID", Name: "iPhone"},
	}
}

func hasArgument(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func argumentAfter(args []string, target string) string {
	for index, arg := range args {
		if arg == target && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}
