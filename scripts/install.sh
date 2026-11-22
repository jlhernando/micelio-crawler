#!/bin/sh
# Fresh public installations only. Never overwrite another Micelio binary.
set -eu

fail() { printf '%s\n' "Micelio install: $*" >&2; exit 1; }
usage() { printf '%s\n' 'Usage: sh install.sh [--version v1.16.0] [--install-dir DIRECTORY]'; }

version=''
install_dir="${HOME:?HOME is required}/.local/bin"
explicit_dir=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--install-dir)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "Missing value for $1"
            if [ "$1" = '--version' ]; then version=$2; else install_dir=$2; explicit_dir=true; fi
            shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; fail "Unknown argument: $1" ;;
    esac
done
case "$install_dir" in /*) ;; *) fail 'Use an absolute installation directory.' ;; esac
target="$install_dir/micelio"
[ ! -e "$target" ] && [ ! -L "$target" ] || fail "$target already exists; nothing changed."
existing=$(command -v micelio 2>/dev/null || true)
if [ -n "$existing" ]; then
    printf '%s\n' "Another Micelio exists at $existing; it will remain untouched." >&2
    "$explicit_dir" || fail 'Choose a separate directory with --install-dir to keep both builds.'
fi
case "$(uname -s)" in Darwin) platform=darwin ;; Linux) platform=linux ;; *) fail 'Supported systems: macOS and Linux.' ;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) fail 'Supported processors: amd64 and arm64.' ;; esac
for tool in curl awk mktemp link; do command -v "$tool" >/dev/null 2>&1 || fail "$tool is required."; done
if command -v sha256sum >/dev/null 2>&1; then hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then hash_tool=shasum
else fail 'sha256sum or shasum is required.'; fi

repo='https://github.com/jlhernando/micelio-crawler'
download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 300 "$@"
}
if [ -z "$version" ]; then
    resolved=$(download --output /dev/null --write-out '%{url_effective}' "$repo/releases/latest")
    case "$resolved" in "$repo/releases/tag/"*) version=${resolved##*/} ;; *) fail 'Could not resolve an exact release.' ;; esac
fi
case "$version" in v[0-9]*) ;; *) fail 'Version must start with v and a digit.' ;; esac
case "$version" in *[!a-zA-Z0-9.+-]*) fail 'Invalid version.' ;; esac
asset="micelio-$platform-$arch"
mkdir -p "$install_dir"
stage=$(mktemp -d "$install_dir/.micelio-install.XXXXXX")
trap 'rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM
download --output "$stage/$asset" "$repo/releases/download/$version/$asset"
download --output "$stage/checksums.txt" "$repo/releases/download/$version/checksums.txt"
expected=$(awk -v name="$asset" '
    { sub(/\r$/, ""); if ($2 == name || $2 == "*" name) {
        if (NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-fA-F]/) invalid=1;
        digest=$1; count++
    }}
    END { if (count != 1 || invalid) exit 1; print tolower(digest) }
' "$stage/checksums.txt") || fail 'Missing, duplicate or invalid checksum entry.'
if [ "$hash_tool" = sha256sum ]; then actual=$(sha256sum "$stage/$asset"); else actual=$(shasum -a 256 "$stage/$asset"); fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; nothing installed.'
chmod 755 "$stage/$asset"
# link(1), unlike ln(1), treats the destination literally, even if it became
# a directory. It atomically fails if any destination appeared meanwhile.
link "$stage/$asset" "$target" || fail 'Destination exists or cannot be created; nothing replaced.'
printf 'Installed public Micelio %s at %s\n' "$version" "$target"
printf 'Start the dashboard with the full path, or add this directory to PATH yourself:\n"%s" ui\n' "$target"
if [ "$platform" = darwin ]; then printf '%s\n' 'macOS downloads are unsigned. See docs/INSTALL.md if Gatekeeper blocks launch.'; fi
