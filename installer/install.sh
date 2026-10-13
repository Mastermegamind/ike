#!/usr/bin/env bash
# Ike installer for Ubuntu 22.04, 24.04 and 26.04 LTS.
# Run on a FRESH KVM VPS as root:
#   curl -sSL https://raw.githubusercontent.com/YOUR-ORG/ike/main/installer/install.sh | sudo bash
#
# Licensed under the GNU AGPL-3.0.

set -euo pipefail

PANEL_NAME="ike"
AGENT_NAME="ike-agent"
MCP_NAME="ike-mcp"
REPO="YOUR-ORG/ike"
SUPPORTED_UBUNTU="22.04 24.04 26.04"

PHP_VERSIONS="8.1 8.2 8.3 8.4"
INSTALL_MARIADB=1
FORCE=0
AGENT_BINARY=""
MCP_BINARY=""

LOG_FILE="/var/log/${PANEL_NAME}-install.log"
CONF_DIR="/etc/${PANEL_NAME}"
STATE_DIR="/var/lib/${PANEL_NAME}"
PANEL_LOG_DIR="/var/log/${PANEL_NAME}"
NGINX_SITES_DIR="/etc/nginx/${PANEL_NAME}/sites"
SSH_KEYS_DIR="/etc/ssh/${PANEL_NAME}/keys"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

# ---------------------------------------------------------------- helpers ----

if [ -t 1 ]; then
	C_RED=$'\e[31m'; C_GRN=$'\e[32m'; C_YEL=$'\e[33m'; C_BLD=$'\e[1m'; C_RST=$'\e[0m'
else
	C_RED=""; C_GRN=""; C_YEL=""; C_BLD=""; C_RST=""
fi

info()  { printf '%s==>%s %s\n' "$C_GRN" "$C_RST" "$*"; }
warn()  { printf '%sWARNING:%s %s\n' "$C_YEL" "$C_RST" "$*" >&2; }
die()   { printf '%sERROR:%s %s\n' "$C_RED" "$C_RST" "$*" >&2; exit 1; }

apt_install() {
	apt-get install -y -q --no-install-recommends "$@" >>"$LOG_FILE" 2>&1
}

# True if the package exists in the configured apt sources.
pkg_available() {
	apt-cache show "$1" >/dev/null 2>&1
}

usage() {
	cat <<EOF
Usage: install.sh [options]

  --php "8.3 8.4"        PHP versions to install (default: "${PHP_VERSIONS}")
  --no-mariadb           Do not install MariaDB
  --force                Install even if the server is not fresh
  --agent-binary PATH    Use a locally built ${AGENT_NAME} instead of downloading
  --mcp-binary PATH      Use a locally built ${MCP_NAME} instead of downloading
  -h, --help             Show this help
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--php) PHP_VERSIONS="${2:?--php needs a value}"; shift 2 ;;
		--no-mariadb) INSTALL_MARIADB=0; shift ;;
		--force) FORCE=1; shift ;;
		--agent-binary) AGENT_BINARY="${2:?}"; shift 2 ;;
		--mcp-binary) MCP_BINARY="${2:?}"; shift 2 ;;
		-h|--help) usage; exit 0 ;;
		*) usage; die "Unknown option: $1" ;;
	esac
done

for v in $PHP_VERSIONS; do
	[[ "$v" =~ ^[5-9]\.[0-9]$ ]] || die "Invalid PHP version: $v"
done

# ----------------------------------------------------------------- checks ----

