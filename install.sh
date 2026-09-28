#!/usr/bin/env sh
# Install a released spproof binary. Intended for a machine without Go:
#
#   curl -sSfL https://raw.githubusercontent.com/arhuman/spproof/main/install.sh | sh
#
# Go developers should prefer `go install github.com/arhuman/spproof/cmd/spproof@latest`,
# which needs no script at all.
#
# Environment:
#   VERSION      tag to install (default: the latest release)
#   INSTALL_DIR  where to put the binary (default: $HOME/.local/bin)
#   NO_VERIFY    set to 1 to skip checksum verification (not recommended)
set -eu

REPO="arhuman/spproof"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

die() {
	echo "install.sh: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

need curl
need tar
need mktemp

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
linux | darwin) ;;
*) die "unsupported OS: $os (releases cover linux and darwin; on Windows download the .zip from the releases page)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch="amd64" ;;
arm64 | aarch64) arch="arm64" ;;
*) die "unsupported architecture: $(uname -m) (releases cover amd64 and arm64)" ;;
esac

# The tag is resolved from the API rather than guessed, and the redirect on
# /releases/latest is not followed blindly: an empty answer must fail loudly
# instead of building a URL containing an empty version.
version="${VERSION:-}"
if [ -z "$version" ]; then
	version="$(curl -sSfL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$version" ] || die "could not resolve the latest release tag; pass VERSION=vX.Y.Z"
fi

# goreleaser strips the leading v from the archive name but keeps it in the tag,
# so both spellings are needed: the tag addresses the release, the number names
# the file inside it.
version_num="${version#v}"
archive="spproof_${version_num}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp="$(mktemp -d)"
# Cleared on every exit path, including a failed download, so a partial archive
# is never left behind in /tmp.
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Installing spproof $version for $os/$arch"

curl -sSfL "$base/$archive" -o "$tmp/$archive" ||
	die "download failed: $base/$archive (does that release provide $os/$arch?)"

# Verify against the release's checksums.txt. This script is meant to be piped
# into a shell, so an unverified binary would be the weakest link in the chain:
# every release publishes checksums, and skipping the check needs to be a
# deliberate NO_VERIFY=1 rather than the default.
if [ "${NO_VERIFY:-0}" = "1" ]; then
	echo "warning: skipping checksum verification (NO_VERIFY=1)" >&2
elif curl -sSfL "$base/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
	if command -v sha256sum >/dev/null 2>&1; then
		sum="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
	elif command -v shasum >/dev/null 2>&1; then
		sum="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
	else
		die "neither sha256sum nor shasum found; re-run with NO_VERIFY=1 to bypass"
	fi
	want="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1 | head -n 1)"
	[ -n "$want" ] || die "$archive is absent from checksums.txt"
	[ "$sum" = "$want" ] || die "checksum mismatch for $archive: got $sum, want $want"
	echo "Checksum verified"
else
	die "could not fetch checksums.txt; re-run with NO_VERIFY=1 to bypass"
fi

tar -xzf "$tmp/$archive" -C "$tmp" || die "could not extract $archive"
[ -f "$tmp/spproof" ] || die "the archive did not contain a spproof binary"

mkdir -p "$INSTALL_DIR" || die "could not create $INSTALL_DIR"
install -m 0755 "$tmp/spproof" "$INSTALL_DIR/spproof" ||
	die "could not write to $INSTALL_DIR (set INSTALL_DIR to a writable path)"

echo "Installed $INSTALL_DIR/spproof"

# A binary outside PATH looks like a failed install, so say so rather than
# leaving the caller to discover it.
case ":$PATH:" in
*":$INSTALL_DIR:"*) "$INSTALL_DIR/spproof" version 2>/dev/null || true ;;
*) echo "note: $INSTALL_DIR is not on PATH; add it with: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
