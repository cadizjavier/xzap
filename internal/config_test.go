package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigRequiredFields(t *testing.T) {
	valid := Config{
		Project: "MyApp.xcodeproj",
		Devices: map[string]Device{
			"main": {Kind: SimulatorDevice, Name: "iPhone", ID: "SIMULATOR-ID"},
		},
		Defaults: DeviceDefaults{Run: "main", Build: "main", Test: "main"},
	}
	tests := []struct {
		name  string
		clear func(*Config)
		want  string
	}{
		{name: "project", clear: func(config *Config) { config.Project = "" }, want: `field "project" is required`},
		{name: "devices", clear: func(config *Config) { config.Devices = nil }, want: "at least one device"},
		{name: "run default", clear: func(config *Config) { config.Defaults.Run = "" }, want: `field "defaults.run" is required`},
		{name: "build default", clear: func(config *Config) { config.Defaults.Build = "" }, want: `field "defaults.build" is required`},
		{name: "test default", clear: func(config *Config) { config.Defaults.Test = "" }, want: `field "defaults.test" is required`},
		{name: "unknown default", clear: func(config *Config) { config.Defaults.Test = "missing" }, want: `references unknown device "missing"`},
		{name: "invalid alias", clear: func(config *Config) {
			config.Devices["Bad Name"] = config.Devices["main"]
			delete(config.Devices, "main")
			config.Defaults.Run = "Bad Name"
		}, want: "invalid device name"},
		{name: "invalid kind", clear: func(config *Config) {
			device := config.Devices["main"]
			device.Kind = "watch"
			config.Devices["main"] = device
		}, want: "invalid kind"},
		{name: "device id", clear: func(config *Config) {
			device := config.Devices["main"]
			device.ID = ""
			config.Devices["main"] = device
		}, want: `field "devices.main.id" is required`},
		{name: "device name", clear: func(config *Config) {
			device := config.Devices["main"]
			device.Name = ""
			config.Devices["main"] = device
		}, want: `field "devices.main.name" is required`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.Devices = map[string]Device{"main": valid.Devices["main"]}
			test.clear(&config)
			err := ValidateConfig(config)
			if err == nil {
				t.Fatal("ValidateConfig() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestDefaultDeviceUsesExplicitActionDefaults(t *testing.T) {
	config := Config{
		Project: "MyApp.xcodeproj",
		Devices: map[string]Device{
			"main": {Kind: SimulatorDevice, Name: "iPhone", ID: "SIMULATOR"},
			"test": {Kind: SimulatorDevice, Name: "iPad", ID: "TEST"},
		},
		Defaults: DeviceDefaults{Run: "main", Build: "main", Test: "test"},
	}
	if err := ValidateConfig(config); err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	for action, want := range map[string]string{"run": "main", "build": "main", "test": "test"} {
		got, err := config.DefaultDevice(action)
		if err != nil || got != want {
			t.Errorf("DefaultDevice(%q) = %q, %v; want %q", action, got, err, want)
		}
	}
}

func TestLoadConfigRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), CacheFile)
	if err := os.WriteFile(path, []byte(`{"project":`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("error = %q, want JSON context", err)
	}
}

func TestLoadConfigRejectsRemovedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), CacheFile)
	data := []byte(`{
		"project":"MyApp.xcodeproj",
		"simulator_id":"SIMULATOR-ID",
		"simulator_name":"iPhone"
	}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "at least one device") {
		t.Errorf("LoadConfig() error = %v, want removed schema rejection", err)
	}
}

func TestResolveProjectSupportsRelativeAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "Projects", "MyApp.xcodeproj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	relative, err := ResolveProject(root, filepath.Join("Projects", "MyApp.xcodeproj"))
	if err != nil {
		t.Fatalf("ResolveProject(relative) error = %v", err)
	}
	absolute, err := ResolveProject(root, project)
	if err != nil {
		t.Fatalf("ResolveProject(absolute) error = %v", err)
	}
	if relative != project || absolute != project {
		t.Errorf("resolved paths = %q, %q; want %q", relative, absolute, project)
	}
}

func TestResolveProjectRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()
	fileProject := filepath.Join(root, "File.xcodeproj")
	if err := os.WriteFile(fileProject, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrongType := filepath.Join(root, "MyApp")
	if err := os.Mkdir(wrongType, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		project string
		want    string
	}{
		{name: "missing", project: "Missing.xcodeproj", want: "configured project not found"},
		{name: "file", project: "File.xcodeproj", want: "configured project is not a directory"},
		{name: "wrong bundle type", project: "MyApp", want: "not an Xcode project or workspace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveProject(root, test.project)
			if err == nil {
				t.Fatal("ResolveProject() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want %q", err, test.want)
			}
		})
	}
}
