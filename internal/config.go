package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const CacheFile = ".xzap"

type Config struct {
	Project    string            `json:"project"`
	Devices    map[string]Device `json:"devices"`
	Defaults   DeviceDefaults    `json:"defaults"`
	OnlyErrors bool              `json:"only_errors"`
}

type DeviceKind string

const (
	SimulatorDevice DeviceKind = "simulator"
	PhysicalDevice  DeviceKind = "physical"
)

type Device struct {
	Kind DeviceKind `json:"kind"`
	Name string     `json:"name"`
	ID   string     `json:"id"`

	Model       string `json:"-"`
	OSVersion   string `json:"-"`
	State       string `json:"-"`
	IsAvailable bool   `json:"-"`
}

type DeviceDefaults struct {
	Run   string `json:"run"`
	Build string `json:"build"`
	Test  string `json:"test"`
}

var deviceAliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func ValidateDeviceAlias(alias string) error {
	if !deviceAliasPattern.MatchString(alias) {
		return fmt.Errorf("invalid device name %q; use lowercase letters, numbers, '.', '_', or '-'", alias)
	}
	return nil
}

func (config Config) DefaultDevice(action string) (string, error) {
	switch action {
	case "build":
		return config.Defaults.Build, nil
	case "test":
		return config.Defaults.Test, nil
	case "run":
		return config.Defaults.Run, nil
	default:
		return "", fmt.Errorf("unknown device default %q", action)
	}
}

func SortedDeviceAliases(devices map[string]Device) []string {
	aliases := make([]string, 0, len(devices))
	for alias := range devices {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

func FindConfig(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		path := filepath.Join(dir, CacheFile)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err := ValidateConfig(config); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return config, nil
}

func ValidateConfig(config Config) error {
	if config.Project == "" {
		return fmt.Errorf(".xzap field %q is required; run xzap init", "project")
	}
	if len(config.Devices) == 0 {
		return errors.New(".xzap must configure at least one device; run xzap init")
	}
	for alias, device := range config.Devices {
		if err := ValidateDeviceAlias(alias); err != nil {
			return fmt.Errorf(".xzap device: %w", err)
		}
		if device.Kind != SimulatorDevice && device.Kind != PhysicalDevice {
			return fmt.Errorf(".xzap device %q has invalid kind %q", alias, device.Kind)
		}
		if device.ID == "" {
			return fmt.Errorf(".xzap field %q is required", "devices."+alias+".id")
		}
		if device.Name == "" {
			return fmt.Errorf(".xzap field %q is required", "devices."+alias+".name")
		}
	}
	for action, alias := range map[string]string{
		"run": config.Defaults.Run, "build": config.Defaults.Build, "test": config.Defaults.Test,
	} {
		if alias == "" {
			return fmt.Errorf(".xzap field %q is required; run xzap init", "defaults."+action)
		}
		if _, exists := config.Devices[alias]; !exists {
			return fmt.Errorf(".xzap default for %s references unknown device %q", action, alias)
		}
	}
	return nil
}

func ResolveProject(root, configured string) (string, error) {
	project := configured
	if !filepath.IsAbs(project) {
		project = filepath.Join(root, project)
	}
	project = filepath.Clean(project)
	if !strings.HasSuffix(project, ".xcodeproj") && !strings.HasSuffix(project, ".xcworkspace") {
		return "", fmt.Errorf(
			"configured project is not an Xcode project or workspace: %s; run xzap init",
			project,
		)
	}
	info, err := os.Stat(project)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("configured project not found: %s; run xzap init", project)
		}
		return "", fmt.Errorf("inspect configured project %s: %w", project, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("configured project is not a directory: %s; run xzap init", project)
	}
	return project, nil
}

func SaveConfig(root string, config Config) (string, error) {
	if err := ValidateConfig(config); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}

	path := filepath.Join(root, CacheFile)
	temp, err := os.CreateTemp(root, ".xzap-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())

	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
