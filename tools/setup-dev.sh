#!/usr/bin/env bash
# Set up a Linux or macOS machine to build, test, lint and debug pawnstar-go: the toolchain, the tools and the editor.
# Each component is checked first and installed only when it is missing or not the version the repo wants, so the
# script is safe to run again at any time. Go and its tools go into the user's own profile (~/.local/go, ~/.local/bin,
# ~/go/bin); the system packages come from apt (Debian, Ubuntu, Mint: sudo asks for a password) or, on macOS, with
# Xcode's command-line tools. tools/setup-dev.ps1 is the same for Windows. VS Code's "setup environment" and "check
# environment" tasks run the one for the platform.
#
# What it sets up:
#   - a C compiler for cgo, which the race detector (`make race`) needs, with make, git and zip (build-essential, git,
#     zip; Xcode's command-line tools on macOS)
#   - Go, the newest 1.26.x (go.mod's line) in ~/.local/go unless a 1.26 is on the PATH already: never 1.27 or later,
#     whose simd/archsimd API the amd64 NNUE kernels do not compile against
#   - golangci-lint, the newest release of the line CI pins (.github/workflows/ci.yml), in ~/.local/bin, and gopls and
#     dlv for VS Code's Go extension, in ~/go/bin
#   - the repo's git hooks (core.hooksPath .githooks) and the VS Code extensions .vscode/extensions.json recommends
#
#   tools/setup-dev.sh                # set up
#   tools/setup-dev.sh --check        # report only; exit 1 if anything is missing
#   tools/setup-dev.sh --no-persist   # add nothing to the shell profile's PATH (a test run in a scratch HOME)
set -euo pipefail
cd "$(dirname "$0")/.."

check=0
persist=1
for arg in "$@"; do
    case "$arg" in
        --check) check=1 ;;
        --no-persist) persist=0 ;;
        -h | --help) sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "unknown argument: $arg" >&2; exit 2 ;;
    esac
done

# ---------------------------------------------------------------------------------------------------------------------
# Helpers (the same in every reckless-technology repo's setup script).
missing=0
ok() { printf '  \033[32mok\033[0m       %-24s %s\n' "$1" "$2"; }
miss() { missing=$((missing + 1)); printf '  \033[33mmissing\033[0m  %-24s %s\n' "$1" "$2"; }
installing() { printf '  \033[36minstall\033[0m  %-24s %s\n' "$1" "$2"; }
have() { command -v "$1" >/dev/null 2>&1; }
os=$(uname -s)
start_path=":$PATH:"

# Put a directory on this shell's PATH and, unless --no-persist or --check, on every new shell's (the profiles a login
# shell, bash and zsh read) - unless the PATH this script started with had it already.
add_to_path() {
    case ":$PATH:" in *":$1:"*) ;; *) PATH="$1:$PATH"; export PATH ;; esac
    [ "$persist" = 1 ] && [ "$check" = 0 ] || return 0
    case "$start_path" in *":$1:"*) return 0 ;; esac
    local line="export PATH=\"$1:\$PATH\""
    local profiles=("$HOME/.profile" "$HOME/.bashrc")
    [ "$os" = Darwin ] && profiles+=("$HOME/.zprofile")
    for f in "${profiles[@]}"; do
        grep -qxF "$line" "$f" 2>/dev/null || printf '\n%s\n' "$line" >>"$f"
    done
}

