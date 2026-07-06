#!/usr/bin/env bash
set -euo pipefail

GO_VERSION="1.25.8"
INSTALL_PREFIX="/usr/local"
BIN_DIR="${INSTALL_PREFIX}/bin"
CONFIG_DIR="/etc/tether"
DATA_DIR="/var/lib/tether"
SYSTEMD_DIR="/etc/systemd/system"
SERVICE_NAME="tether"
SERVICE_USER="tether"
SERVICE_GROUP="tether"
LISTEN_ADDR=":2222"
OPENROUTER_API_KEY="${OPENROUTER_API_KEY:-}"
PORTAL_PASSWORD=""
SKIP_DEPS=0
SKIP_GO_INSTALL=0
ENABLE_SERVICE=1
START_SERVICE=1

usage() {
  cat <<USAGE
Usage: sudo scripts/install-linux.sh [options]

Installs Tether from this source checkout onto a Linux host.

Options:
  --openrouter-api-key KEY   Write OPENROUTER_API_KEY to /etc/tether/tether.env.
  --portal-password PASS     Set the SSH portal password. If omitted, a random one is generated.
  --listen ADDR              SSH portal listen address. Default: :2222
  --prefix DIR               Install binaries under DIR/bin. Default: /usr/local
  --config-dir DIR           Tether config directory. Default: /etc/tether
  --data-dir DIR             Tether data directory. Default: /var/lib/tether
  --skip-deps                Do not install OS packages.
  --skip-go-install          Do not install Go even if Go ${GO_VERSION}+ is missing.
  --no-enable                Do not enable the systemd service.
  --no-start                 Do not start/restart the systemd service.
  -h, --help                 Show this help.
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
    --openrouter-api-key)
      [ "$#" -ge 2 ] || die "--openrouter-api-key requires a value"
      OPENROUTER_API_KEY="$2"
      shift 2
      ;;
    --portal-password)
      [ "$#" -ge 2 ] || die "--portal-password requires a value"
      PORTAL_PASSWORD="$2"
      shift 2
      ;;
    --listen)
      [ "$#" -ge 2 ] || die "--listen requires a value"
      LISTEN_ADDR="$2"
      shift 2
      ;;
    --prefix)
      [ "$#" -ge 2 ] || die "--prefix requires a value"
      INSTALL_PREFIX="${2%/}"
      BIN_DIR="${INSTALL_PREFIX}/bin"
      shift 2
      ;;
    --config-dir)
      [ "$#" -ge 2 ] || die "--config-dir requires a value"
      CONFIG_DIR="${2%/}"
      shift 2
      ;;
    --data-dir)
      [ "$#" -ge 2 ] || die "--data-dir requires a value"
      DATA_DIR="${2%/}"
      shift 2
      ;;
    --skip-deps)
      SKIP_DEPS=1
      shift
      ;;
    --skip-go-install)
      SKIP_GO_INSTALL=1
      shift
      ;;
    --no-enable)
      ENABLE_SERVICE=0
      shift
      ;;
    --no-start)
      START_SERVICE=0
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

[ "$(uname -s)" = "Linux" ] || die "this installer only supports Linux"

if [ "${EUID}" -ne 0 ]; then
  die "run this installer as root, for example: sudo scripts/install-linux.sh"
fi

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_DIR}"

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

install_packages() {
  [ "${SKIP_DEPS}" -eq 0 ] || return 0

  log "Installing OS packages"
  if have_cmd apt-get; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates curl tar gzip git openssh-client bubblewrap sudo
  elif have_cmd dnf; then
    dnf install -y ca-certificates curl tar gzip git openssh-clients bubblewrap sudo
  elif have_cmd yum; then
    yum install -y ca-certificates curl tar gzip git openssh-clients bubblewrap sudo
  elif have_cmd pacman; then
    pacman -Sy --needed --noconfirm ca-certificates curl tar gzip git openssh bubblewrap sudo
  elif have_cmd zypper; then
    zypper --non-interactive install ca-certificates curl tar gzip git openssh bubblewrap sudo
  elif have_cmd apk; then
    apk add --no-cache bash ca-certificates curl tar gzip git openssh-client bubblewrap sudo
  else
    warn "No supported package manager found. Install ca-certificates curl tar gzip git openssh-client bubblewrap and sudo yourself."
  fi
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
  have_cmd curl || die "curl is required to install Go"
  have_cmd tar || die "tar is required to install Go"

  local arch tarball url tmp
  arch="$(go_arch)"
  tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
  url="https://go.dev/dl/${tarball}"
  tmp="$(mktemp -d)"
  trap "rm -rf '${tmp}'" EXIT

  log "Installing Go ${GO_VERSION} to /usr/local/go"
  curl -fsSL "${url}" -o "${tmp}/${tarball}"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${tmp}/${tarball}"
  export PATH="/usr/local/go/bin:${PATH}"
  go_is_sufficient || die "installed Go but could not verify Go ${GO_VERSION}+"
}