check_system() {
	[ "$(id -u)" -eq 0 ] || die "Run this installer as root (sudo bash install.sh)."

	[ -r /etc/os-release ] || die "Cannot detect the operating system."
	# shellcheck disable=SC1091
	. /etc/os-release
	[ "${ID:-}" = "ubuntu" ] || die "Only Ubuntu is supported (found: ${PRETTY_NAME:-unknown})."
	case " $SUPPORTED_UBUNTU " in
		*" ${VERSION_ID:-} "*) ;;
		*) die "Ubuntu ${VERSION_ID:-?} is not supported. Use one of: ${SUPPORTED_UBUNTU}." ;;
	esac
	OS_VERSION="$VERSION_ID"
	OS_CODENAME="${VERSION_CODENAME:?missing VERSION_CODENAME}"

	case "$(uname -m)" in
		x86_64) ARCH="amd64" ;;
		aarch64|arm64) ARCH="arm64" ;;
		*) die "Unsupported CPU architecture: $(uname -m). Use x86_64 or arm64." ;;
	esac

	local virt="unknown"
	if command -v systemd-detect-virt >/dev/null 2>&1; then
		virt="$(systemd-detect-virt 2>/dev/null || true)"
	fi
	case "$virt" in
		openvz|lxc|lxc-libvirt|docker|podman|wsl|systemd-nspawn)
			die "Virtualization '$virt' is not supported. Use a KVM VPS (Docker, firewall and resource limits need a real kernel)." ;;
	esac

	local mem_mb
	mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
	if [ "$mem_mb" -lt 900 ]; then
		die "At least 1 GB RAM is required (found ${mem_mb} MB)."
	fi
	[ "$mem_mb" -lt 1900 ] && warn "Only ${mem_mb} MB RAM. Fine for a few sites, but use 2 GB+ if you plan to enable email."

	local free_gb
	free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
	[ "$free_gb" -ge 5 ] || die "At least 5 GB free disk space is required (found ${free_gb} GB)."

	if [ -e "$CONF_DIR/installed" ]; then
		info "Existing ${PANEL_NAME} install found; re-running as an upgrade."
		return
	fi
	if [ "$FORCE" -eq 0 ]; then
		local found=()
		local p
		for p in /usr/local/cpanel /usr/local/directadmin /usr/local/hestia /usr/local/vesta \
			/usr/local/CyberCP /www/server/panel /etc/webmin /usr/local/psa /data/coolify; do
			[ -e "$p" ] && found+=("$p")
		done
		for p in apache2 nginx mysql-server mariadb-server; do
			dpkg -s "$p" >/dev/null 2>&1 && found+=("package:$p")
		done
		if [ ${#found[@]} -gt 0 ]; then
			die "This server is not fresh (found: ${found[*]}). Use a new VPS, or pass --force if you are sure."
		fi
	fi
}

# ---------------------------------------------------------------- install ----

install_base() {
	info "Updating package lists"
	apt-get update -q >>"$LOG_FILE" 2>&1
	info "Installing base packages"
	apt_install ca-certificates curl gnupg software-properties-common lsb-release \
		ufw fail2ban unzip acl openssl cron logrotate
}

# Prefer nginx.org's stable repo; fall back to Ubuntu's nginx if it doesn't
# support this release yet (common for a few months after a new LTS).
install_nginx() {
	info "Installing Nginx"
	local key=/usr/share/keyrings/nginx-archive-keyring.gpg
	local url="https://nginx.org/packages/ubuntu"
	local source="ubuntu"
	if curl -fsS --max-time 15 "${url}/dists/${OS_CODENAME}/Release" -o /dev/null 2>>"$LOG_FILE" \
		&& curl -fsS --max-time 15 https://nginx.org/keys/nginx_signing.key | gpg --dearmor --yes -o "$key" 2>>"$LOG_FILE"; then
		echo "deb [signed-by=${key}] ${url} ${OS_CODENAME} nginx" >/etc/apt/sources.list.d/nginx.list
		printf 'Package: *\nPin: origin nginx.org\nPin-Priority: 900\n' >/etc/apt/preferences.d/99nginx
		apt-get update -q >>"$LOG_FILE" 2>&1
		source="nginx.org"
	else
		warn "nginx.org has no packages for Ubuntu ${OS_VERSION} yet; using Ubuntu's Nginx."
		rm -f /etc/apt/sources.list.d/nginx.list /etc/apt/preferences.d/99nginx
	fi
	apt_install nginx
	NGINX_SOURCE="$source"

	# Which user does nginx run as? nginx.org uses "nginx", Ubuntu uses "www-data".
	WEB_USER=$(awk '/^[[:space:]]*user[[:space:]]/ {gsub(";",""); print $2; exit}' /etc/nginx/nginx.conf)
	WEB_USER="${WEB_USER:-www-data}"
	WEB_GROUP=$(id -gn "$WEB_USER")

	mkdir -p "$NGINX_SITES_DIR"
	# conf.d/*.conf is included inside the http{} block by both Ubuntu and nginx.org configs.
	cat >/etc/nginx/conf.d/${PANEL_NAME}.conf <<EOF
# Managed by ${PANEL_NAME}
server_tokens off;
include ${NGINX_SITES_DIR}/*.conf;
EOF
	# Replace the stock default site with a catch-all that drops unknown hosts.
	rm -f /etc/nginx/sites-enabled/default /etc/nginx/conf.d/default.conf
	cat >/etc/nginx/conf.d/00-${PANEL_NAME}-default.conf <<'EOF'
# Requests for domains not hosted here are closed without a response.
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    return 444;
}
EOF
	nginx -t >>"$LOG_FILE" 2>&1 || die "Nginx configuration test failed. See $LOG_FILE"
	systemctl enable --now nginx >>"$LOG_FILE" 2>&1
	systemctl reload nginx
}

install_php() {
	info "Installing PHP ${PHP_VERSIONS}"
	if add-apt-repository -y ppa:ondrej/php >>"$LOG_FILE" 2>&1; then
		apt-get update -q >>"$LOG_FILE" 2>&1
	else
		warn "Could not add the ondrej/php PPA; only Ubuntu's own PHP version will be available."
	fi

	local installed=()
	local v ext
	for v in $PHP_VERSIONS; do
		if ! pkg_available "php${v}-fpm"; then
			warn "PHP ${v} is not available for Ubuntu ${OS_VERSION}; skipping."
			continue
		fi
		apt_install "php${v}-fpm" "php${v}-cli" "php${v}-common"
		# Extensions are installed one by one: some (e.g. opcache in PHP 8.5+) are built in.
		for ext in mysql curl gd mbstring xml zip intl bcmath opcache readline soap imagick redis; do
			if pkg_available "php${v}-${ext}"; then
				apt_install "php${v}-${ext}" || warn "php${v}-${ext} failed to install"
			fi
		done
		# The stock www pool must stay (php-fpm needs at least one), but make it idle-cheap.
		local www="/etc/php/${v}/fpm/pool.d/www.conf"
		if [ -f "$www" ]; then
			sed -i -E 's/^pm = .*/pm = ondemand/; s/^;?pm.process_idle_timeout = .*/pm.process_idle_timeout = 10s/' "$www"
		fi
		systemctl enable --now "php${v}-fpm" >>"$LOG_FILE" 2>&1
		systemctl restart "php${v}-fpm"
		installed+=("$v")
	done

	if [ ${#installed[@]} -eq 0 ]; then
		info "Falling back to Ubuntu's default PHP"
		apt_install php-fpm php-cli php-mysql php-curl php-gd php-mbstring php-xml php-zip php-intl
		v=$(find /etc/php -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort -V | tail -1)
		[ -n "$v" ] || die "PHP installation failed. See $LOG_FILE"
		systemctl enable --now "php${v}-fpm" >>"$LOG_FILE" 2>&1
		installed+=("$v")
	fi
	DEFAULT_PHP=$(printf '%s\n' "${installed[@]}" | sort -V | tail -1)
	PHP_INSTALLED="${installed[*]}"
}

install_mariadb() {
	[ "$INSTALL_MARIADB" -eq 1 ] || return 0
	info "Installing MariaDB"
	# Ubuntu's MariaDB is maintained for the whole LTS lifetime. root uses unix_socket auth
	# (no password; only the system root user can log in as the database root).
	apt_install mariadb-server mariadb-client
	systemctl enable --now mariadb >>"$LOG_FILE" 2>&1
	# Equivalent of mysql_secure_installation's non-interactive parts.
	mariadb >>"$LOG_FILE" 2>&1 <<'SQL'
DELETE FROM mysql.global_priv WHERE User='';
DELETE FROM mysql.global_priv WHERE User='root' AND Host NOT IN ('localhost','127.0.0.1','::1');
DROP DATABASE IF EXISTS test;
DELETE FROM mysql.db WHERE Db='test' OR Db='test\_%';
FLUSH PRIVILEGES;
SQL
}

# SSH access for hosting accounts:
#   ike-sftp group -> SFTP only, chrooted to the user's home
#   ike-ssh  group -> normal shell (trusted users)
# Keys are read from a root-owned file per user so users can't add keys themselves.
configure_ssh() {
	info "Configuring SSH/SFTP for hosting accounts"
	apt_install openssh-server
	getent group ike-sftp >/dev/null || groupadd --system ike-sftp
	getent group ike-ssh  >/dev/null || groupadd --system ike-ssh
	install -d -m 0755 -o root -g root "/etc/ssh/${PANEL_NAME}" "$SSH_KEYS_DIR"

	local cfg=/etc/ssh/sshd_config
	local backup
	backup="${cfg}.${PANEL_NAME}.bak.$(date +%s)"
	cp -a "$cfg" "$backup"

	# Our Match blocks go at the very END of sshd_config: anything after a Match line
	# belongs to that Match, so appending is the only placement that can't capture
	# the admin's own settings.
	sed -i "/^# BEGIN ${PANEL_NAME}/,/^# END ${PANEL_NAME}/d" "$cfg"
	cat >>"$cfg" <<EOF
# BEGIN ${PANEL_NAME} (managed, do not edit between these markers)
Match Group ike-sftp
    ChrootDirectory %h
    ForceCommand internal-sftp -d /web
    AuthorizedKeysFile ${SSH_KEYS_DIR}/%u
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTunnel no
Match Group ike-ssh
    AuthorizedKeysFile ${SSH_KEYS_DIR}/%u
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding no
    X11Forwarding no
    PermitTunnel no
# END ${PANEL_NAME}
EOF
	if ! sshd -t >>"$LOG_FILE" 2>&1; then
		cp -a "$backup" "$cfg"
		die "sshd rejected the new configuration; your original config was restored. See $LOG_FILE"
	fi
	# Ubuntu 22.10+ may use socket activation; reloading ssh.service is safe either way.
	systemctl reload ssh >>"$LOG_FILE" 2>&1 || systemctl reload sshd >>"$LOG_FILE" 2>&1 || true
}

configure_firewall() {
	info "Configuring firewall (ufw) and fail2ban"
	local ssh_ports
	ssh_ports=$(sshd -T 2>/dev/null | awk '$1=="port" {print $2}')
	ssh_ports="${ssh_ports:-22}"
	local p
	# Allow SSH before enabling the firewall so the current session is never cut off.
	for p in $ssh_ports; do
		ufw allow "${p}/tcp" comment 'SSH' >>"$LOG_FILE" 2>&1
	done
	{
		ufw allow 80/tcp comment 'HTTP'
		ufw allow 443/tcp comment 'HTTPS'
		ufw default deny incoming
		ufw default allow outgoing
		ufw --force enable
	} >>"$LOG_FILE" 2>&1

	cat >/etc/fail2ban/jail.d/${PANEL_NAME}.conf <<'EOF'
[sshd]
enabled  = true
maxretry = 5
findtime = 10m
bantime  = 1h
EOF
	systemctl enable --now fail2ban >>"$LOG_FILE" 2>&1
	systemctl restart fail2ban
}

fetch_binary() { # name dest local_path
	local name="$1" dest="$2" local_path="$3"
	if [ -n "$local_path" ]; then
		[ -f "$local_path" ] || die "$local_path not found"
		install -m 0755 "$local_path" "$dest"
		return
	fi
	local url="https://github.com/${REPO}/releases/latest/download/${name}-linux-${ARCH}"
	local tmp
	tmp=$(mktemp)
	curl -fsSL --max-time 120 "$url" -o "$tmp" 2>>"$LOG_FILE" \
		|| { rm -f "$tmp"; die "Could not download ${name} from ${url}. Build it and pass --agent-binary / --mcp-binary."; }
	if curl -fsSL --max-time 30 "${url}.sha256" -o "${tmp}.sha256" 2>>"$LOG_FILE"; then
		echo "$(cut -d' ' -f1 "${tmp}.sha256")  ${tmp}" | sha256sum -c - >>"$LOG_FILE" 2>&1 \
			|| { rm -f "$tmp" "${tmp}.sha256"; die "Checksum mismatch for ${name}."; }
		rm -f "${tmp}.sha256"
	else
		warn "No checksum published for ${name}; skipping verification."
	fi
	install -m 0755 "$tmp" "$dest"
	rm -f "$tmp"
}

install_agent() {
	info "Installing ${AGENT_NAME} and ${MCP_NAME}"
	getent group "$PANEL_NAME" >/dev/null || groupadd --system "$PANEL_NAME"
	install -d -m 0750 -o root -g root "$CONF_DIR"
	install -d -m 0700 -o root -g root "$STATE_DIR" "$STATE_DIR/sites"
	install -d -m 0750 -o root -g adm "$PANEL_LOG_DIR"

	fetch_binary "$AGENT_NAME" "/usr/local/bin/${AGENT_NAME}" "$AGENT_BINARY"
	fetch_binary "$MCP_NAME" "/usr/local/bin/${MCP_NAME}" "$MCP_BINARY"

	cat >"$CONF_DIR/agent.json" <<EOF
{
  "web_user": "${WEB_USER}",
  "web_group": "${WEB_GROUP}",
  "nginx_sites_dir": "${NGINX_SITES_DIR}",
  "home_base": "/home",
  "state_dir": "${STATE_DIR}/sites",
  "default_php": "${DEFAULT_PHP}"
}
EOF
	chmod 0640 "$CONF_DIR/agent.json"

	cat >/etc/systemd/system/${AGENT_NAME}.service <<EOF
[Unit]
Description=${PANEL_NAME} agent
After=network.target nginx.service

[Service]
Type=simple
ExecStart=/usr/local/bin/${AGENT_NAME} --config ${CONF_DIR}/agent.json --socket /run/${PANEL_NAME}/agent.sock
Restart=on-failure
RestartSec=3
RuntimeDirectory=${PANEL_NAME}
RuntimeDirectoryMode=0750
PrivateTmp=yes
ProtectKernelModules=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
ProtectClock=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF
	systemctl daemon-reload
	systemctl enable "${AGENT_NAME}" >>"$LOG_FILE" 2>&1
	systemctl restart "${AGENT_NAME}"

	local _
	for _ in $(seq 1 20); do
		[ -S "/run/${PANEL_NAME}/agent.sock" ] && break
		sleep 0.5
	done
	if ! curl -fsS --unix-socket "/run/${PANEL_NAME}/agent.sock" http://agent/v1/health >>"$LOG_FILE" 2>&1; then
		die "${AGENT_NAME} did not start. Check: journalctl -u ${AGENT_NAME}"
	fi
	echo "installed_at=$(date -u +%FT%TZ) ubuntu=${OS_VERSION} php=\"${PHP_INSTALLED}\" nginx=${NGINX_SOURCE}" >"$CONF_DIR/installed"
}

summary() {
	local ip
	ip=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')
	cat <<EOF

${C_BLD}${C_GRN}${PANEL_NAME} installed successfully${C_RST}

  Ubuntu        ${OS_VERSION} (${ARCH})
  Nginx         from ${NGINX_SOURCE}, runs as ${WEB_USER}
  PHP           ${PHP_INSTALLED} (default ${DEFAULT_PHP})
  MariaDB       $( [ "$INSTALL_MARIADB" -eq 1 ] && echo installed || echo skipped )
  Server IP     ${ip}
  Install log   ${LOG_FILE}

${C_BLD}Try it${C_RST} — create a site (point example.com's DNS A record to ${ip} first):

  curl --unix-socket /run/${PANEL_NAME}/agent.sock -X POST http://agent/v1/sites \\
       -d '{"domain":"example.com","user":"example"}'

${C_BLD}Connect an AI agent (MCP)${C_RST} — add this to your MCP client config on your own computer:

  {
    "mcpServers": {
      "${PANEL_NAME}": {
        "command": "ssh",
        "args": ["root@${ip}", "${MCP_NAME}"]
      }
    }
  }

  It starts read-only. To let the AI create sites and manage SSH access, use
  "args": ["root@${ip}", "${MCP_NAME}", "--allow-write"]. Every call is logged to
  ${PANEL_LOG_DIR}/mcp-audit.log.

The web panel is not part of this release yet.
EOF
}

main() {
	: >"$LOG_FILE"
	chmod 0600 "$LOG_FILE"
	check_system
	info "Installing ${PANEL_NAME} on Ubuntu ${OS_VERSION} (${OS_CODENAME}, ${ARCH})"
	install_base
	install_nginx
	install_php
	install_mariadb
	configure_ssh
	configure_firewall
	install_agent
	summary
}

main "$@"
