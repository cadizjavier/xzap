package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/cadizjavier/xzap/internal"
)

// Set by release builds; go install supplies the module version instead.
var version string

const (
	exitRuntime     = 1
	exitUsage       = 2
	exitBuild       = 3
	exitTests       = 4
	exitInterrupted = 130
)

type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

type timedError struct {
	cause    error
	duration time.Duration
}

func (e timedError) Error() string {
	return fmt.Sprintf("%v after %s", e.cause, internal.FormatDuration(e.duration))
}

func (e timedError) Unwrap() error { return e.cause }

type command struct {
	action          string
	deviceAction    string
	deviceName      string
	scheme          string
	project         string
	defaultDevice   string
	testDevice      string
	on              string
	simulator       string
	physical        string
	defaultFor      string
	testIdentifiers []string
	noColor         bool
	help            bool
	version         bool
}

const (
	commandInit   = "init"
	commandDoctor = "doctor"
	commandBuild  = "build"
	commandClean  = "clean"
	commandRun    = "run"
	commandTest   = "test"
	commandDevice = "device"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command, err := parseCommand(os.Args[1:])
	if err != nil {
		exit(usageError{err.Error()})
	}
	internal.SetColorEnabled(!command.noColor)
	switch {
	case command.help:
		printHelp()
		return
	case command.version:
		fmt.Println(currentVersion())
		return
	}
	started := time.Now()
	switch command.action {
	case commandInit:
		err = initialize(ctx, initOptions{Project: command.project, DefaultDevice: command.defaultDevice, TestDevice: command.testDevice})
	case commandDevice:
		err = manageDevice(ctx, command)
	case commandDoctor:
		err = doctor(ctx)
	case commandBuild:
		err = build(ctx, command.scheme, command.on)
	case commandClean:
		err = clean(ctx, command.scheme, command.on)
	case commandTest:
		err = test(ctx, command.scheme, command.testIdentifiers, command.on)
	default:
		err = run(ctx, command.scheme, command.on)
	}
	if err != nil {
		var silent silentError
		if !errors.As(err, &silent) {
			err = timedError{cause: err, duration: time.Since(started)}
		}
		exit(err)
	}
}

func currentVersion() string {
	moduleVersion := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = info.Main.Version
	}
	return resolveVersion(version, moduleVersion)
}

func resolveVersion(linkedVersion, moduleVersion string) string {
	if linkedVersion != "" {
		return linkedVersion
	}
	if moduleVersion != "" && moduleVersion != "(devel)" {
		return moduleVersion
	}
	return "dev"
}

func parseCommand(args []string) (command, error) {
	result := command{action: commandRun}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case commandInit, commandDoctor, commandBuild, commandClean, commandRun, commandTest, commandDevice:
			result.action = args[0]
			args = args[1:]
		default:
			return command{}, fmt.Errorf("unknown command %q", args[0])
		}
	}
	if result.action == commandDevice && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list", "add", "remove", "default":
			result.deviceAction = args[0]
			args = args[1:]
		default:
			return command{}, fmt.Errorf("unknown device command %q", args[0])
		}
	}

	args = reorderFlags(args)
	flags := flag.NewFlagSet("xzap", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&result.version, "version", false, "print version")
	flags.BoolVar(&result.version, "V", false, "print version")
	flags.BoolVar(&result.help, "help", false, "print help")
	flags.BoolVar(&result.help, "h", false, "print help")
	flags.BoolVar(&result.noColor, "no-color", false, "disable ANSI colors")
	flags.StringVar(&result.project, "project", "", "project or workspace for init")
	flags.StringVar(&result.defaultDevice, "default-device", "", "default device name or ID for init")
	flags.StringVar(&result.testDevice, "test-device", "", "test device name or ID for init")
	flags.StringVar(&result.on, "on", "", "named device to use")
	flags.StringVar(&result.on, "o", "", "named device to use")
	flags.StringVar(&result.simulator, "simulator", "", "Simulator name or ID")
	flags.StringVar(&result.physical, "physical", "", "physical device name or ID")
	flags.StringVar(&result.defaultFor, "for", "", "default scope: run, build, or test")
	if err := flags.Parse(args); err != nil {
		return command{}, err
	}
	if result.action != commandInit && (result.project != "" || result.defaultDevice != "" || result.testDevice != "") {
		return command{}, usageError{"--project, --default-device, and --test-device are only valid with init"}
	}
	if result.action != commandDevice && (result.simulator != "" || result.physical != "" || result.defaultFor != "") {
		return command{}, usageError{"--simulator, --physical, and --for are only valid with device commands"}
	}
	if result.action != commandBuild && result.action != commandClean && result.action != commandRun && result.action != commandTest && result.on != "" {
		return command{}, usageError{"--on is only valid with build, clean, run, or test"}
	}
	positionals := flags.Args()
	if !result.help && !result.version {
		switch result.action {
		case commandBuild, commandClean, commandRun:
			if len(positionals) != 1 {
				return command{}, schemeUsageError(result.action)
			}
			result.scheme = positionals[0]
		case commandTest:
			if len(positionals) == 0 {
				return command{}, schemeUsageError(result.action)
			}
			result.scheme = positionals[0]
			result.testIdentifiers = positionals[1:]
		case commandDevice:
			if result.deviceAction == "" {
				return command{}, usageError{"xzap device requires a command: list, add, remove, or default"}
			}
			if result.deviceAction == "list" {
				if len(positionals) != 0 {
					return command{}, usageError{"xzap device list does not accept arguments"}
				}
			} else if len(positionals) != 1 {
				return command{}, usageError{fmt.Sprintf("xzap device %s requires exactly one name", result.deviceAction)}
			} else {
				result.deviceName = positionals[0]
			}
			if result.simulator != "" && result.physical != "" {
				return command{}, usageError{"--simulator and --physical cannot be combined"}
			}
			if result.deviceAction != "add" && (result.simulator != "" || result.physical != "") {
				return command{}, usageError{"--simulator and --physical are only valid with device add"}
			}
			if result.deviceAction != "default" && result.defaultFor != "" {
				return command{}, usageError{"--for is only valid with device default"}
			}
		default:
			if len(positionals) > 0 {
				return command{}, fmt.Errorf("unexpected arguments: %s", strings.Join(positionals, " "))
			}
		}
	}
	return result, nil
}

