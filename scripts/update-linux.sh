#!/usr/bin/env bash
set -euo pipefail

GO_VERSION="1.25.8"
GITHUB_REPO="Chromatischer/Tether"
INSTALL_PREFIX="/usr/local"
BIN_DIR="${INSTALL_PREFIX}/bin"
SERVICE_NAME="tether"
SOURCE_MODE="release"
BRANCH="main"
REF_NAME=""
RESTART_SERVICE=1
SKIP_GO_INSTALL=0

usage() {
  cat <<USAGE
Usage: sudo scripts/update-linux.sh [options]

Updates an installed Tether by building from GitHub source.
By default, updates from the latest GitHub release.

Options:
  --release                 Update from the latest GitHub release. Default.
  --tag TAG                 Update from a specific release tag.
  --branch BRANCH           Update from the latest commit on a branch.
  --repo OWNER/REPO         GitHub repository. Default: ${GITHUB_REPO}
  --prefix DIR              Install binaries under DIR/bin. Default: /usr/local
  --service NAME            systemd service name. Default: tether
  --skip-go-install         Do not install Go even if Go ${GO_VERSION}+ is missing.
  --no-restart              Do not restart the service after installing binaries.
  -h, --help                Show this help.
USAGE
}

log() {
  printf '==> %s\n' "$*"
}

warn() {
  printf 'WARN: %s\n' "$*" >&2
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      SOURCE_MODE="release"
      REF_NAME=""
      shift
      ;;
    --tag)
      [ "$#" -ge 2 ] || die "--tag requires a value"
      SOURCE_MODE="tag"
      REF_NAME="$2"
      shift 2
      ;;
    --branch)
      [ "$#" -ge 2 ] || die "--branch requires a value"
      SOURCE_MODE="branch"
      BRANCH="$2"
      shift 2
      ;;
    --repo)
      [ "$#" -ge 2 ] || die "--repo requires a value"
      GITHUB_REPO="$2"
      shift 2
      ;;
    --prefix)
      [ "$#" -ge 2 ] || die "--prefix requires a value"
      INSTALL_PREFIX="${2%/}"
      BIN_DIR="${INSTALL_PREFIX}/bin"
      shift 2
      ;;
    --service)
      [ "$#" -ge 2 ] || die "--service requires a value"
      SERVICE_NAME="$2"
      shift 2
      ;;
    --skip-go-install)
      SKIP_GO_INSTALL=1
      shift
      ;;
    --no-restart)
      RESTART_SERVICE=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[ "$(uname -s)" = "Linux" ] || die "this updater only supports Linux"

if [ "${EUID}" -ne 0 ]; then
  die "run this updater as root, for example: sudo scripts/update-linux.sh"
fi

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

need_cmd() {
  have_cmd "$1" || die "$1 is required"
}

version_ge() {
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

go_is_sufficient() {
  if ! have_cmd go; then
    return 1
  fi
  local found
  found="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  [ -n "${found}" ] || return 1
  version_ge "${found}" "${GO_VERSION}"
}

go_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    aarch64|arm64) printf 'arm64' ;;
    armv6l) printf 'armv6l' ;;
    armv7l) printf 'armv6l' ;;
    i386|i686) printf '386' ;;
    *) die "unsupported CPU architecture for Go installer: $(uname -m)" ;;
  esac
}

install_go() {
  if go_is_sufficient; then
    log "Using existing Go $(go env GOVERSION)"
    return 0
  fi

  [ "${SKIP_GO_INSTALL}" -eq 0 ] || die "Go ${GO_VERSION}+ is required and --skip-go-install was set"
  need_cmd curl
  need_cmd tar

  local arch tarball url tmp
  arch="$(go_arch)"
  tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
  url="https://go.dev/dl/${tarball}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' EXIT

  log "Installing Go ${GO_VERSION} to /usr/local/go"
  curl -fsSL "${url}" -o "${tmp}/${tarball}"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${tmp}/${tarball}"
  export PATH="/usr/local/go/bin:${PATH}"
  go_is_sufficient || die "installed Go but could not verify Go ${GO_VERSION}+"
}

latest_release_tag() {
  local api body tag
  api="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"
  body="$(curl -fsSL "${api}")"
  tag="$(printf '%s\n' "${body}" | sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "${tag}" ] || die "could not determine latest release tag for ${GITHUB_REPO}"
  printf '%s' "${tag}"
}

source_url() {
  case "${SOURCE_MODE}" in
    release)
      REF_NAME="$(latest_release_tag)"
      printf 'https://github.com/%s/archive/refs/tags/%s.tar.gz' "${GITHUB_REPO}" "${REF_NAME}"
      ;;
    tag)
      [ -n "${REF_NAME}" ] || die "tag is empty"
      printf 'https://github.com/%s/archive/refs/tags/%s.tar.gz' "${GITHUB_REPO}" "${REF_NAME}"
      ;;
    branch)
      REF_NAME="${BRANCH}"
      printf 'https://github.com/%s/archive/refs/heads/%s.tar.gz' "${GITHUB_REPO}" "${BRANCH}"
      ;;
    *)
      die "unknown source mode: ${SOURCE_MODE}"
      ;;
  esac
}

service_exists() {
  have_cmd systemctl && systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1
}

build_from_archive() {
  local tmp archive url src
  tmp="$(mktemp -d)"
  archive="${tmp}/tether-source.tar.gz"
  url="$(source_url)"

  log "Downloading ${SOURCE_MODE} ${REF_NAME} from ${GITHUB_REPO}"
  curl -fL "${url}" -o "${archive}"
  tar -xzf "${archive}" -C "${tmp}"
  src="$(find "${tmp}" -mindepth 1 -maxdepth 1 -type d | head -n1)"
  [ -n "${src}" ] || die "source archive did not contain a directory"

  log "Building Tether"
  (
    cd "${src}"
    go mod download
    mkdir -p "${tmp}/bin"
    go build -trimpath -o "${tmp}/bin/tether" ./cmd/tether
    go build -trimpath -o "${tmp}/bin/tether-backup" ./cmd/tether-backup
    go build -trimpath -o "${tmp}/bin/tether-keygen" ./cmd/tether-keygen
    go build -trimpath -o "${tmp}/bin/tether-passhash" ./cmd/tether-passhash
  )

  mkdir -p "${BIN_DIR}"
  if service_exists && [ "${RESTART_SERVICE}" -eq 1 ]; then
    log "Stopping ${SERVICE_NAME}.service"
    systemctl stop "${SERVICE_NAME}.service"
  fi

  log "Installing binaries to ${BIN_DIR}"
  install -m 0755 "${tmp}/bin/tether" "${BIN_DIR}/tether"
  install -m 0755 "${tmp}/bin/tether-backup" "${BIN_DIR}/tether-backup"
  install -m 0755 "${tmp}/bin/tether-keygen" "${BIN_DIR}/tether-keygen"
  install -m 0755 "${tmp}/bin/tether-passhash" "${BIN_DIR}/tether-passhash"

  if service_exists && [ "${RESTART_SERVICE}" -eq 1 ]; then
    log "Starting ${SERVICE_NAME}.service"
    systemctl start "${SERVICE_NAME}.service"
  fi

  rm -rf "${tmp}"
}

need_cmd curl
need_cmd tar
need_cmd gzip
install_go
build_from_archive

log "Tether updated from ${SOURCE_MODE} ${REF_NAME}"
