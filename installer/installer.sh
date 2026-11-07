#!/usr/bin/env bash
# Ike one-line installer.
#
#   curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash
#
# With choices (skips the guided wizard):
#
#   curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- --web hybrid --php all --node "20 22"
#
# This script is small on purpose. It checks the server, downloads the full
# installer (install.sh) from an Ike GitHub release, verifies its SHA-256
# checksum, and runs it. Everything it does is in install.sh, which you can
# read first: https://github.com/Mastermegamind/ike/blob/main/installer/install.sh
#
# Environment:
#   IKE_REPO     GitHub owner/name to install from   (default Mastermegamind/ike)
#   IKE_VERSION  release tag such as v0.3.0          (default latest)
#
# SPDX-License-Identifier: AGPL-3.0-or-later

# Everything lives in main(), called on the last line, so a download that is
# cut off halfway can never run half a script.
main() {
	set -euo pipefail
	local repo="${IKE_REPO:-Mastermegamind/ike}" version="${IKE_VERSION:-latest}" base tmp want got

	say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
	die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

	[ "$(id -u)" -eq 0 ] || die "Run as root: curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash"
	[ "$(uname -s)" = "Linux" ] || die "Ike runs on Linux servers."
	case "$(uname -m)" in
		x86_64 | amd64 | aarch64 | arm64) ;;
		*) die "This CPU ($(uname -m)) isn't supported. Ike runs on x86_64 and arm64." ;;
	esac
	[ -r /etc/os-release ] || die "Cannot detect the operating system."
	# shellcheck disable=SC1091
	. /etc/os-release
	[ "${ID:-}" = "ubuntu" ] || die "Ike needs Ubuntu 22.04, 24.04 or 26.04. This server runs ${PRETTY_NAME:-an unknown system}."
	case "${VERSION_ID:-}" in
		22.04 | 24.04 | 26.04) ;;
		*) die "Ubuntu ${VERSION_ID:-?} isn't supported. Use 22.04, 24.04 or 26.04." ;;
	esac
	case "$repo" in
		*[!A-Za-z0-9._/-]* | */*/* | /* | */ ) die "IKE_REPO must look like owner/name." ;;
		*/*) ;;
		*) die "IKE_REPO must look like owner/name." ;;
	esac
	case "$version" in
		latest) base="https://github.com/${repo}/releases/latest/download" ;;
		v[0-9]*) base="https://github.com/${repo}/releases/download/${version}" ;;
		*) die "IKE_VERSION must be latest or a tag like v0.3.0." ;;
	esac

	if ! command -v curl >/dev/null 2>&1; then
		say "Installing curl"
		DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates >/dev/null
	fi

	tmp=$(mktemp -d)
	# Expanded now on purpose: $tmp is local and gone by the time the trap runs.
	# shellcheck disable=SC2064
	trap "rm -rf '${tmp}'" EXIT
	say "Downloading the Ike installer (${repo}, ${version})"
	curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "${tmp}/install.sh" "${base}/install.sh" \
		|| die "Could not download ${base}/install.sh. Check the server can reach github.com."
	curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "${tmp}/install.sh.sha256" "${base}/install.sh.sha256" \
		|| die "Could not download the installer's checksum, so it can't be verified."
	want=$(cut -d' ' -f1 "${tmp}/install.sh.sha256")
	got=$(sha256sum "${tmp}/install.sh" | cut -d' ' -f1)
	if [ -z "$want" ] || [ "$want" != "$got" ]; then
		die "Checksum mismatch for install.sh (expected ${want:-nothing}, got ${got}). Nothing was installed."
	fi
	say "Checksum verified"

	say "Starting the installer"
	# When piped from curl, stdin is the script itself. Hand the installer the
	# terminal instead, so the guided wizard can ask its questions.
	if [ $# -eq 0 ] && { : </dev/tty; } 2>/dev/null; then
		IKE_REPO="$repo" IKE_VERSION="$version" bash "${tmp}/install.sh" --wizard </dev/tty
	else
		IKE_REPO="$repo" IKE_VERSION="$version" bash "${tmp}/install.sh" "$@"
	fi
}

main "$@"