func schemeUsageError(action string) error {
	return usageError{fmt.Sprintf("xzap %s requires exactly one scheme", action)}
}

func reorderFlags(args []string) []string {
	var flags, positional []string
	valueFlags := map[string]bool{
		"--project": true, "--default-device": true, "--test-device": true,
		"--on": true, "-o": true, "--simulator": true, "--physical": true, "--for": true,
	}
	boolFlags := map[string]bool{"--no-color": true, "--help": true, "-h": true, "--version": true, "-V": true}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case valueFlags[arg] && index+1 < len(args):
			flags = append(flags, arg, args[index+1])
			index++
		case boolFlags[arg]:
			flags = append(flags, arg)
		case strings.HasPrefix(arg, "-"):
			flags = append(flags, arg)
		default:
			positional = append(positional, arg)
		}
	}
	return append(flags, positional...)
}

type initOptions struct {
	Project       string
	DefaultDevice string
	TestDevice    string
}

func initialize(ctx context.Context, opts initOptions) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	configPath := filepath.Join(root, internal.CacheFile)
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Updating %s\n\n", configPath)
	}
	fmt.Print("1/3 Project — what xzap builds\n")
	var project string
	if opts.Project != "" {
		project, err = internal.ResolveProject(root, opts.Project)
		if err != nil {
			return err
		}
	} else {
		projectProgress := internal.NewStep("Finding Xcode projects")
		projectProgress.Start()
		projects, findErr := internal.FindProjects(".")
		if findErr != nil {
			projectProgress.Stop(false)
			return findErr
		}
		projectProgress.Stop(true)
		project = projects[0]
		if len(projects) > 1 {
			project, err = internal.SelectProject(projects, os.Stdin, os.Stdout)
			if err != nil {
				return err
			}
		}
	}
	fmt.Printf("  %s\n\n", project)
	fmt.Print("2/3 Default device — used by xzap run and build\n")
	targetProgress := internal.NewStep("Finding available devices")
	targetProgress.Start()
	devices, err := internal.ListAvailableDevices(ctx)
	if err != nil {
		targetProgress.Stop(false)
		return err
	}
	targetProgress.Stop(true)
	var defaultDevice internal.Device
	if opts.DefaultDevice != "" {
		defaultDevice, err = internal.MatchDevice(devices, opts.DefaultDevice, "")
	} else {
		defaultDevice, err = internal.SelectConfiguredDevice(devices, internal.Device{}, os.Stdin, os.Stdout)
	}
	if err != nil {
		return fmt.Errorf("select default device: %w", err)
	}
	fmt.Printf("  %s\n\n", internal.DeviceDescription(defaultDevice))
	testDevice := defaultDevice
	if opts.TestDevice != "" {
		testDevice, err = internal.MatchDevice(devices, opts.TestDevice, "")
		if err != nil {
			return fmt.Errorf("select test device: %w", err)
		}
	} else if opts.DefaultDevice == "" {
		fmt.Print("3/3 Test device — used by xzap test; Enter reuses default\n")
		testDevice, err = internal.SelectConfiguredDevice(devices, defaultDevice, os.Stdin, os.Stdout)
		if err != nil {
			return fmt.Errorf("select test device: %w", err)
		}
	}
	fmt.Printf("  %s\n\n", internal.DeviceDescription(testDevice))
	relativeProject, err := filepath.Rel(root, project)
	if err != nil {
		return err
	}
	config := internal.Config{
		Project:    relativeProject,
		Devices:    map[string]internal.Device{"main": defaultDevice},
		Defaults:   internal.DeviceDefaults{Run: "main", Build: "main", Test: "main"},
		OnlyErrors: true,
	}
	if testDevice.Kind != defaultDevice.Kind || testDevice.ID != defaultDevice.ID {
		config.Devices["test"] = testDevice
		config.Defaults.Test = "test"
	}
	path, err := internal.SaveConfig(root, config)
	if err != nil {
		return err
	}
	fmt.Printf("%s Saved %s\n", internal.Green("✓"), path)
	return nil
}

