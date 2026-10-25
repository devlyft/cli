#!/bin/sh
# Install the DevLyft CLI on Linux or macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/devlyft/cli/main/install.sh | sh
#
# The script downloads a release archive, checks it against the release's
# sha256 checksum file and, when cosign is installed, verifies the keyless
# signature on that checksum file before installing anything. It never uses
# sudo.
#
# Environment variables:
#   DEVLYFT_VERSION            version to install, e.g. 0.3.1 or v0.3.1
#                              (default: the latest release)
#   DEVLYFT_INSTALL_DIR        where to put the binary (default: /usr/local/bin
#                              if writable, otherwise ~/.local/bin)
#   DEVLYFT_REQUIRE_SIGNATURE  set to 1 to fail when the signature cannot be
#                              verified (for example cosign is missing)
#   DEVLYFT_DOWNLOAD_BASE      URL of the directory holding the release assets,
#                              for mirrors and testing (requires DEVLYFT_VERSION)

set -eu

REPO="devlyft/cli"
CERT_IDENTITY_REGEXP='^https://github\.com/devlyft/cli/\.github/workflows/release\.yml@refs/tags/v'
OIDC_ISSUER="https://token.actions.githubusercontent.com"

say() { printf '%s\n' "$*" >&2; }
warn() { printf 'warning: %s\n' "$*" >&2; }
fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

has() { command -v "$1" >/dev/null 2>&1; }

download() {
	# download URL DEST
	if has curl; then
		curl -fsSL --proto '=https,file' --tlsv1.2 -o "$2" "$1"
	elif has wget; then
		wget -q --https-only -O "$2" "$1"
	else
		fail "curl or wget is required"
	fi
}

latest_version() {
	has curl || fail "set DEVLYFT_VERSION, or install curl to find the latest release"
	url=$(curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
		fail "could not look up the latest release"
	tag=${url##*/}
	case $tag in
	v[0-9]*) printf '%s\n' "${tag#v}" ;;
	*) fail "could not find the latest release (got $url)" ;;
	esac
}

detect_os() {
	case $(uname -s) in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	MINGW* | MSYS* | CYGWIN*) fail "on Windows, install with Scoop or download the zip from https://github.com/$REPO/releases" ;;
	*) fail "unsupported operating system: $(uname -s)" ;;
	esac
}

detect_arch() {
	case $(uname -m) in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
	esac
}

sha256() {
	if has sha256sum; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif has shasum; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		fail "sha256sum or shasum is required to verify the download"
	fi
}

verify_signature() {
	# verify_signature DIR CHECKSUMS BASE
	if ! has cosign; then
		if [ "${DEVLYFT_REQUIRE_SIGNATURE:-}" = 1 ]; then
			fail "cosign is not installed and DEVLYFT_REQUIRE_SIGNATURE=1; install cosign (https://docs.sigstore.dev/cosign/system_config/installation/)"
		fi
		warn "cosign is not installed: checked the sha256 checksum but not the release signature"
		return 0
	fi
	if ! download "$3/$2.sigstore.json" "$1/$2.sigstore.json"; then
		fail "could not download the signature bundle $2.sigstore.json"
	fi
	cosign verify-blob \
		--bundle "$1/$2.sigstore.json" \
		--certificate-identity-regexp "$CERT_IDENTITY_REGEXP" \
		--certificate-oidc-issuer "$OIDC_ISSUER" \
		"$1/$2" >/dev/null 2>&1 ||
		fail "signature verification failed for $2: do not use this download"
	say "Verified the release signature."
}

choose_install_dir() {
	if [ -n "${DEVLYFT_INSTALL_DIR:-}" ]; then
		echo "$DEVLYFT_INSTALL_DIR"
	elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		echo /usr/local/bin
	else
		echo "$HOME/.local/bin"
	fi
}

main() {
	os=$(detect_os)
	arch=$(detect_arch)

	version=${DEVLYFT_VERSION:-}
	if [ -n "${DEVLYFT_DOWNLOAD_BASE:-}" ] && [ -z "$version" ]; then
		fail "DEVLYFT_DOWNLOAD_BASE requires DEVLYFT_VERSION"
	fi
	if [ -z "$version" ]; then
		version=$(latest_version)
	fi
	version=${version#v}

	base=${DEVLYFT_DOWNLOAD_BASE:-"https://github.com/$REPO/releases/download/v$version"}
	archive="devlyft_${version}_${os}_${arch}.tar.gz"
	checksums="devlyft_${version}_checksums.txt"

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t devlyft)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading devlyft $version for $os/$arch..."
	download "$base/$archive" "$tmp/$archive" || fail "could not download $base/$archive"
	download "$base/$checksums" "$tmp/$checksums" || fail "could not download $base/$checksums"

	verify_signature "$tmp" "$checksums" "$base"

	want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$tmp/$checksums")
	[ -n "$want" ] || fail "$archive is not listed in $checksums"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || fail "checksum mismatch for $archive: expected $want, got $got"

	tar -xzf "$tmp/$archive" -C "$tmp" devlyft || fail "could not extract $archive"

	dir=$(choose_install_dir)
	mkdir -p "$dir" || fail "could not create $dir; set DEVLYFT_INSTALL_DIR to a writable directory"
	[ -w "$dir" ] || fail "$dir is not writable; set DEVLYFT_INSTALL_DIR, or rerun with the permissions to write there"
	# Install via a temporary name and rename, so a running devlyft is never
	# left half-written.
	cp "$tmp/devlyft" "$dir/.devlyft.tmp.$$"
	chmod 0755 "$dir/.devlyft.tmp.$$"
	mv -f "$dir/.devlyft.tmp.$$" "$dir/devlyft"

	say "Installed $("$dir/devlyft" version) to $dir/devlyft"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) warn "$dir is not on your PATH; add it, for example: export PATH=\"$dir:\$PATH\"" ;;
	esac
}

main "$@"
