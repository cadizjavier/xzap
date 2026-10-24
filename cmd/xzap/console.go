package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cadizjavier/xzap/internal"
)

type doctorCheck struct {
	Name   string
	OK     bool
	Detail string
}

type silentError struct{ message string }

func (e silentError) Error() string { return e.message }

func doctor(ctx context.Context) error {
	var checks []doctorCheck
	add := func(name, detail string, err error) {
		if err != nil {
			checks = append(checks, doctorCheck{Name: name, Detail: err.Error()})
			return
		}
		checks = append(checks, doctorCheck{Name: name, OK: true, Detail: detail})
	}

	xcodeVersion, err := internal.XcodeVersion(ctx)
	add("Xcode", firstLine(xcodeVersion), err)
	targets, targetErr := internal.ListTargets(ctx)
	add("Simulators", fmt.Sprintf("%d available", len(targets)), targetErr)

	configPath, err := internal.FindConfig(".")
	if err != nil {
		add("Configuration", "", errors.New("no .xzap found; run xzap init"))
	} else {
		config, loadErr := internal.LoadConfig(configPath)
		add("Configuration", configPath, loadErr)
		if loadErr == nil {
			project, projectErr := internal.ResolveProject(filepath.Dir(configPath), config.Project)
			add("Project", project, projectErr)
			for _, alias := range internal.SortedDeviceAliases(config.Devices) {
				device, _, deviceErr := internal.ResolveConfiguredDevice(ctx, config.Devices[alias])
				add("Device "+alias, internal.DeviceDescription(device), deviceErr)
			}
		}
	}

	ok := true
	for _, check := range checks {
		ok = ok && check.OK
	}
	for _, check := range checks {
		icon := internal.Green("✓")
		if !check.OK {
			icon = internal.Red("✗")
		}
		fmt.Printf("%s %-15s %s\n", icon, check.Name, check.Detail)
	}
	if !ok {
		return silentError{"doctor checks failed"}
	}
	return nil
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return line
}