func loadConfigFromWorkingDirectory() (internal.Config, string, error) {
	configPath, err := internal.FindConfig(".")
	if err != nil {
		return internal.Config{}, "", errors.New("no .xzap found; run xzap init first")
	}
	config, err := internal.LoadConfig(configPath)
	if err != nil {
		return internal.Config{}, "", err
	}
	return config, configPath, nil
}

func manageDevice(ctx context.Context, command command) error {
	config, configPath, err := loadConfigFromWorkingDirectory()
	if err != nil {
		return err
	}
	switch command.deviceAction {
	case "list":
		for _, alias := range internal.SortedDeviceAliases(config.Devices) {
			device := config.Devices[alias]
			var defaults []string
			for _, action := range []string{"run", "build", "test"} {
				value, defaultErr := config.DefaultDevice(action)
				if defaultErr == nil && alias == value {
					defaults = append(defaults, action)
				}
			}
			suffix := ""
			if len(defaults) > 0 {
				suffix = " [default: " + strings.Join(defaults, ", ") + "]"
			}
			fmt.Printf("%-16s %s%s\n", alias, internal.DeviceDescription(device), suffix)
		}
		return nil
	case "add":
		return addDevice(ctx, configPath, config, command)
	case "remove":
		if _, exists := config.Devices[command.deviceName]; !exists {
			return fmt.Errorf("unknown device %q", command.deviceName)
		}
		for action, alias := range map[string]string{"run": config.Defaults.Run, "build": config.Defaults.Build, "test": config.Defaults.Test} {
			if alias == command.deviceName {
				return fmt.Errorf("device %q is the %s default; choose another default first", command.deviceName, action)
			}
		}
		delete(config.Devices, command.deviceName)
	case "default":
		if _, exists := config.Devices[command.deviceName]; !exists {
			return fmt.Errorf("unknown device %q", command.deviceName)
		}
		scope := command.defaultFor
		if scope == "" {
			scope = "run"
		}
		switch scope {
		case "run":
			config.Defaults.Run = command.deviceName
		case "build":
			config.Defaults.Build = command.deviceName
		case "test":
			config.Defaults.Test = command.deviceName
		default:
			return usageError{"--for must be run, build, or test"}
		}
	}
	path, err := internal.SaveConfig(filepath.Dir(configPath), config)
	if err != nil {
		return err
	}
	fmt.Printf("%s Updated %s\n", internal.Green("✓"), path)
	return nil
}