sha256_of() { if have sha256sum; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# Download a file and refuse it unless its SHA-256 is the one given.
fetch_verified() {
    curl -fsSL "$1" -o "$3"
    local got
    got=$(sha256_of "$3")
    if [ "$got" != "$2" ]; then rm -f "$3"; echo "SHA-256 mismatch for $1 (got $got, want $2)" >&2; exit 1; fi
}

sudo_cmd() { if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi; }

# apt packages that are not installed yet, installed together (Debian, Ubuntu, Mint).
apt_packages() {
    local need=()
    for p in "$@"; do
        grep -q 'install ok installed' <<<"$(dpkg-query -W -f='${Status}' "$p" 2>/dev/null || true)" || need+=("$p")
    done
    if [ ${#need[@]} = 0 ]; then ok 'apt packages' "$*"; return; fi
    if [ "$check" = 1 ]; then miss 'apt packages' "${need[*]}"; return; fi
    installing 'apt packages' "${need[*]}"
    sudo_cmd apt-get update -q
    sudo_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y -q "${need[@]}"
}

# macOS: Xcode's command-line tools (clang, make, git; zip is the system's own).
macos_base() {
    if xcode-select -p >/dev/null 2>&1; then ok 'Xcode command-line tools' "$(xcode-select -p)"
    elif [ "$check" = 1 ]; then miss 'Xcode command-line tools' 'xcode-select --install'
    else
        installing 'Xcode command-line tools' 'a dialog opens: install, then run this script again'
        xcode-select --install || true
        exit 0
    fi
}

linux_base() {
    if have apt-get; then apt_packages "$@"
    else
        echo "  This is not a Debian-family Linux: install the equivalents of these packages, then run again: $*" >&2
        if [ "$check" = 1 ]; then missing=$((missing + 1)); fi
    fi
}

set_git_hooks() {
    [ -d .githooks ] || return 0
    if [ "$(git config core.hooksPath || true)" = .githooks ]; then ok 'git hooks' 'core.hooksPath .githooks'
    elif [ "$check" = 1 ]; then miss 'git hooks' 'core.hooksPath is not .githooks'
    else git config core.hooksPath .githooks; installing 'git hooks' 'core.hooksPath .githooks'; fi
}

# The extensions .vscode/extensions.json recommends, when VS Code's command line is on the PATH.
install_vscode_extensions() {
    have code && [ -f .vscode/extensions.json ] || return 0
    local installed
    installed=$(code --list-extensions 2>/dev/null || true)
    for ext in $(sed -e 's://.*$::' .vscode/extensions.json | tr -d '\n' | sed -e 's/.*"recommendations"[^[]*\[//' -e 's/\].*//' | tr -d '" ' | tr ',' ' '); do
        if grep -qixF "$ext" <<<"$installed"; then ok 'VS Code extension' "$ext"
        elif [ "$check" = 1 ]; then miss 'VS Code extension' "$ext"
        else installing 'VS Code extension' "$ext"; code --install-extension "$ext" --force >/dev/null; fi
    done
}

# ---------------------------------------------------------------------------------------------------------------------
# Go-specific helpers.

# A Go binary's own version ("1.26.9"), never a toolchain it would switch to: GOTOOLCHAIN=local for the question.
version_of_go() { { GOTOOLCHAIN=local "$1" version 2>/dev/null || true; } | sed -n 's/^go version go\([0-9][0-9.]*\).*/\1/p'; }
# Whether a version ("1.26.9") is of a line ("1.26").
in_line() { case "$1" in "$2" | "$2".*) return 0 ;; *) return 1 ;; esac; }

# ---------------------------------------------------------------------------------------------------------------------
echo "pawnstar-go: $([ "$check" = 1 ] && echo checking || echo 'setting up') the $os development environment"

case "$os" in
    Linux) goos=linux; linux_base build-essential curl git zip ;;
    Darwin) goos=darwin; macos_base ;;
    *) echo "unsupported system: $os (tools/setup-dev.ps1 is the Windows script)" >&2; exit 2 ;;
esac
case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) echo "unsupported machine: $(uname -m)" >&2; exit 2 ;;
esac

# The versions the repo pins: go.mod's line (1.26) and the golangci-lint line CI runs (v2.12).
go_line=$(sed -n 's/^go \([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' go.mod | head -1)
lint_line=$(awk '/golangci-lint-action@/ { f = 1 } f && /version:/ { sub(/.*version:[ \t]*v/, ""); sub(/[ \t\r]*$/, ""); print; exit }' .github/workflows/ci.yml)
if [ -z "$go_line" ] || [ -z "$lint_line" ]; then
    echo 'could not read the Go line from go.mod or the golangci-lint line from ci.yml' >&2; exit 1
fi

# Go: whatever go is on the PATH if it is go.mod's line, else the newest release of that line, in ~/.local/go.
go_root="$HOME/.local/go"
[ -x "$go_root/bin/go" ] && add_to_path "$go_root/bin"
go_version=""
have go && go_version=$(version_of_go "$(command -v go)")
if in_line "$go_version" "$go_line"; then ok 'Go' "go$go_version ($(command -v go))"
elif [ "$check" = 1 ]; then miss 'Go' "go$go_line.x${go_version:+ (found go$go_version, $(command -v go))}"
else
    # go.dev's index, one field a line: each archive's filename, then its sha256 a few lines on.
    best=$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' | awk -v prefix="go$go_line." -v suffix=".$goos-$arch.tar.gz" '
        /"filename":/ { name = $0; sub(/.*"filename": *"/, "", name); sub(/".*/, "", name); next }
        /"sha256":/ && name != "" {
            sum = $0; sub(/.*"sha256": *"/, "", sum); sub(/".*/, "", sum)
            if (index(name, prefix) == 1 && substr(name, length(name) - length(suffix) + 1) == suffix) {
                patch = substr(name, length(prefix) + 1, length(name) - length(prefix) - length(suffix))
                if (patch ~ /^[0-9]+$/) print patch, name, sum
            }
            name = ""
        }' | sort -n | tail -1)
    [ -n "$best" ] || { echo "go.dev lists no go$go_line.x for $goos-$arch" >&2; exit 1; }
    read -r _ file sum <<<"$best"
    installing 'Go' "$file from go.dev into $go_root"
    scratch=$(mktemp -d)
    fetch_verified "https://go.dev/dl/$file" "$sum" "$scratch/$file"
    rm -rf "$go_root"
    mkdir -p "$(dirname "$go_root")"
    tar -xzf "$scratch/$file" -C "$(dirname "$go_root")"
    rm -rf "$scratch"
    add_to_path "$go_root/bin"
    go_version=$(version_of_go "$go_root/bin/go")
    if ! in_line "$(version_of_go "$(command -v go)")" "$go_line"; then
        miss 'Go' "installed, but the go on the PATH is $(command -v go): put $go_root/bin ahead of it"
    fi
