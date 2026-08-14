#!/usr/bin/env bash
set -euo pipefail

readonly REPO="vi-dev/nem-cli"
readonly BINARY="nem"
readonly DEFAULT_INSTALL_DIR="${HOME}/.local/bin"

# ANSI colors — only when stdout is a terminal
if [[ -t 1 ]]; then
  _RED='\033[0;31m' _GREEN='\033[0;32m' _YELLOW='\033[1;33m' _BLUE='\033[0;34m' _NC='\033[0m'
else
  _RED='' _GREEN='' _YELLOW='' _BLUE='' _NC=''
fi

info()    { printf "${_BLUE}==>${_NC} %s\n" "$*"; }
success() { printf "${_GREEN}==>${_NC} %s\n" "$*"; }
warn()    { printf "${_YELLOW}==>${_NC} %s\n" "$*" >&2; }
error()   { printf "${_RED}Error:${_NC} %s\n" "$*" >&2; exit 1; }

TMPDIR_WORK=""
cleanup() {
  if [[ -n "${TMPDIR_WORK}" && -d "${TMPDIR_WORK}" ]]; then
    rm -rf "${TMPDIR_WORK}"
  fi
}

detect_os() {
  local os
  os="$(uname -s)"
  case "${os}" in
    Linux*)  echo "linux"  ;;
    Darwin*) echo "darwin" ;;
    *)       error "Unsupported OS: ${os}. Supported: Linux, Darwin." ;;
  esac
}

detect_arch() {
  local arch
  arch="$(uname -m)"
  case "${arch}" in
    x86_64)        echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)             error "Unsupported architecture: ${arch}. Supported: x86_64, arm64/aarch64." ;;
  esac
}

detect_checksum_tool() {
  if command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    echo "shasum"
  else
    error "No checksum tool found. Install sha256sum (Linux) or shasum (macOS)."
  fi
}

detect_download_tool() {
  if command -v curl >/dev/null 2>&1; then
    echo "curl"
  elif command -v wget >/dev/null 2>&1; then
    echo "wget"
  else
    error "No download tool found. Install curl or wget."
  fi
}

api_fetch() {
  local url="$1" tool="$2"
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    case "${tool}" in
      curl) curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "${url}" ;;
      wget) wget -qO- --header="Authorization: Bearer ${GITHUB_TOKEN}" "${url}" ;;
    esac
  else
    case "${tool}" in
      curl) curl -fsSL "${url}" ;;
      wget) wget -qO- "${url}" ;;
    esac
  fi
}

resolve_version() {
  local dl_tool="$1"
  local version="${NEM_VERSION:-}"
  if [[ -n "${version}" ]]; then
    echo "${version}"
    return
  fi
  info "Fetching latest release..." >&2
  version="$(api_fetch "https://api.github.com/repos/${REPO}/releases/latest" "${dl_tool}" \
             | grep '"tag_name"' \
             | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  [[ -n "${version}" ]] || error "Failed to resolve version from GitHub API."
  echo "${version}"
}

# Release assets embed the version verbatim: the tag `v1.2.3` for a release, and
# the literal `unstable` for the rolling main build — so one formula covers both.
archive_name() {
  local version="$1" os="$2" arch="$3"
  echo "${BINARY}_${version}_${os}_${arch}.tar.gz"
}

# The rolling `unstable` release is replaced on every merge to main, so an
# install from it is not reproducible.
warn_if_unstable() {
  local version="$1"
  [[ "${version}" == "unstable" ]] || return 0
  warn "Installing the unstable build from main — not a released version."
}

do_download() {
  local url="$1" dest="$2" tool="$3"
  case "${tool}" in
    curl) curl -fsSL --progress-bar -o "${dest}" "${url}" ;;
    wget) wget -q --show-progress -O "${dest}" "${url}" ;;
  esac
}

verify_checksum() {
  local archive="$1" checksums_file="$2" tool="$3"
  local filename actual expected
  filename="$(basename "${archive}")"
  expected="$(awk -v f="${filename}" '$2 == f {print $1}' "${checksums_file}")"
  [[ -n "${expected}" ]] || error "No checksum entry for '${filename}' in checksums.txt."
  case "${tool}" in
    sha256sum) actual="$(sha256sum "${archive}" | awk '{print $1}')" ;;
    shasum)    actual="$(shasum -a 256 "${archive}" | awk '{print $1}')" ;;
  esac
  if [[ "${expected}" != "${actual}" ]]; then
    error "Checksum mismatch for '${filename}'.
  expected: ${expected}
  actual:   ${actual}"
  fi
}

main() {
  local install_dir="${NEM_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"

  local os arch dl_tool cs_tool
  os="$(detect_os)"
  arch="$(detect_arch)"
  dl_tool="$(detect_download_tool)"
  cs_tool="$(detect_checksum_tool)"

  local version
  version="$(resolve_version "${dl_tool}")"
  warn_if_unstable "${version}"

  local archive_file
  archive_file="$(archive_name "${version}" "${os}" "${arch}")"
  local base_url="https://github.com/${REPO}/releases/download/${version}"

  TMPDIR_WORK="$(mktemp -d)"
  local archive="${TMPDIR_WORK}/${archive_file}"
  local checksums="${TMPDIR_WORK}/checksums.txt"

  info "Installing ${BINARY} ${version} for ${os}/${arch}..."

  info "Downloading archive..."
  do_download "${base_url}/${archive_file}" "${archive}" "${dl_tool}"

  info "Downloading checksums..."
  do_download "${base_url}/checksums.txt" "${checksums}" "${dl_tool}"

  info "Verifying checksum..."
  verify_checksum "${archive}" "${checksums}" "${cs_tool}"

  info "Extracting binary..."
  local dir_name="${archive_file%.tar.gz}"
  tar -xzf "${archive}" -C "${TMPDIR_WORK}"

  mkdir -p "${install_dir}" || error "Cannot create install directory '${install_dir}'. Try: NEM_INSTALL_DIR=~/.local/bin or run with sudo."
  if [[ ! -w "${install_dir}" ]]; then
    error "Install directory '${install_dir}' is not writable. Try: NEM_INSTALL_DIR=~/.local/bin or run with sudo."
  fi

  # GoReleaser may place the binary at the archive root or inside a directory
  # named after the archive; accept either.
  if [[ -f "${TMPDIR_WORK}/${dir_name}/${BINARY}" ]]; then
    mv "${TMPDIR_WORK}/${dir_name}/${BINARY}" "${install_dir}/${BINARY}"
  elif [[ -f "${TMPDIR_WORK}/${BINARY}" ]]; then
    mv "${TMPDIR_WORK}/${BINARY}" "${install_dir}/${BINARY}"
  else
    error "Could not find '${BINARY}' binary in the downloaded archive."
  fi
  chmod +x "${install_dir}/${BINARY}"

  success "${BINARY} ${version} installed to ${install_dir}/${BINARY}"

  if ! printf '%s' "${PATH}" | tr ':' '\n' | grep -Fxq -- "${install_dir}"; then
    warn "'${install_dir}' is not in your PATH."
    warn "Add to your shell profile: export PATH=\"${install_dir}:\$PATH\""
  fi
}

# Guard so `source install.sh` exposes the functions without running main —
# this is what lets test/install.bats exercise them in isolation.
if [[ "${BASH_SOURCE[0]:-${0}}" == "${0}" ]]; then
  trap cleanup EXIT
  main "$@"
fi