func addDevice(ctx context.Context, configPath string, config internal.Config, command command) error {
	if err := internal.ValidateDeviceAlias(command.deviceName); err != nil {
		return usageError{err.Error()}
	}
	if _, exists := config.Devices[command.deviceName]; exists {
		return fmt.Errorf("device %q already exists; remove it first", command.deviceName)
	}
	var selected internal.Device
	var err error
	switch {
	case command.simulator != "":
		var targets []internal.Target
		targets, err = internal.ListTargets(ctx)
		if err == nil {
			devices := make([]internal.Device, 0, len(targets))
			for _, target := range targets {
				devices = append(devices, internal.DeviceFromTarget(internal.SimulatorDevice, target))
			}
			selected, err = internal.MatchDevice(devices, command.simulator, internal.SimulatorDevice)
		}
	case command.physical != "":
		var targets []internal.Target
		targets, err = internal.ListDevices(ctx)
		if err == nil {
			devices := make([]internal.Device, 0, len(targets))
			for _, target := range targets {
				devices = append(devices, internal.DeviceFromTarget(internal.PhysicalDevice, target))
			}
			selected, err = internal.MatchDevice(devices, command.physical, internal.PhysicalDevice)
		}
	default:
		var devices []internal.Device
		devices, err = internal.ListAvailableDevices(ctx)
		if err == nil {
			selected, err = internal.SelectConfiguredDevice(devices, internal.Device{}, os.Stdin, os.Stdout)
		}
	}
	if err != nil {
		return fmt.Errorf("select device: %w", err)
	}
	config.Devices[command.deviceName] = selected
	path, err := internal.SaveConfig(filepath.Dir(configPath), config)
	if err != nil {
		return err
	}
	fmt.Printf("%s Added %s → %s in %s\n", internal.Green("✓"), command.deviceName, internal.DeviceDescription(selected), path)
	return nil
}

type runContext struct {
	project    string
	alias      string
	device     internal.Device
	onlyErrors bool
}

func resolveRunContext(ctx context.Context, action, requested string) (runContext, error) {
	config, configPath, err := loadConfigFromWorkingDirectory()
	if err != nil {
		return runContext{}, err
	}
	root := filepath.Dir(configPath)
	project, err := internal.ResolveProject(root, config.Project)
	if err != nil {
		return runContext{}, err
	}
	alias := requested
	if alias == "" {
		alias, err = config.DefaultDevice(action)
		if err != nil {
			return runContext{}, err
		}
	}
	configured, exists := config.Devices[alias]
	if !exists {
		return runContext{}, fmt.Errorf("unknown device %q; run xzap device list", alias)
	}
	resolved, recovered, err := internal.ResolveConfiguredDevice(ctx, configured)
	if err != nil {
		return runContext{}, fmt.Errorf("resolve device %q: %w", alias, err)
	}
	if recovered {
		config.Devices[alias] = resolved
		if _, err := internal.SaveConfig(root, config); err != nil {
			return runContext{}, fmt.Errorf("save recovered device: %w", err)
		}
	}
	return runContext{project: project, alias: alias, device: resolved, onlyErrors: config.OnlyErrors}, nil
}

func run(ctx context.Context, scheme, requested string) error {
	started := time.Now()
	resolved, err := resolveRunContext(ctx, commandRun, requested)
	if err != nil {
		return err
	}
	runTarget := resolved.device.Target()
	usePhysical := resolved.device.Kind == internal.PhysicalDevice
	buildOptions := internal.BuildOptions{
		Project:    resolved.project,
		Scheme:     scheme,
		Target:     runTarget,
		Device:     usePhysical,
		OnlyErrors: resolved.onlyErrors,
	}
	if usePhysical {
		if err := internal.Build(ctx, buildOptions); err != nil {
			return err
		}
	} else {
		operationCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		bootResult := make(chan error, 1)
		go func() {
			err := internal.BootTargetQuiet(operationCtx, runTarget)
			if err != nil {
				cancel()
			}
			bootResult <- err
		}()
		buildErr := internal.Build(operationCtx, buildOptions)
		if buildErr != nil {
			cancel()
		}
		bootErr := <-bootResult
		if buildErr != nil || bootErr != nil {
			return concurrentRunError(ctx, buildErr, bootErr)
		}
	}
	appPath, err := internal.AppPath(ctx, buildOptions)
	if err != nil {
		return err
	}
	bundleID, err := internal.BundleIDFromApp(ctx, appPath)
	if err != nil {
		return err
	}
	if usePhysical {
		if err := internal.InstallDevice(ctx, runTarget, appPath); err != nil {
			return err
		}
		if err := internal.LaunchDevice(ctx, runTarget, bundleID); err != nil {
			return err
		}
	} else {
		if err := internal.InstallTarget(ctx, runTarget, appPath); err != nil {
			return err
		}
		if err := internal.LaunchTarget(ctx, runTarget, bundleID); err != nil {
			return err
		}
		if err := internal.ShowTarget(ctx, runTarget); err != nil {
			return err
		}
	}
	fmt.Printf("%s Launched %s → %s (%s) in %s\n", internal.Green("✓"), scheme, resolved.alias, internal.TargetDescription(runTarget), internal.FormatDuration(time.Since(started)))
	return nil
}

