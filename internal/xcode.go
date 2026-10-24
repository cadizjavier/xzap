package internal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNoProjectFound = errors.New("no Xcode project or workspace found")
	ErrBuildFailed    = errors.New("build failed")
	ErrCleanFailed    = errors.New("clean failed")
)

const maxSearchDepth = 3

func FindProjects(dir string) ([]string, error) {
	if dir == "" {
		dir = "."
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	var workspaces, projects []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := len(strings.Split(rel, string(filepath.Separator))) - 1
		if depth > maxSearchDepth {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(d.Name(), ".xcworkspace"):
			workspaces = append(workspaces, path)
			return filepath.SkipDir
		case strings.HasSuffix(d.Name(), ".xcodeproj"):
			projects = append(projects, path)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(workspaces)
	sort.Strings(projects)
	switch {
	case len(workspaces) > 0:
		return workspaces, nil
	case len(projects) > 0:
		return projects, nil
	default:
		return nil, ErrNoProjectFound
	}
}

func IsWorkspace(project string) bool {
	return strings.HasSuffix(project, ".xcworkspace")
}

type targetBuildSettings struct {
	BuildSettings map[string]string `json:"buildSettings"`
}

var runXcodebuild = func(ctx context.Context, args ...string) ([]byte, error) {
	return commandOutput(ctx, "xcodebuild", args...)
}

func XcodeVersion(ctx context.Context) (string, error) {
	output, err := commandOutput(ctx, "xcodebuild", "-version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

type BuildOptions struct {
	Project    string
	Scheme     string
	Target     Target
	Device     bool
	OnlyErrors bool
}

type Diagnostic struct {
	File    string
	Line    string
	Column  string
	Type    string
	Message string
}

func Build(ctx context.Context, opts BuildOptions) error {
	return runBuildAction(ctx, opts, "build", "Building", "Built", ErrBuildFailed)
}

func Clean(ctx context.Context, opts BuildOptions) error {
	return runBuildAction(ctx, opts, "clean", "Cleaning", "Cleaned", ErrCleanFailed)
}

func runBuildAction(ctx context.Context, opts BuildOptions, action, progress, completed string, failure error) error {
	description := fmt.Sprintf("%s → %s", opts.Scheme, TargetDescription(opts.Target))
	spinner := NewOperation(progress+" "+description, completed+" "+description)
	spinner.Start()
	cmd := exec.CommandContext(ctx, "xcodebuild", append(buildArguments(opts), action)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		spinner.Stop(false)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		spinner.Stop(false)
		return err
	}
	if err := cmd.Start(); err != nil {
		spinner.Stop(false)
		return fmt.Errorf("start %s: %w", action, err)
	}

	diagnostics := newDiagnostics(filepath.Dir(opts.Project))
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go streamBuildOutput(stdout, diagnostics, &waitGroup)
	go streamBuildOutput(stderr, diagnostics, &waitGroup)
	waitGroup.Wait()

	if err := cmd.Wait(); err != nil {
		spinner.Stop(false)
		buildErrors := diagnostics.byType("error")
		printDiagnostics(buildErrors)
		if len(buildErrors) == 0 {
			printBuildFallback(diagnostics.fallback())
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", failure, err)
	}

	if err := diagnostics.outputError(); err != nil {
		spinner.Stop(false)
		return fmt.Errorf("read %s output: %w", action, err)
	}
	spinner.Stop(true)
	if !opts.OnlyErrors {
		printDiagnostics(diagnostics.byType("warning"))
	}
	return nil
}

func AppPath(ctx context.Context, opts BuildOptions) (string, error) {
	output, err := runXcodebuild(ctx, append(buildArguments(opts), "-showBuildSettings", "-json")...)
	if err != nil {
		return "", fmt.Errorf("read build settings: %w", err)
	}
	var settings []targetBuildSettings
	if err := json.Unmarshal(output, &settings); err != nil {
		return "", fmt.Errorf("decode build settings: %w", err)
	}

	var paths []string
	for _, setting := range settings {
		if setting.BuildSettings["PRODUCT_TYPE"] != "com.apple.product-type.application" {
			continue
		}
		dir := setting.BuildSettings["TARGET_BUILD_DIR"]
		name := setting.BuildSettings["WRAPPER_NAME"]
		if dir == "" || !strings.HasSuffix(name, ".app") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}

	slices.Sort(paths)
	paths = slices.Compact(paths)
	switch len(paths) {
	case 0:
		return "", errors.New("could not resolve an application product from build settings")
	case 1:
		// Continue below and verify the product exists.
	default:
		return "", fmt.Errorf(
			"scheme produces multiple application products: %s",
			strings.Join(paths, ", "),
		)
	}

	appPath := paths[0]
	if err := ValidateAppPath(appPath); err != nil {
		return "", err
	}
	return appPath, nil
}

func ValidateAppPath(appPath string) error {
	info, err := os.Stat(appPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("app product does not exist at %s", appPath)
		}
		return fmt.Errorf("inspect app product %s: %w", appPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("app product is not a directory: %s", appPath)
	}
	infoPlist := filepath.Join(appPath, "Info.plist")
	if _, err := os.Stat(infoPlist); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("app product is missing Info.plist: %s", appPath)
		}
		return fmt.Errorf("inspect app Info.plist %s: %w", infoPlist, err)
	}
	return nil
}

func buildArguments(opts BuildOptions) []string {
	destination := TargetDestination(opts.Target)
	if opts.Device {
		destination = DeviceDestination(opts.Target)
	}
	return append(projectArguments(opts.Project),
		"-scheme", opts.Scheme,
		"-destination", destination,
		"-skipPackagePluginValidation",
	)
}

func projectArguments(project string) []string {
	if IsWorkspace(project) {
		return []string{"-workspace", project}
	}
	return []string{"-project", project}
}

var (
	locatedDiagnosticPattern = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?:\s*(error|warning):\s*(.+)$`)
	generalDiagnosticPattern = regexp.MustCompile(`^(?:.*?:\s*)?(error|warning):\s*(.+)$`)
)

const maxBuildTailLines = 40

type diagnosticCollector struct {
	mu           sync.Mutex
	root         string
	seen         map[string]struct{}
	list         []Diagnostic
	tail         []string
	outputErrors []error
}

func newDiagnostics(root string) *diagnosticCollector {
	return &diagnosticCollector{
		root: filepath.Clean(root),
		seen: make(map[string]struct{}),
	}
}

func (d *diagnosticCollector) add(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.tail) == 0 || d.tail[len(d.tail)-1] != trimmed {
		d.tail = append(d.tail, trimmed)
		if len(d.tail) > maxBuildTailLines {
			d.tail = d.tail[len(d.tail)-maxBuildTailLines:]
		}
	}

	diagnostic, ok := parseDiagnostic(trimmed, d.root)
	if !ok {
		return
	}
	key := diagnostic.File + ":" + diagnostic.Line + ":" + diagnostic.Column + ":" + diagnostic.Type + ":" + diagnostic.Message
	if _, exists := d.seen[key]; !exists {
		d.seen[key] = struct{}{}
		d.list = append(d.list, diagnostic)
	}
}

func parseDiagnostic(line, root string) (Diagnostic, bool) {
	if matches := locatedDiagnosticPattern.FindStringSubmatch(line); len(matches) == 6 {
		path := filepath.Clean(matches[1])
		if relative, err := filepath.Rel(root, path); err == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path = relative
		}
		return Diagnostic{
			File:    path,
			Line:    matches[2],
			Column:  matches[3],
			Type:    matches[4],
			Message: matches[5],
		}, true
	}
	if matches := generalDiagnosticPattern.FindStringSubmatch(line); len(matches) == 3 {
		return Diagnostic{Type: matches[1], Message: matches[2]}, true
	}
	return Diagnostic{}, false
}

func (d *diagnosticCollector) byType(kind string) []Diagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	var result []Diagnostic
	for _, diagnostic := range d.list {
		if diagnostic.Type == kind {
			result = append(result, diagnostic)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].File + ":" + result[i].Line + ":" + result[i].Column + ":" + result[i].Message
		right := result[j].File + ":" + result[j].Line + ":" + result[j].Column + ":" + result[j].Message
		return left < right
	})
	return result
}

func (d *diagnosticCollector) addOutputError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.outputErrors = append(d.outputErrors, err)
	d.tail = append(d.tail, "read build output: "+err.Error())
}

func (d *diagnosticCollector) outputError() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return errors.Join(d.outputErrors...)
}

func (d *diagnosticCollector) fallback() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return compactCommandError(strings.Join(d.tail, "\n"))
}

func streamBuildOutput(reader io.Reader, diagnostics *diagnosticCollector, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		diagnostics.add(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		diagnostics.addOutputError(err)
	}
}

func printDiagnostics(diagnostics []Diagnostic) {
	writeDiagnostics(os.Stderr, diagnostics)
}

func writeDiagnostics(writer io.Writer, diagnostics []Diagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	for _, diagnostic := range diagnostics {
		location := diagnostic.File
		if diagnostic.Line != "" {
			location += ":" + diagnostic.Line
		}
		if diagnostic.Column != "" {
			location += ":" + diagnostic.Column
		}
		if location != "" {
			location += ": "
		}
		fmt.Fprintf(writer, "%s%s: %s\n", location, diagnostic.Type, diagnostic.Message)
	}
}

func printBuildFallback(output string) {
	if output == "" {
		return
	}
	for _, line := range strings.Split(output, "\n") {
		fmt.Fprintln(os.Stderr, line)
	}
}