fi

if in_line "$go_version" "$go_line"; then
    # Where go install puts the tools: GOBIN, else the first GOPATH's bin (~/go/bin by default).
    tool_bin=$(go env GOBIN)
    [ -n "$tool_bin" ] || tool_bin="$(go env GOPATH | cut -d: -f1)/bin"
    [ -d "$tool_bin" ] && add_to_path "$tool_bin"

    # gopls and dlv, which VS Code's Go extension runs for the code model and the debugger. A release that needs a
    # newer Go to build (gopls 0.24 needs 1.27) has go install fetch that toolchain into the module cache for its own
    # build alone; the repo's go stays 1.26.
    for tool in gopls=golang.org/x/tools/gopls@latest dlv=github.com/go-delve/delve/cmd/dlv@latest; do
        name=${tool%%=*}
        package=${tool#*=}
        if have "$name"; then ok "$name" "$(command -v "$name")"
        elif [ "$check" = 1 ]; then miss "$name" "go install $package"
        else installing "$name" "go install $package"; go install "$package"; add_to_path "$tool_bin"; fi
    done

    # golangci-lint: the newest release of CI's line, from its GitHub release, checked against its checksums.txt.
    lint_version=""
    [ -x "$HOME/.local/bin/golangci-lint" ] && add_to_path "$HOME/.local/bin"
    have golangci-lint && lint_version=$({ golangci-lint version 2>/dev/null || true; } | sed -n 's/.*version v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -1)
    if in_line "$lint_version" "$lint_line"; then ok 'golangci-lint' "$lint_version ($(command -v golangci-lint))"
    elif [ "$check" = 1 ]; then miss 'golangci-lint' "v$lint_line.x${lint_version:+ (found $lint_version)}"
    else
        module=github.com/golangci/golangci-lint
        [ "${lint_line%%.*}" -ge 2 ] && module="$module/v${lint_line%%.*}"
        patch=$(curl -fsSL "https://proxy.golang.org/$module/@v/list" | awk -v p="v$lint_line." \
            'index($0, p) == 1 && substr($0, length(p) + 1) ~ /^[0-9]+$/ { print substr($0, length(p) + 1) }' | sort -n | tail -1)
        [ -n "$patch" ] || { echo "no golangci-lint v$lint_line.x is published" >&2; exit 1; }
        version="$lint_line.$patch"
        file="golangci-lint-$version-$goos-$arch.tar.gz"
        base="https://github.com/golangci/golangci-lint/releases/download/v$version"
        sum=$(curl -fsSL "$base/golangci-lint-$version-checksums.txt" | awk -v f="$file" '$2 == f { print $1 }')
        [ -n "$sum" ] || { echo "$file is not in the release's checksums.txt" >&2; exit 1; }
        installing 'golangci-lint' "$version from its GitHub release into $HOME/.local/bin"
        scratch=$(mktemp -d)
        fetch_verified "$base/$file" "$sum" "$scratch/$file"
        tar -xzf "$scratch/$file" -C "$scratch"
        mkdir -p "$HOME/.local/bin"
        cp "$scratch/golangci-lint-$version-$goos-$arch/golangci-lint" "$HOME/.local/bin/golangci-lint"
        chmod 755 "$HOME/.local/bin/golangci-lint"
        rm -rf "$scratch"
        add_to_path "$HOME/.local/bin"
    fi

    # cgo, for the race detector: on when go env says so and its C compiler (gcc, or Xcode's clang) exists.
    cc=$(go env CC)
    cgo=$(go env CGO_ENABLED)
    if [ "$cgo" = 1 ] && have "${cc%% *}"; then ok 'cgo (race detector)' "CC=$cc ($(command -v "${cc%% *}"))"
    else miss 'cgo (race detector)' "CGO_ENABLED=$cgo CC=$cc: a C compiler (build-essential; Xcode's command-line tools)"; fi
elif [ "$check" = 1 ]; then miss 'gopls, dlv, golangci-lint' 'after Go'
fi

set_git_hooks
install_vscode_extensions

if [ "$check" = 1 ]; then
    if [ "$missing" -gt 0 ]; then echo "$missing missing: run tools/setup-dev.sh (VS Code: the 'setup environment' task)."; exit 1; fi
    echo 'Everything is in place.'
else
    echo 'Done. Open a new terminal (or restart VS Code) for PATH changes to reach it, then: make test-short (make check for the full gate)'
fi
