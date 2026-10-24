#!/bin/sh

main() {
    set -eu

    if [ "$(uname -s)" != Darwin ]; then
        echo 'xzap requires macOS.' >&2
        exit 1
    fi
    if [ "$(uname -m)" != arm64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" != 1 ]; then
        echo 'This release requires an Apple Silicon Mac.' >&2
        exit 1
    fi

    release_url=https://github.com/cadizjavier/xzap/releases
    install_dir=${XZAP_INSTALL_DIR:-"$HOME/.local/bin"}
    work_dir=$(mktemp -d "${TMPDIR:-/tmp}/xzap-install.XXXXXX")
    trap 'rm -rf "$work_dir"' 0
    trap 'exit 1' HUP INT TERM

    curl -fsSL "$release_url/latest/download/checksums.txt" -o "$work_dir/checksums.txt"
    archive=$(awk 'NF == 2 && $2 ~ /^xzap_v[0-9][A-Za-z0-9._-]*_macos_arm64[.]tar[.]gz$/ { print $2 }' "$work_dir/checksums.txt")
    case "$archive" in
        ''|*[!A-Za-z0-9._-]*)
            echo 'Could not find an Apple Silicon archive in the release checksums.' >&2
            exit 1
            ;;
    esac

    version=${archive#xzap_}
    version=${version%_macos_arm64.tar.gz}
    printf 'Downloading xzap %s…\n' "$version"
    # Pin the archive to the release named in the checksums, even if latest changes.
    curl -fsSL "$release_url/download/$version/$archive" -o "$work_dir/$archive"
    if ! (cd "$work_dir" && shasum -a 256 -c checksums.txt); then
        echo 'Checksum verification failed; xzap was not installed.' >&2
        exit 1
    fi
    tar -xzf "$work_dir/$archive" -C "$work_dir" xzap
    if [ ! -f "$work_dir/xzap" ] || [ -L "$work_dir/xzap" ]; then
        echo 'The release archive does not contain an xzap executable.' >&2
        exit 1
    fi

    mkdir -p "$install_dir"
    install -m 755 "$work_dir/xzap" "$install_dir/xzap"
    printf 'Installed xzap %s to %s/xzap\n' "$version" "$install_dir"
    case ":${PATH:-}:" in
        *":$install_dir:"*) ;;
        *)
            if [ "$install_dir" = "$HOME/.local/bin" ]; then
                printf '\nAdd this line to ~/.zshrc, then open a new terminal:\n  export PATH="$HOME/.local/bin:$PATH"\n'
            else
                printf '\nAdd %s to your PATH to run xzap.\n' "$install_dir"
            fi
            ;;
    esac
}

main "$@"