random_password() {
  if have_cmd openssl; then
    openssl rand -base64 24 | tr -d '\n'
  else
    od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

ensure_user() {
  if getent group "${SERVICE_GROUP}" >/dev/null; then
    :
  else
    groupadd --system "${SERVICE_GROUP}"
  fi

  if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    :
  else
    useradd --system --gid "${SERVICE_GROUP}" --home-dir "${DATA_DIR}" --shell /usr/sbin/nologin "${SERVICE_USER}"
  fi
}

build_binaries() {
  log "Building Tether binaries"
  mkdir -p "${BIN_DIR}"
  go mod download
  go build -trimpath -o "${BIN_DIR}/tether" ./cmd/tether
  go build -trimpath -o "${BIN_DIR}/tether-backup" ./cmd/tether-backup
  go build -trimpath -o "${BIN_DIR}/tether-keygen" ./cmd/tether-keygen
  go build -trimpath -o "${BIN_DIR}/tether-passhash" ./cmd/tether-passhash
  chmod 0755 "${BIN_DIR}/tether" "${BIN_DIR}/tether-backup" "${BIN_DIR}/tether-keygen" "${BIN_DIR}/tether-passhash"
  install -m 0755 "${REPO_DIR}/scripts/update-linux.sh" "${BIN_DIR}/tether-update"
  cat >"${BIN_DIR}/tether-restart" <<EOF
#!/usr/bin/env bash
set -euo pipefail
systemctl restart ${SERVICE_NAME}.service
EOF
  chmod 0755 "${BIN_DIR}/tether-restart"
}

install_config() {
  log "Installing config and runtime directories"
  mkdir -p "${CONFIG_DIR}" "${DATA_DIR}" "${DATA_DIR}/admin"
  chown root:"${SERVICE_GROUP}" "${CONFIG_DIR}"
  chmod 0750 "${CONFIG_DIR}"
  chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${DATA_DIR}"
  chmod 0750 "${DATA_DIR}"

  if [ ! -f "${CONFIG_DIR}/ssh_host_ed25519" ]; then
    ssh-keygen -t ed25519 -N "" -f "${CONFIG_DIR}/ssh_host_ed25519" >/dev/null
  fi
  chown root:"${SERVICE_GROUP}" "${CONFIG_DIR}/ssh_host_ed25519" "${CONFIG_DIR}/ssh_host_ed25519.pub"
  chmod 0640 "${CONFIG_DIR}/ssh_host_ed25519"
  chmod 0644 "${CONFIG_DIR}/ssh_host_ed25519.pub"

  if [ -z "${PORTAL_PASSWORD}" ]; then
    PORTAL_PASSWORD="$(random_password)"
    GENERATED_PORTAL_PASSWORD=1
  else
    GENERATED_PORTAL_PASSWORD=0
  fi

  local password_hash master_key
  password_hash="$("${BIN_DIR}/tether-passhash" "${PORTAL_PASSWORD}")"
  master_key="$("${BIN_DIR}/tether-keygen")"

  if [ ! -f "${CONFIG_DIR}/tether.yaml" ]; then
    cat >"${CONFIG_DIR}/tether.yaml" <<EOF
log_level: info

db:
  path: ${DATA_DIR}/tether.sqlite

openrouter:
  api_key: ""
  base_url: "https://openrouter.ai/api/v1"
  model: "z-ai/glm-5.1"
  provider:
    ignore: ["morph"]

secrets:
  master_key: ""
  ttl_hours: 24

signal:
  enabled: false
  account_number: ""
  signal_cli_path: "signal-cli"
  http_addr: "127.0.0.1:17800"

discord:
  enabled: false
  bot_token: ""

paths:
  data_dir: ${DATA_DIR}

ssh:
  listen_addr: "${LISTEN_ADDR}"
  host_key_path: ${CONFIG_DIR}/ssh_host_ed25519
  portal_password_hash: "${password_hash}"
  authorized_keys_path: ${CONFIG_DIR}/authorized_keys
EOF
  else
    warn "${CONFIG_DIR}/tether.yaml already exists; leaving it unchanged"
  fi
  chown root:"${SERVICE_GROUP}" "${CONFIG_DIR}/tether.yaml"
  chmod 0640 "${CONFIG_DIR}/tether.yaml"

  if [ ! -f "${CONFIG_DIR}/authorized_keys" ]; then
    : >"${CONFIG_DIR}/authorized_keys"
  fi
  chown root:"${SERVICE_GROUP}" "${CONFIG_DIR}/authorized_keys"
  chmod 0640 "${CONFIG_DIR}/authorized_keys"

  if [ ! -f "${CONFIG_DIR}/tether.env" ]; then
    cat >"${CONFIG_DIR}/tether.env" <<EOF
OPENROUTER_API_KEY=${OPENROUTER_API_KEY}
TETHER_MASTER_KEY=${master_key}
# TETHER_SIGNAL_NUMBER=
# TETHER_DISCORD_BOT_TOKEN=
EOF
  else
    warn "${CONFIG_DIR}/tether.env already exists; leaving it unchanged"
  fi
  chown root:"${SERVICE_GROUP}" "${CONFIG_DIR}/tether.env"
  chmod 0640 "${CONFIG_DIR}/tether.env"
}

install_systemd_unit() {
  if ! have_cmd systemctl; then
    warn "systemd not found; installed binaries and config, but no service was installed"
    return 0
  fi

  log "Installing systemd service"
  cat >"${SYSTEMD_DIR}/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Tether SSH AI assistant
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
EnvironmentFile=-${CONFIG_DIR}/tether.env
WorkingDirectory=${DATA_DIR}
ExecStart=${BIN_DIR}/tether -config ${CONFIG_DIR}/tether.yaml
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  [ "${ENABLE_SERVICE}" -eq 0 ] || systemctl enable "${SERVICE_NAME}.service"
  [ "${START_SERVICE}" -eq 0 ] || systemctl restart "${SERVICE_NAME}.service"
}

install_update_sudoers() {
  if ! have_cmd sudo; then
    warn "sudo not found; in-app updates will need /admin update command set to a root-capable updater command"
    return 0
  fiwhen saving in settings change the Button text to Saved with a check symbol when saved correctly and without issues.
  local sudoers
  sudoers="/etc/sudoers.d/tether-update"
  log "Installing sudo rule for in-app updates"
  cat >"${sudoers}" <<EOF
${SERVICE_USER} ALL=(root) NOPASSWD: ${BIN_DIR}/tether-update, ${BIN_DIR}/tether-update *
${SERVICE_USER} ALL=(root) NOPASSWD: ${BIN_DIR}/tether-restart
EOF
  chmod 0440 "${sudoers}"
  if have_cmd visudo; then
    visudo -cf "${sudoers}" >/dev/null
  fi
}

install_packages
install_go
ensure_user
build_binaries
install_config
install_systemd_unit
install_update_sudoers

log "Tether installed"
printf 'Binaries: %s\n' "${BIN_DIR}"
printf 'Updater:  %s/tether-update\n' "${BIN_DIR}"
printf 'Config:   %s/tether.yaml\n' "${CONFIG_DIR}"
printf 'Env:      %s/tether.env\n' "${CONFIG_DIR}"
printf 'Data:     %s\n' "${DATA_DIR}"
printf 'Service:  %s.service\n' "${SERVICE_NAME}"

if [ -z "${OPENROUTER_API_KEY}" ]; then
  warn "OPENROUTER_API_KEY is empty. Edit ${CONFIG_DIR}/tether.env, then run: systemctl restart ${SERVICE_NAME}"
fi

if [ "${GENERATED_PORTAL_PASSWORD:-0}" -eq 1 ]; then
  printf '\nGenerated SSH portal password: %s\n' "${PORTAL_PASSWORD}"
  printf 'Store it now, then change it by rerunning tether-passhash and editing %s/tether.yaml.\n' "${CONFIG_DIR}"
fi

printf '\nConnect with: ssh -p %s <portal-user>@<host>\n' "${LISTEN_ADDR##*:}"
