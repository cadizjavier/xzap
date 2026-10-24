package internal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleaseInstaller(t *testing.T) {
	for _, test := range []struct {
		name, system, arch, rosetta, downloadFailure, wantError string
		badChecksum, badManifest                                bool
	}{
		{name: "install and update", system: "Darwin", arch: "arm64"},
		{name: "Rosetta", system: "Darwin", arch: "x86_64", rosetta: "1"},
		{name: "Linux", system: "Linux", arch: "arm64", wantError: "requires macOS"},
		{name: "Intel Mac", system: "Darwin", arch: "x86_64", wantError: "requires an Apple Silicon Mac"},
		{name: "bad checksum", system: "Darwin", arch: "arm64", badChecksum: true, wantError: "Checksum verification failed"},
		{name: "bad manifest", system: "Darwin", arch: "arm64", badManifest: true, wantError: "Could not find an Apple Silicon archive"},
		{name: "download failure", system: "Darwin", arch: "arm64", downloadFailure: "1", wantError: "download failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin := filepath.Join(root, "fake-bin")
			installDir := filepath.Join(root, "bin with spaces")
			payloadDir := filepath.Join(root, "payload")
			for _, dir := range []string{fakeBin, installDir, payloadDir} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, contents string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(contents), mode); err != nil {
					t.Fatal(err)
				}
			}
			const payload = "#!/bin/sh\nprintf '%s\\n' v1.2.3\n"
			const previous = "previous installation"
			write(filepath.Join(payloadDir, "xzap"), payload, 0o755)
			installed := filepath.Join(installDir, "xzap")
			write(installed, previous, 0o755)
			const archiveName = "xzap_v1.2.3_macos_arm64.tar.gz"
			archive := filepath.Join(root, archiveName)
			if output, err := exec.Command("tar", "-czf", archive, "-C", payloadDir, "xzap").CombinedOutput(); err != nil {
				t.Fatalf("create fixture archive: %v\n%s", err, output)
			}
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			checksum := sha256.Sum256(data)
			if test.badChecksum {
				checksum = [32]byte{}
			}
			manifest := fmt.Sprintf("%x  %s\n", checksum, archiveName)
			if test.badManifest {
				manifest = "not a release manifest\n"
			}
			write(filepath.Join(root, "checksums.txt"), manifest, 0o644)
			write(filepath.Join(fakeBin, "uname"), `#!/bin/sh
case "$1" in
  -s) printf '%s\n' "$TEST_INSTALL_OS" ;;
  -m) printf '%s\n' "$TEST_INSTALL_ARCH" ;;
esac
`, 0o755)
			write(filepath.Join(fakeBin, "sysctl"), "#!/bin/sh\nprintf '%s\\n' \"${TEST_INSTALL_ROSETTA:-0}\"\n", 0o755)
			write(filepath.Join(fakeBin, "curl"), `#!/bin/sh
set -eu
url= output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift 2 ;;
    https://*) url=$1; shift ;;
    *) shift ;;
  esac
done
printf '%s\t%s\n' "$url" "$output" >> "$TEST_INSTALL_FIXTURE/curl.log"
if [ "$TEST_INSTALL_DOWNLOAD_FAILURE" = 1 ]; then
  echo 'download failed' >&2
  exit 22
fi
case "$url" in
  https://github.com/cadizjavier/xzap/releases/latest/download/checksums.txt)
    cp "$TEST_INSTALL_FIXTURE/checksums.txt" "$output" ;;
  https://github.com/cadizjavier/xzap/releases/download/v1.2.3/xzap_v1.2.3_macos_arm64.tar.gz)
    cp "$TEST_INSTALL_FIXTURE/xzap_v1.2.3_macos_arm64.tar.gz" "$output" ;;
  *) echo "unexpected URL: $url" >&2; exit 22 ;;
esac
`, 0o755)
			script, err := os.ReadFile(filepath.Join("..", "install.sh"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh")
			cmd.Stdin = strings.NewReader(string(script))
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"XZAP_INSTALL_DIR="+installDir,
				"TEST_INSTALL_FIXTURE="+root,
				"TEST_INSTALL_OS="+test.system,
				"TEST_INSTALL_ARCH="+test.arch,
				"TEST_INSTALL_ROSETTA="+test.rosetta,
				"TEST_INSTALL_DOWNLOAD_FAILURE="+test.downloadFailure,
			)
			output, runErr := cmd.CombinedOutput()
			want := payload
			if test.wantError != "" {
				want = previous
				if runErr == nil || !strings.Contains(string(output), test.wantError) {
					t.Fatalf("installer error = %v, output = %q; want %q", runErr, output, test.wantError)
				}
			} else if runErr != nil || !strings.Contains(string(output), "Installed xzap v1.2.3") || !strings.Contains(string(output), "PATH") {
				t.Fatalf("installer error = %v, output = %q", runErr, output)
			}
			contents, err := os.ReadFile(installed)
			if err != nil || string(contents) != want {
				t.Fatalf("installed contents = %q, %v; want %q", contents, err, want)
			}
			info, err := os.Stat(installed)
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("installed executable permissions: %v, %v", info, err)
			}
			if log, err := os.ReadFile(filepath.Join(root, "curl.log")); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
					fields := strings.SplitN(line, "\t", 2)
					if len(fields) != 2 {
						t.Fatalf("invalid download log: %q", line)
					}
					if _, err := os.Stat(filepath.Dir(fields[1])); !os.IsNotExist(err) {
						t.Fatalf("installer did not clean temporary files: %s, %v", fields[1], err)
					}
				}
			}
		})
	}
}
