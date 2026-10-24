# xzap

Build, launch, and test iOS apps from the terminal, on Simulators or paired
physical devices. xzap wraps Xcode's tools with short commands, saved device
choices, and compact output that keeps errors visible.

## Install

Requires macOS and full Xcode.

Install or update the latest Apple Silicon release:

```bash
curl -fsSL https://github.com/cadizjavier/xzap/releases/latest/download/install.sh | sh
```

Installs to `~/.local/bin`, verifies the checksum, and prints PATH setup if needed.
Or download the archive from [Releases](https://github.com/cadizjavier/xzap/releases).

Or build from source with Go 1.25.6+:

```bash
go install github.com/cadizjavier/xzap/cmd/xzap@latest
```

Add `GOBIN` (default: `$(go env GOPATH)/bin`) to your `PATH`.
Local builds: `make install` (add `~/.local/bin` to your `PATH`).

## Start

```bash
cd YourApp
xzap init          # Choose a project/workspace and devices
xzap run MyApp     # Build, install, and launch the MyApp scheme
xzap test MyApp    # Run its tests
```

Replace `MyApp` with your Xcode scheme name. Setup is saved in `.xzap`;
add it to your app's `.gitignore` because it contains machine-specific state.

## Commands

```bash
xzap build MyApp                      # Build without launching
xzap clean MyApp                      # Clean build products
xzap test MyApp AppTests/LoginTests    # Run selected tests
xzap doctor                           # Check Xcode, config, and devices
xzap --help                           # All commands and options
```

Supports saved Simulator and physical-device choices. See `xzap --help` for
device management.

Failed tests keep their `.xcresult` bundle and print its path for inspection
in Xcode.

## Contributing

Issues and pull requests welcome. Check changes with:

```bash
go test ./...
go vet ./...
```

## Releasing

Git tags are the version source; untagged local builds report `dev`.
Push a version tag (e.g. `git tag v0.0.1` then `git push origin v0.0.1`).
GitHub Actions checks the code on Linux, cross-compiles for Apple Silicon, and
publishes the archive and checksum under Releases.
Build the same files locally with `make release` from the tagged commit
(output: `dist/`), or set `VERSION=v0.0.1` explicitly.

[MIT License](LICENSE) © 2026 Javier Cadiz.
