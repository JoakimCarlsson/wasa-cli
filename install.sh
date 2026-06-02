#!/usr/bin/env bash
#
# wasa installer.
#
#   curl -fsSL https://raw.githubusercontent.com/JoakimCarlsson/wasa/main/install.sh | bash
#
# Environment variables:
#   BIN_DIR   install directory          (default: $HOME/.local/bin)
#   VERSION   release tag to install     (default: latest)

set -euo pipefail

REPO="JoakimCarlsson/wasa"
BINARY="wasa"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-latest}"
tmp=""

err() {
    echo "error: $1" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || err "'$1' is required but not installed."
}

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        linux) PLATFORM="linux" ;;
        darwin) PLATFORM="darwin" ;;
        mingw* | msys* | cygwin*)
            err "wasa has no native Windows build. Install it inside WSL2 instead." ;;
        *) err "unsupported OS: $os" ;;
    esac

    case "$arch" in
        x86_64 | amd64) ARCH="amd64" ;;
        arm64 | aarch64) ARCH="arm64" ;;
        *) err "unsupported architecture: $arch" ;;
    esac
}

resolve_version() {
    if [ "$VERSION" = "latest" ]; then
        local resp
        resp="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")" \
            || err "could not query the latest release. Has one been published yet?"
        if [[ "$resp" =~ \"tag_name\":[[:space:]]*\"([^\"]+)\" ]]; then
            VERSION="${BASH_REMATCH[1]}"
        else
            err "could not parse the latest release tag from the GitHub API response."
        fi
    fi
    VERSION="${VERSION#v}"
}

check_runtime_deps() {
    local missing=""
    command -v git >/dev/null 2>&1 || missing="git"
    command -v tmux >/dev/null 2>&1 || missing="${missing:+$missing }tmux"
    if [ -n "$missing" ]; then
        echo "note: wasa needs these at runtime but they were not found: $missing"
        echo "      install them with your package manager before running wasa."
    fi
}

setup_path() {
    case "${SHELL:-}" in
        */zsh) profile="$HOME/.zshrc" ;;
        */bash) profile="$HOME/.bashrc" ;;
        */fish) profile="$HOME/.config/fish/config.fish" ;;
        *) profile="$HOME/.profile" ;;
    esac

    case ":$PATH:" in
        *":$BIN_DIR:"*) return ;;
    esac

    if [ "$profile" = "$HOME/.config/fish/config.fish" ]; then
        printf '\nset -gx PATH %s $PATH\n' "$BIN_DIR" >> "$profile"
    else
        printf '\nexport PATH="%s:$PATH"\n' "$BIN_DIR" >> "$profile"
    fi
    echo "added $BIN_DIR to your PATH in $profile — open a new terminal to pick it up."
}

main() {
    need curl
    need tar
    detect_platform
    resolve_version

    local archive url
    archive="${BINARY}_${VERSION}_${PLATFORM}_${ARCH}.tar.gz"
    url="https://github.com/${REPO}/releases/download/v${VERSION}/${archive}"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT

    echo "downloading ${archive} ..."
    curl -fsSL "$url" -o "$tmp/$archive" \
        || err "download failed: $url (was the asset built for ${PLATFORM}_${ARCH}?)"

    if curl -fsSL "https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt" \
        -o "$tmp/checksums.txt" 2>/dev/null; then
        if command -v sha256sum >/dev/null 2>&1; then
            (cd "$tmp" && grep " $archive\$" checksums.txt | sha256sum -c -) \
                || err "checksum verification failed for $archive"
        elif command -v shasum >/dev/null 2>&1; then
            (cd "$tmp" && grep " $archive\$" checksums.txt | shasum -a 256 -c -) \
                || err "checksum verification failed for $archive"
        fi
    fi

    tar -xzf "$tmp/$archive" -C "$tmp"
    mkdir -p "$BIN_DIR"
    install -m 0755 "$tmp/$BINARY" "$BIN_DIR/$BINARY" 2>/dev/null \
        || { mv "$tmp/$BINARY" "$BIN_DIR/$BINARY" && chmod 0755 "$BIN_DIR/$BINARY"; }

    setup_path
    check_runtime_deps

    echo ""
    echo "installed: $("$BIN_DIR/$BINARY" --version 2>/dev/null || echo "$BIN_DIR/$BINARY")"
}

main "$@"