func concurrentRunError(ctx context.Context, buildErr, bootErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if buildErr != nil && errors.Is(bootErr, context.Canceled) {
		return buildErr
	}
	if bootErr != nil && errors.Is(buildErr, context.Canceled) {
		return bootErr
	}
	return errors.Join(buildErr, bootErr)
}

func build(ctx context.Context, scheme, requested string) error {
	resolved, err := resolveRunContext(ctx, commandBuild, requested)
	if err != nil {
		return err
	}
	target := resolved.device.Target()
	err = internal.Build(ctx, internal.BuildOptions{
		Project: resolved.project, Scheme: scheme, Target: target, Device: resolved.device.Kind == internal.PhysicalDevice,
		OnlyErrors: resolved.onlyErrors,
	})
	return err
}

func clean(ctx context.Context, scheme, requested string) error {
	resolved, err := resolveRunContext(ctx, commandBuild, requested)
	if err != nil {
		return err
	}
	return internal.Clean(ctx, internal.BuildOptions{
		Project: resolved.project, Scheme: scheme, Target: resolved.device.Target(), Device: resolved.device.Kind == internal.PhysicalDevice,
		OnlyErrors: resolved.onlyErrors,
	})
}

func test(ctx context.Context, scheme string, identifiers []string, requested string) error {
	resolved, err := resolveRunContext(ctx, commandTest, requested)
	if err != nil {
		return err
	}
	testTarget := resolved.device.Target()
	physical := resolved.device.Kind == internal.PhysicalDevice
	if !physical {
		if err := internal.BootTarget(ctx, testTarget); err != nil {
			return err
		}
	}
	summary, err := internal.RunTests(ctx, internal.TestOptions{
		BuildOptions: internal.BuildOptions{
			Project: resolved.project, Scheme: scheme, Target: testTarget, Device: physical,
			OnlyErrors: resolved.onlyErrors,
		},
		Identifiers: identifiers,
	})
	if summary.Total > 0 {
		internal.PrintTestSummary(summary, testTarget)
	}
	return err
}

func printHelp() {
	fmt.Fprintf(os.Stderr, `xzap - build, run, and test an iOS app

Usage:
  xzap init [--project PATH --default-device NAME_OR_ID --test-device NAME_OR_ID]
  xzap device list
  xzap device add NAME [--simulator NAME_OR_ID | --physical NAME_OR_ID]
  xzap device remove NAME
  xzap device default NAME [--for run|build|test]
  xzap doctor
  xzap build SCHEME [--on NAME]
  xzap clean SCHEME [--on NAME]
  xzap run SCHEME [--on NAME]
  xzap test SCHEME [TEST-IDENTIFIER...] [--on NAME]

Run "xzap init" from the project directory to create or update .xzap with the
project and named devices. A device can be a Simulator or a physical iOS
device. Commands use their configured default unless --on selects another.

Options:
  -h, --help                    Print help
  -V, --version                 Print version
  -o, --on NAME                 Use a named device
      --project PATH            Project or workspace for init
      --default-device VALUE    Default device name or ID for init
      --test-device VALUE       Test device name or ID for init
      --simulator VALUE         Simulator name or ID for device add
      --physical VALUE          Physical device name or ID for device add
      --for ACTION              Default scope: run, build, or test
      --no-color                Disable ANSI colors

Examples:
  xzap init
  xzap device add phone --physical "Javier's iPhone"
  xzap device default phone
  xzap device default main --for test
  xzap doctor
  xzap build SCHEME
  xzap clean SCHEME
  xzap run SCHEME
  xzap run SCHEME --on phone
  xzap test SCHEME
  xzap test SCHEME AppTests/LoginTests/testExpiredSession

Exit codes: 1 runtime/config, 2 usage, 3 build/clean, 4 tests, 130 interrupted.
`)
}

func exit(err error) {
	code := exitCode(err)
	var silent silentError
	if errors.As(err, &silent) {
		os.Exit(code)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", internal.Red("Error:"), err)
	os.Exit(code)
}

func exitCode(err error) int {
	var usage usageError
	switch {
	case errors.As(err, &usage):
		return exitUsage
	case errors.Is(err, context.Canceled):
		return exitInterrupted
	case errors.Is(err, internal.ErrBuildFailed), errors.Is(err, internal.ErrCleanFailed):
		return exitBuild
	case errors.Is(err, internal.ErrTestsFailed), errors.Is(err, internal.ErrNoTests):
		return exitTests
	default:
		return exitRuntime
	}
}
