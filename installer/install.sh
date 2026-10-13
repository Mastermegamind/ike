#!/usr/bin/env bash
# Ike installer for Ubuntu 22.04, 24.04 and 26.04 LTS.
# Run on a FRESH KVM VPS as root:
#   curl -sSL https://raw.githubusercontent.com/YOUR-ORG/ike/main/installer/install.sh | sudo bash
#
# Every component is the server admin's choice; run with --help for options.
# Licensed under the GNU AGPL-3.0.

set -euo pipefail

PANEL_NAME="ike"
AGENT_NAME="ike-agent"
MCP_NAME="ike-mcp"
REPO="${IKE_REPO:-YOUR-ORG/ike}"          # override with IKE_REPO=owner/name
IKE_VERSION="${IKE_VERSION:-latest}"     # or a tag such as v0.3.0
SUPPORTED_UBUNTU="22.04 24.04 26.04"
ALL_PHP="5.6 7.0 7.1 7.2 7.3 7.4 8.0 8.1 8.2 8.3 8.4 8.5"

# ---- admin choices (defaults) ----
WEB_STACK="nginx"           # nginx | apache | hybrid
DEFAULT_WEB_SERVER=""       # hybrid only: nginx | nginx+apache
PHP_VERSIONS="8.2 8.3 8.4 8.5"
DEFAULT_PHP=""
NODE_VERSIONS="22"
PYTHON_VERSIONS="3.12"
PMA_CHANNEL="stable"        # stable | 6 | none
INSTALL_MARIADB=1
FORCE=0
AGENT_BINARY=""
MCP_BINARY=""
PANEL_BINARY=""
ADMIN_USER="admin"
ADMIN_EMAIL=""
WIZARD=0
PANEL_PORTS="2083 2086 2087"

LOG_FILE="/var/log/${PANEL_NAME}-install.log"
CONF_DIR="/etc/${PANEL_NAME}"
STATE_DIR="/var/lib/${PANEL_NAME}"
PANEL_LOG_DIR="/var/log/${PANEL_NAME}"
OPT_DIR="/opt/${PANEL_NAME}"
RUNTIMES_DIR="${OPT_DIR}/runtimes"
NGINX_SITES_DIR="/etc/nginx/${PANEL_NAME}/sites"
APACHE_SITES_DIR="/etc/apache2/${PANEL_NAME}/sites"
SSH_KEYS_DIR="/etc/ssh/${PANEL_NAME}/keys"
PMA_DIR="/usr/share/${PANEL_NAME}/phpmyadmin"
PMA_PORT=8090

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

# download URL DEST [SHA256]  — fails on HTTP errors and checksum mismatch.
download() {
	curl -fsSL --retry 3 --max-time 600 "$1" -o "$2" 2>>"$LOG_FILE" || return 1
	if [ -n "${3:-}" ]; then
		echo "${3}  ${2}" | sha256sum -c - >>"$LOG_FILE" 2>&1 || { rm -f "$2"; die "Checksum mismatch for $1"; }
	fi
}

usage() {
	cat <<EOF
Usage: install.sh [options]

Web server
  --web nginx|apache|hybrid   nginx: Nginx only (default)
                              apache: Apache only
                              hybrid: Nginx in front; each website picks Nginx or Nginx+Apache (.htaccess)
  --default-web-server X      hybrid only: "nginx" or "nginx+apache" for new websites

Languages
  --php "7.4 8.3"             PHP versions, or "all" for ${ALL_PHP}
                              (default: "${PHP_VERSIONS}")
  --default-php 8.3           PHP version for new websites (default: newest installed)
  --node "20 22 24"           Node.js major versions, or "none" (default: "${NODE_VERSIONS}")
  --python "3.12 3.13"        Python versions, or "none" (default: "${PYTHON_VERSIONS}")

Databases
  --no-mariadb                Do not install MariaDB
  --phpmyadmin stable|6|none  phpMyAdmin channel (default: stable; "6" = 6.0 development snapshot)

Other
  --force                     Install even if the server is not fresh
  --agent-binary PATH         Use a locally built ${AGENT_NAME}
  --mcp-binary PATH           Use a locally built ${MCP_NAME}
  --panel-binary PATH         Use a locally built ike-panel
  --admin-user NAME           First panel admin (default: admin)
  --admin-email EMAIL         First panel admin's email
  --wizard                    Ask for every choice step by step (default when run with no options in a terminal)
  -h, --help                  Show this help
EOF
}

if [ $# -eq 0 ] && [ -t 0 ] && [ -t 1 ]; then WIZARD=1; fi

while [ $# -gt 0 ]; do
	case "$1" in
		--web) WEB_STACK="${2:?}"; shift 2 ;;
		--default-web-server) DEFAULT_WEB_SERVER="${2:?}"; shift 2 ;;
		--php) PHP_VERSIONS="${2:?--php needs a value}"; shift 2 ;;
		--default-php) DEFAULT_PHP="${2:?}"; shift 2 ;;
		--node) NODE_VERSIONS="${2:?}"; shift 2 ;;
		--python) PYTHON_VERSIONS="${2:?}"; shift 2 ;;
		--no-mariadb) INSTALL_MARIADB=0; shift ;;
		--phpmyadmin) PMA_CHANNEL="${2:?}"; shift 2 ;;
		--force) FORCE=1; shift ;;
		--agent-binary) AGENT_BINARY="${2:?}"; shift 2 ;;
		--mcp-binary) MCP_BINARY="${2:?}"; shift 2 ;;
		--panel-binary) PANEL_BINARY="${2:?}"; shift 2 ;;
		--admin-user) ADMIN_USER="${2:?}"; shift 2 ;;
		--admin-email) ADMIN_EMAIL="${2:?}"; shift 2 ;;
		--wizard) WIZARD=1; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage; die "Unknown option: $1" ;;
	esac
done

# ---------------------------------------------------------------- wizard ----

ask() { # ask "Question" default  ->  sets REPLY
	local q="$1" def="$2"
	printf '%s%s%s [%s]: ' "$C_BLD" "$q" "$C_RST" "$def" >&2
	read -r REPLY </dev/tty || REPLY=""
	REPLY="${REPLY:-$def}"
}

run_wizard() {
	printf '\n%sIke guided install%s\nPress Enter to accept the suggestion in [brackets].\n\n' "$C_BLD" "$C_RST" >&2
	echo "Web server: nginx (fastest), apache (Apache only), hybrid (Nginx in front, Apache per website for .htaccess)" >&2
	ask "Web server" "$WEB_STACK"; WEB_STACK="$REPLY"
	if [ "$WEB_STACK" = "hybrid" ]; then
		ask "New websites use (nginx or nginx+apache)" "nginx+apache"; DEFAULT_WEB_SERVER="$REPLY"
	fi
	echo "PHP versions separated by spaces, or 'all' for ${ALL_PHP}" >&2
	ask "PHP versions" "$PHP_VERSIONS"; PHP_VERSIONS="$REPLY"
	ask "Node.js versions (or none)" "$NODE_VERSIONS"; NODE_VERSIONS="$REPLY"
	ask "Python versions (or none)" "$PYTHON_VERSIONS"; PYTHON_VERSIONS="$REPLY"
	ask "Install MariaDB (yes/no)" "yes"
	if [ "$REPLY" = "no" ]; then INSTALL_MARIADB=0; fi
	if [ "$INSTALL_MARIADB" -eq 1 ]; then
		ask "phpMyAdmin (stable, 6 for the 6.0 preview, none)" "$PMA_CHANNEL"; PMA_CHANNEL="$REPLY"
	fi
	ask "First panel admin username" "$ADMIN_USER"; ADMIN_USER="$REPLY"
	ask "Admin email" "${ADMIN_EMAIL:-admin@$(hostname -f 2>/dev/null || hostname)}"; ADMIN_EMAIL="$REPLY"
	ask "Install now? (yes/no)" "yes"
	[ "$REPLY" = "yes" ] || die "Cancelled. Nothing was changed."
}

if [ "$WIZARD" -eq 1 ]; then run_wizard; fi

[ "$PHP_VERSIONS" = "all" ] && PHP_VERSIONS="$ALL_PHP"
[ "$NODE_VERSIONS" = "none" ] && NODE_VERSIONS=""
[ "$PYTHON_VERSIONS" = "none" ] && PYTHON_VERSIONS=""

case "$WEB_STACK" in nginx|apache|hybrid) ;; *) die "--web must be nginx, apache or hybrid" ;; esac
case "$DEFAULT_WEB_SERVER" in
	"") [ "$WEB_STACK" = "hybrid" ] && DEFAULT_WEB_SERVER="nginx+apache" ;;
	nginx|nginx+apache) [ "$WEB_STACK" = "hybrid" ] || die "--default-web-server only applies to --web hybrid" ;;
	*) die "--default-web-server must be nginx or nginx+apache" ;;
esac
case "$PMA_CHANNEL" in stable|6|none) ;; *) die "--phpmyadmin must be stable, 6 or none" ;; esac
[ "$INSTALL_MARIADB" -eq 0 ] && PMA_CHANNEL="none"

for v in $PHP_VERSIONS; do [[ "$v" =~ ^[5-9]\.[0-9]$ ]] || die "Invalid PHP version: $v"; done
for v in $NODE_VERSIONS; do [[ "$v" =~ ^[1-9][0-9]?$ ]] || die "Invalid Node.js version: $v (use the major version, e.g. 22)"; done
for v in $PYTHON_VERSIONS; do [[ "$v" =~ ^3\.[0-9]{1,2}$ ]] || die "Invalid Python version: $v"; done
[ -z "$DEFAULT_PHP" ] || [[ "$DEFAULT_PHP" =~ ^[5-9]\.[0-9]$ ]] || die "Invalid --default-php"

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
		x86_64) ARCH="amd64"; NODE_ARCH="x64"; UV_ARCH="x86_64" ;;
		aarch64|arm64) ARCH="arm64"; NODE_ARCH="arm64"; UV_ARCH="aarch64" ;;
		*) die "Unsupported CPU architecture: $(uname -m). Use x86_64 or arm64." ;;
	esac

	local virt="unknown"
	if command -v systemd-detect-virt >/dev/null 2>&1; then
		virt="$(systemd-detect-virt 2>/dev/null || true)"
	fi
	case "$virt" in
		openvz|lxc|lxc-libvirt|docker|podman|wsl|systemd-nspawn)
			die "Virtualization '$virt' is not supported. Use a KVM VPS (firewall and resource limits need a real kernel)." ;;
	esac

	local mem_mb
	mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
	[ "$mem_mb" -ge 900 ] || die "At least 1 GB RAM is required (found ${mem_mb} MB)."
	if [ "$mem_mb" -lt 1900 ]; then
		warn "Only ${mem_mb} MB RAM. Fine for a few sites; use 2 GB+ for email or many PHP versions."
	fi

	local free_gb
	free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
	[ "$free_gb" -ge 5 ] || die "At least 5 GB free disk space is required (found ${free_gb} GB)."

	if [ -e "$CONF_DIR/installed" ]; then
		info "Existing ${PANEL_NAME} install found; re-running as an upgrade."
		return
	fi
	if [ "$FORCE" -eq 0 ]; then
		local found=() p
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
		ufw fail2ban unzip xz-utils acl openssl cron logrotate python3 rsync unattended-upgrades quota
	systemctl enable --now cron >>"$LOG_FILE" 2>&1
}

# Prefer nginx.org's stable repo; fall back to Ubuntu's nginx if it doesn't
# support this release yet.
install_nginx() {
	[ "$WEB_STACK" = "apache" ] && return 0
	info "Installing Nginx"
	local key=/usr/share/keyrings/nginx-archive-keyring.gpg url="https://nginx.org/packages/ubuntu"
	NGINX_SOURCE="ubuntu"
	if curl -fsS --max-time 15 "${url}/dists/${OS_CODENAME}/Release" -o /dev/null 2>>"$LOG_FILE" \
		&& curl -fsS --max-time 15 https://nginx.org/keys/nginx_signing.key | gpg --dearmor --yes -o "$key" 2>>"$LOG_FILE"; then
		echo "deb [signed-by=${key}] ${url} ${OS_CODENAME} nginx" >/etc/apt/sources.list.d/nginx.list
		printf 'Package: *\nPin: origin nginx.org\nPin-Priority: 900\n' >/etc/apt/preferences.d/99nginx
		apt-get update -q >>"$LOG_FILE" 2>&1
		NGINX_SOURCE="nginx.org"
	else
		warn "nginx.org has no packages for Ubuntu ${OS_VERSION} yet; using Ubuntu's Nginx."
		rm -f /etc/apt/sources.list.d/nginx.list /etc/apt/preferences.d/99nginx
	fi
	apt_install nginx

	mkdir -p "$NGINX_SITES_DIR"
	# conf.d/*.conf is included inside http{} by both Ubuntu's and nginx.org's nginx.conf.
	cat >/etc/nginx/conf.d/${PANEL_NAME}.conf <<EOF
# Managed by ${PANEL_NAME}
server_tokens off;
map \$http_upgrade \$connection_upgrade_ike {
    default upgrade;
    ''      close;
}
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

install_apache() {
	[ "$WEB_STACK" = "nginx" ] && return 0
	info "Installing Apache (${WEB_STACK} mode)"
	apt_install apache2
	a2dismod -q php* mpm_prefork >>"$LOG_FILE" 2>&1 || true
	a2enmod -q mpm_event proxy proxy_fcgi proxy_http proxy_wstunnel rewrite headers remoteip setenvif alias >>"$LOG_FILE" 2>&1
	a2dissite -q 000-default >>"$LOG_FILE" 2>&1 || true
	mkdir -p "$APACHE_SITES_DIR"

	if [ "$WEB_STACK" = "hybrid" ]; then
		APACHE_LISTEN="127.0.0.1:8080"
		echo "Listen 127.0.0.1:8080" >/etc/apache2/ports.conf
	else
		APACHE_LISTEN="*:80"
		echo "Listen 80" >/etc/apache2/ports.conf
	fi
	cat >/etc/apache2/conf-available/${PANEL_NAME}.conf <<EOF
# Managed by ${PANEL_NAME}
ServerTokens Prod
ServerSignature Off
$( [ "$WEB_STACK" = "hybrid" ] && printf 'RemoteIPHeader X-Real-IP\nRemoteIPInternalProxy 127.0.0.1\n' )
<Directory ${CONF_DIR}/pages>
    Require all granted
</Directory>
IncludeOptional ${APACHE_SITES_DIR}/*.conf
EOF
	a2enconf -q ${PANEL_NAME} >>"$LOG_FILE" 2>&1
	apache2ctl configtest >>"$LOG_FILE" 2>&1 || die "Apache configuration test failed. See $LOG_FILE"
	systemctl enable --now apache2 >>"$LOG_FILE" 2>&1
	systemctl restart apache2
}

# Which user/group serves files? Nginx decides when it is installed, else Apache.
detect_web_user() {
	if [ "$WEB_STACK" = "apache" ]; then
		WEB_USER="www-data"
	else
		WEB_USER=$(awk '/^[[:space:]]*user[[:space:]]/ {gsub(";",""); print $2; exit}' /etc/nginx/nginx.conf)
		WEB_USER="${WEB_USER:-www-data}"
	fi
	WEB_GROUP=$(id -gn "$WEB_USER")
	# In hybrid mode Apache (www-data) must also read website files.
	if [ "$WEB_STACK" = "hybrid" ] && [ "$WEB_GROUP" != "www-data" ]; then
		usermod -aG "$WEB_GROUP" www-data
	fi
}

install_php() {
	info "Installing PHP ${PHP_VERSIONS}"
	if add-apt-repository -y ppa:ondrej/php >>"$LOG_FILE" 2>&1; then
		apt-get update -q >>"$LOG_FILE" 2>&1
	else
		warn "Could not add the ondrej/php PPA; only Ubuntu's own PHP version will be available."
	fi

	local installed=() skipped=() v ext
	for v in $PHP_VERSIONS; do
		if ! pkg_available "php${v}-fpm"; then
			skipped+=("$v")
			continue
		fi
		apt_install "php${v}-fpm" "php${v}-cli" "php${v}-common"
		# One by one: some extensions are built in on newer PHP (e.g. opcache in 8.5).
		for ext in mysql curl gd mbstring xml zip intl bcmath opcache readline soap imagick redis; do
			if pkg_available "php${v}-${ext}"; then
				apt_install "php${v}-${ext}" || warn "php${v}-${ext} failed to install"
			fi
		done
		# The stock www pool must stay (php-fpm needs one), but make it idle-cheap.
		local www="/etc/php/${v}/fpm/pool.d/www.conf"
		[ -f "$www" ] && sed -i -E 's/^pm = .*/pm = ondemand/; s/^;?pm.process_idle_timeout = .*/pm.process_idle_timeout = 10s/' "$www"
		systemctl enable --now "php${v}-fpm" >>"$LOG_FILE" 2>&1
		systemctl restart "php${v}-fpm"
		installed+=("$v")
	done
	[ ${#skipped[@]} -gt 0 ] && warn "PHP ${skipped[*]} not available for Ubuntu ${OS_VERSION}; skipped."

	if [ ${#installed[@]} -eq 0 ]; then
		info "Falling back to Ubuntu's default PHP"
		apt_install php-fpm php-cli php-mysql php-curl php-gd php-mbstring php-xml php-zip php-intl
		v=$(find /etc/php -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort -V | tail -1)
		[ -n "$v" ] || die "PHP installation failed. See $LOG_FILE"
		systemctl enable --now "php${v}-fpm" >>"$LOG_FILE" 2>&1
		installed+=("$v")
	fi
	PHP_INSTALLED="${installed[*]}"
	if [ -n "$DEFAULT_PHP" ] && [[ " $PHP_INSTALLED " == *" $DEFAULT_PHP "* ]]; then
		:
	else
		[ -n "$DEFAULT_PHP" ] && warn "PHP $DEFAULT_PHP is not installed; using the newest version as default."
		DEFAULT_PHP=$(printf '%s\n' "${installed[@]}" | sort -V | tail -1)
	fi
}

# WP-CLI powers the one-click WordPress installer.
install_wpcli() {
	info "Installing WP-CLI"
	local base="https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar" tmp
	tmp=$(mktemp -d)
	if download "${base}/wp-cli.phar.sha512" "${tmp}/sha" \
		&& curl -fsSL --max-time 300 "${base}/wp-cli.phar" -o "${tmp}/wp" 2>>"$LOG_FILE" \
		&& echo "$(cut -d' ' -f1 "${tmp}/sha")  ${tmp}/wp" | sha512sum -c - >>"$LOG_FILE" 2>&1; then
		mkdir -p "${OPT_DIR}/bin"
		install -m 0755 "${tmp}/wp" "${OPT_DIR}/bin/wp"
	else
		warn "Could not download WP-CLI; the WordPress installer will be unavailable."
	fi
	rm -rf "$tmp"
}

# Node.js: official builds from nodejs.org, checksum-verified, one folder per major.
install_node() {
	[ -n "$NODE_VERSIONS" ] || return 0
	info "Installing Node.js ${NODE_VERSIONS}"
	local index tmp major ver file sums
	index=$(mktemp)
	download https://nodejs.org/dist/index.json "$index" || { warn "Could not reach nodejs.org; skipping Node.js."; return 0; }
	for major in $NODE_VERSIONS; do
		ver=$(python3 -c 'import json,sys; m=sys.argv[2]; print(next((r["version"] for r in json.load(open(sys.argv[1])) if r["version"].startswith("v"+m+".")), ""))' "$index" "$major")
		if [ -z "$ver" ]; then
			warn "Node.js ${major} not found; skipping."
			continue
		fi
		file="node-${ver}-linux-${NODE_ARCH}.tar.xz"
		tmp=$(mktemp -d)
		sums="${tmp}/SHASUMS256.txt"
		download "https://nodejs.org/dist/${ver}/SHASUMS256.txt" "$sums" || { warn "No checksums for Node.js ${ver}; skipping."; continue; }
		download "https://nodejs.org/dist/${ver}/${file}" "${tmp}/${file}" "$(awk -v f="$file" '$2==f {print $1}' "$sums")" \
			|| { warn "Download of Node.js ${ver} failed; skipping."; continue; }
		rm -rf "${RUNTIMES_DIR}/node/${major}"
		mkdir -p "${RUNTIMES_DIR}/node/${major}"
		tar -xJf "${tmp}/${file}" -C "${RUNTIMES_DIR}/node/${major}" --strip-components=1 --no-same-owner
		rm -rf "$tmp"
		NODE_INSTALLED="${NODE_INSTALLED:-} ${major}(${ver})"
	done
	rm -f "$index"
}

# Python: standalone builds managed by uv (works the same on every Ubuntu release).
install_python() {
	[ -n "$PYTHON_VERSIONS" ] || return 0
	info "Installing Python ${PYTHON_VERSIONS}"
	local uvtar="uv-${UV_ARCH}-unknown-linux-gnu.tar.gz" tmp sha v bin
	tmp=$(mktemp -d)
	if ! download "https://github.com/astral-sh/uv/releases/latest/download/${uvtar}.sha256" "${tmp}/sha"; then
		warn "Could not reach GitHub to fetch uv; skipping Python."
		return 0
	fi
	sha=$(cut -d' ' -f1 "${tmp}/sha")
	download "https://github.com/astral-sh/uv/releases/latest/download/${uvtar}" "${tmp}/${uvtar}" "$sha" \
		|| { warn "Could not download uv; skipping Python."; return 0; }
	mkdir -p "${OPT_DIR}/bin"
	tar -xzf "${tmp}/${uvtar}" -C "$tmp"
	install -m 0755 "${tmp}/uv-${UV_ARCH}-unknown-linux-gnu/uv" "${OPT_DIR}/bin/uv"
	rm -rf "$tmp"

	export UV_PYTHON_INSTALL_DIR="${RUNTIMES_DIR}/python/.uv"
	for v in $PYTHON_VERSIONS; do
		if ! "${OPT_DIR}/bin/uv" python install "$v" >>"$LOG_FILE" 2>&1; then
			warn "Python ${v} could not be installed; skipping."
			continue
		fi
		bin=$("${OPT_DIR}/bin/uv" python find --managed-python "$v" 2>>"$LOG_FILE" || true)
		[ -n "$bin" ] || { warn "Python ${v} installed but not found; skipping."; continue; }
		# Stable path the agent uses: runtimes/python/<version>/bin/python3
		ln -sfn "$(dirname "$(dirname "$bin")")" "${RUNTIMES_DIR}/python/${v}"
		PYTHON_INSTALLED="${PYTHON_INSTALLED:-} ${v}"
	done
}

install_mariadb() {
	[ "$INSTALL_MARIADB" -eq 1 ] || return 0
	info "Installing MariaDB"
	# Ubuntu's MariaDB is maintained for the whole LTS lifetime. root uses unix_socket auth.
	apt_install mariadb-server mariadb-client
	systemctl enable --now mariadb >>"$LOG_FILE" 2>&1
	mariadb >>"$LOG_FILE" 2>&1 <<'SQL'
DELETE FROM mysql.global_priv WHERE User='';
DELETE FROM mysql.global_priv WHERE User='root' AND Host NOT IN ('localhost','127.0.0.1','::1');
DROP DATABASE IF EXISTS test;
DELETE FROM mysql.db WHERE Db='test' OR Db='test\_%';
FLUSH PRIVILEGES;
SQL
}

# phpMyAdmin runs on its own PHP-FPM pool and user, independent of website PHP
# versions, and listens on localhost only until the panel proxies it with sign-on.
install_phpmyadmin() {
	[ "$PMA_CHANNEL" = "none" ] && return 0
	local url pma_php="" v tmp
	if [ "$PMA_CHANNEL" = "6" ]; then
		url="https://files.phpmyadmin.net/snapshots/phpMyAdmin-6.0+snapshot-all-languages.tar.gz"
		info "Installing phpMyAdmin 6.0 development snapshot (not for production)"
	else
		url="https://www.phpmyadmin.net/downloads/phpMyAdmin-latest-all-languages.tar.gz"
		info "Installing phpMyAdmin (latest stable)"
	fi
	# Newest installed PHP that phpMyAdmin supports (5.2: 7.2-8.3; 6.0: 8.2+).
	for v in $(tr ' ' '\n' <<<"$PHP_INSTALLED" | sort -rV); do
		case "$PMA_CHANNEL:$v" in
			stable:7.[2-4]|stable:8.[0-3]|6:8.[2-9]) pma_php="$v"; break ;;
		esac
	done
	if [ -z "$pma_php" ]; then
		warn "No installed PHP version suits phpMyAdmin (${PMA_CHANNEL}); skipping. Add PHP 8.3 and re-run."
		return 0
	fi

	tmp=$(mktemp -d)
	download "${url}.sha256" "${tmp}/sha" || { warn "Could not reach phpmyadmin.net; skipping."; return 0; }
	download "$url" "${tmp}/pma.tar.gz" "$(cut -d' ' -f1 "${tmp}/sha")" || { warn "phpMyAdmin download failed; skipping."; return 0; }
	rm -rf "$PMA_DIR"
	mkdir -p "$PMA_DIR"
	tar -xzf "${tmp}/pma.tar.gz" -C "$PMA_DIR" --strip-components=1 --no-same-owner
	rm -rf "$tmp"

	getent passwd ike-pma >/dev/null || useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin ike-pma
	install -d -m 0750 -o ike-pma -g ike-pma /var/lib/ike/pma-tmp
	local secret
	secret=$(openssl rand -base64 32 | tr -d '\n/+=' | cut -c1-32)
	cat >"${PMA_DIR}/config.inc.php" <<EOF
<?php
// Managed by ${PANEL_NAME}
\$cfg['blowfish_secret'] = '${secret}';
\$cfg['TempDir'] = '/var/lib/ike/pma-tmp';
\$cfg['Servers'][1]['auth_type'] = 'cookie';
\$cfg['Servers'][1]['host'] = 'localhost';
\$cfg['Servers'][1]['AllowNoPassword'] = false;
\$cfg['Servers'][1]['AllowRoot'] = false;
\$cfg['LoginCookieValidity'] = 3600;
EOF
	chown root:ike-pma "${PMA_DIR}/config.inc.php"
	chmod 0640 "${PMA_DIR}/config.inc.php"

	cat >"/etc/php/${pma_php}/fpm/pool.d/ike-pma.conf" <<EOF
; Managed by ${PANEL_NAME}
[ike-pma]
user = ike-pma
group = ike-pma
listen = /run/php/ike-pma.sock
listen.owner = ${WEB_USER}
listen.group = ${WEB_GROUP}
listen.mode = 0660
pm = ondemand
pm.max_children = 4
php_admin_value[open_basedir] = ${PMA_DIR}:/var/lib/ike/pma-tmp:/usr/share/php
php_admin_value[upload_tmp_dir] = /var/lib/ike/pma-tmp
php_admin_value[session.save_path] = /var/lib/ike/pma-tmp
EOF
	systemctl reload "php${pma_php}-fpm"

	if [ "$WEB_STACK" != "apache" ]; then
		cat >/etc/nginx/conf.d/${PANEL_NAME}-phpmyadmin.conf <<EOF
# phpMyAdmin on localhost only. Reach it with:  ssh -L ${PMA_PORT}:127.0.0.1:${PMA_PORT} root@this-server
server {
    listen 127.0.0.1:${PMA_PORT};
    root ${PMA_DIR};
    index index.php;
    location ~ /(libraries|setup|templates|vendor)/ { deny all; }
    location ~ \.php\$ {
        try_files \$uri =404;
        fastcgi_pass unix:/run/php/ike-pma.sock;
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME \$document_root\$fastcgi_script_name;
    }
}
EOF
		nginx -t >>"$LOG_FILE" 2>&1 && systemctl reload nginx
	else
		echo "Listen 127.0.0.1:${PMA_PORT}" >>/etc/apache2/ports.conf
		cat >/etc/apache2/conf-available/${PANEL_NAME}-phpmyadmin.conf <<EOF
# phpMyAdmin on localhost only.
<VirtualHost 127.0.0.1:${PMA_PORT}>
    DocumentRoot ${PMA_DIR}
    <Directory ${PMA_DIR}>
        Require all granted
        DirectoryIndex index.php
    </Directory>
    <FilesMatch "\.php\$">
        SetHandler "proxy:unix:/run/php/ike-pma.sock|fcgi://localhost"
    </FilesMatch>
</VirtualHost>
EOF
		a2enconf -q ${PANEL_NAME}-phpmyadmin >>"$LOG_FILE" 2>&1
		apache2ctl configtest >>"$LOG_FILE" 2>&1 && systemctl reload apache2
	fi
	PMA_INSTALLED="${PMA_CHANNEL} on PHP ${pma_php}"
}

# SSH access for hosting accounts:
#   ike-sftp group -> SFTP only, chrooted to the account's home
#   ike-ssh  group -> normal shell (trusted users)
# Keys are read from a root-owned file per account so users can't add keys themselves.
configure_ssh() {
	info "Configuring SSH/SFTP for hosting accounts"
	apt_install openssh-server
	getent group ike-sftp >/dev/null || groupadd --system ike-sftp
	getent group ike-ssh  >/dev/null || groupadd --system ike-ssh
	install -d -m 0755 -o root -g root "/etc/ssh/${PANEL_NAME}" "$SSH_KEYS_DIR"

	local cfg=/etc/ssh/sshd_config backup
	backup="${cfg}.${PANEL_NAME}.bak.$(date +%s)"
	cp -a "$cfg" "$backup"

	# Match blocks go at the very END of sshd_config: everything after a Match line
	# belongs to it, so appending is the only placement that can't capture the
	# admin's own settings.
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
	systemctl reload ssh >>"$LOG_FILE" 2>&1 || systemctl reload sshd >>"$LOG_FILE" 2>&1 || true
}

configure_firewall() {
	info "Configuring firewall (ufw) and fail2ban"
	SSH_PORTS=$(sshd -T 2>/dev/null | awk '$1=="port" {print $2}')
	SSH_PORTS="${SSH_PORTS:-22}"
	local p
	# Allow SSH before enabling the firewall so the current session is never cut off.
	for p in $SSH_PORTS; do
		ufw allow "${p}/tcp" comment 'SSH' >>"$LOG_FILE" 2>&1
	done
	{
		ufw allow 80/tcp comment 'HTTP'
		ufw allow 443/tcp comment 'HTTPS'
		for p in $PANEL_PORTS; do ufw allow "${p}/tcp" comment 'Ike panel'; done
		ufw default deny incoming
		ufw default allow outgoing
		ufw --force enable
	} >>"$LOG_FILE" 2>&1

	local jails="[sshd]
enabled  = true
maxretry = 5
findtime = 10m
bantime  = 1h"
	if [ "$WEB_STACK" != "apache" ]; then
		jails="${jails}

[nginx-http-auth]
enabled = true

[nginx-botsearch]
enabled  = true
logpath  = /var/log/ike/*/access.log"
	fi
	printf '%s\n' "$jails" >/etc/fail2ban/jail.d/${PANEL_NAME}.conf
	systemctl enable --now fail2ban >>"$LOG_FILE" 2>&1
	systemctl restart fail2ban
}

fetch_binary() { # name dest local_path
	local name="$1" dest="$2" local_path="$3" url tmp
	if [ -n "$local_path" ]; then
		[ -f "$local_path" ] || die "$local_path not found"
		install -m 0755 "$local_path" "$dest"
		return
	fi
	if [ "$IKE_VERSION" = "latest" ]; then
		url="https://github.com/${REPO}/releases/latest/download/${name}-linux-${ARCH}"
	else
		url="https://github.com/${REPO}/releases/download/${IKE_VERSION}/${name}-linux-${ARCH}"
	fi
	tmp=$(mktemp)
	download "${url}.sha256" "${tmp}.sha256" \
		|| die "Could not download ${name} from ${url}. Build it and pass --agent-binary / --mcp-binary."
	download "$url" "$tmp" "$(cut -d' ' -f1 "${tmp}.sha256")" || die "Could not download ${name}."
	install -m 0755 "$tmp" "$dest"
	rm -f "$tmp" "${tmp}.sha256"
}

install_agent() {
	info "Installing ${AGENT_NAME} and ${MCP_NAME}"
	getent group "$PANEL_NAME" >/dev/null || groupadd --system "$PANEL_NAME"
	install -d -m 0755 -o root -g root "$CONF_DIR" "${CONF_DIR}/pages" "${OPT_DIR}" "$RUNTIMES_DIR" \
		"${RUNTIMES_DIR}/node" "${RUNTIMES_DIR}/python"
	install -d -m 0711 -o root -g root "$STATE_DIR"
	install -d -m 0700 -o root -g root "${CONF_DIR}/apps" "$STATE_DIR/sites" "$STATE_DIR/accounts" "$STATE_DIR/imports"
	install -d -m 0750 -o root -g adm "$PANEL_LOG_DIR"

	fetch_binary "$AGENT_NAME" "/usr/local/bin/${AGENT_NAME}" "$AGENT_BINARY"
	fetch_binary "$MCP_NAME" "/usr/local/bin/${MCP_NAME}" "$MCP_BINARY"

	local protected="" p
	for p in $SSH_PORTS 80 443 $PANEL_PORTS; do protected="${protected}\"${p}/tcp\", "; done
	cat >"$CONF_DIR/agent.json" <<EOF
{
  "web_stack": "${WEB_STACK}",
  "default_web_server": "${DEFAULT_WEB_SERVER}",
  "web_user": "${WEB_USER}",
  "web_group": "${WEB_GROUP}",
  "nginx_sites_dir": "${NGINX_SITES_DIR}",
  "apache_sites_dir": "${APACHE_SITES_DIR}",
  "apache_listen": "${APACHE_LISTEN:-127.0.0.1:8080}",
  "home_base": "/home",
  "state_dir": "${STATE_DIR}/sites",
  "accounts_dir": "${STATE_DIR}/accounts",
  "pages_dir": "${CONF_DIR}/pages",
  "apps_dir": "${CONF_DIR}/apps",
  "runtimes_dir": "${RUNTIMES_DIR}",
  "tools_dir": "${OPT_DIR}/bin",
  "imports_dir": "${STATE_DIR}/imports",
  "repo": "${REPO}",
  "default_php": "${DEFAULT_PHP}",
  "protected_ports": [${protected%, }]
}
EOF
	chmod 0640 "$CONF_DIR/agent.json"

	cat >/etc/systemd/system/${AGENT_NAME}.service <<EOF
[Unit]
Description=${PANEL_NAME} agent
After=network.target

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
	curl -fsS --unix-socket "/run/${PANEL_NAME}/agent.sock" http://agent/v1/health >>"$LOG_FILE" 2>&1 \
		|| die "${AGENT_NAME} did not start. Check: journalctl -u ${AGENT_NAME}"
	echo "installed_at=$(date -u +%FT%TZ) ubuntu=${OS_VERSION} web=${WEB_STACK} php=\"${PHP_INSTALLED}\"" >"$CONF_DIR/installed"
}

install_panel() {
	info "Installing the Ike web panel"
	fetch_binary "ike-panel" "/usr/local/bin/ike-panel" "$PANEL_BINARY"
	# The panel runs unprivileged; the ike group lets it talk to the agent socket.
	getent passwd ike >/dev/null || useradd --system --gid "$PANEL_NAME" --home-dir "${STATE_DIR}/panel" --shell /usr/sbin/nologin ike
	install -d -m 0700 -o ike -g "$PANEL_NAME" "${STATE_DIR}/panel" "${CONF_DIR}/tls"
	local host
	host=$(hostname -f 2>/dev/null || hostname)
	if [ ! -f "${CONF_DIR}/panel.json" ]; then
		cat >"${CONF_DIR}/panel.json" <<JSON
{
  "listeners": [
    { "name": "panel",    "addr": ":2083", "roles": ["user", "reseller", "admin"] },
    { "name": "reseller", "addr": ":2086", "roles": ["reseller", "admin"] },
    { "name": "admin",    "addr": ":2087", "roles": ["admin"] }
  ],
  "tls_cert": "${CONF_DIR}/tls/panel.crt",
  "tls_key": "${CONF_DIR}/tls/panel.key",
  "agent_socket": "/run/${PANEL_NAME}/agent.sock",
  "store": "${STATE_DIR}/panel/panel.json",
  "hostname": "${host}",
  "public_url": "https://${host}:2083"
}
JSON
	fi
	chown root:"$PANEL_NAME" "${CONF_DIR}/panel.json"
	chmod 0640 "${CONF_DIR}/panel.json"

	if [ ! -s "${STATE_DIR}/panel/panel.json" ]; then
		ADMIN_EMAIL="${ADMIN_EMAIL:-admin@${host}}"
		ADMIN_PASSWORD_LINE=$(runuser -u ike -- /usr/local/bin/ike-panel create-admin --username "$ADMIN_USER" \
			--email "$ADMIN_EMAIL" --store "${STATE_DIR}/panel/panel.json" </dev/null)
	fi

	cat >/etc/systemd/system/ike-panel.service <<UNIT
[Unit]
Description=Ike web panel
After=network.target ${AGENT_NAME}.service
Wants=${AGENT_NAME}.service

[Service]
User=ike
Group=${PANEL_NAME}
ExecStart=/usr/local/bin/ike-panel --config ${CONF_DIR}/panel.json
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=${STATE_DIR}/panel ${CONF_DIR}/tls
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryMax=512M

[Install]
WantedBy=multi-user.target
UNIT
	systemctl daemon-reload
	systemctl enable --now ike-panel >>"$LOG_FILE" 2>&1
	systemctl restart ike-panel
}

summary() {
	local ip
	ip=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')
	cat <<EOF

${C_BLD}${C_GRN}${PANEL_NAME} installed successfully${C_RST}

  Ubuntu        ${OS_VERSION} (${ARCH})
  Web server    ${WEB_STACK}$( [ "$WEB_STACK" = "hybrid" ] && echo " (new websites: ${DEFAULT_WEB_SERVER})" )$( [ -n "${NGINX_SOURCE:-}" ] && echo ", Nginx from ${NGINX_SOURCE}" )
  PHP           ${PHP_INSTALLED} (default ${DEFAULT_PHP})
  Node.js       ${NODE_INSTALLED:- none}
  Python        ${PYTHON_INSTALLED:- none}
  MariaDB       $( [ "$INSTALL_MARIADB" -eq 1 ] && echo installed || echo skipped )
  phpMyAdmin    ${PMA_INSTALLED:-not installed}$( [ -n "${PMA_INSTALLED:-}" ] && echo " — ssh -L ${PMA_PORT}:127.0.0.1:${PMA_PORT} root@${ip}, then open http://localhost:${PMA_PORT}" )
  Server IP     ${ip}
  Install log   ${LOG_FILE}

${C_BLD}Sign in to the panel${C_RST} (browsers warn about the self-signed certificate until SSL is set up):

  Admin panel       https://${ip}:2087
  Reseller panel    https://${ip}:2086
  Account owners    https://${ip}:2083
  ${ADMIN_PASSWORD_LINE:-The admin account already exists.}

  Turn on two-factor sign-in in Settings after you sign in.

${C_BLD}Or use the API directly${C_RST} (point example.com's DNS A record to ${ip} first):

  curl --unix-socket /run/${PANEL_NAME}/agent.sock -X POST http://agent/v1/accounts -d '{"name":"example"}'
  curl --unix-socket /run/${PANEL_NAME}/agent.sock -X POST http://agent/v1/sites \\
       -d '{"domain":"example.com","account":"example"}'

${C_BLD}Connect an AI agent (MCP)${C_RST} — in your MCP client config on your own computer:

  { "mcpServers": { "${PANEL_NAME}": { "command": "ssh", "args": ["root@${ip}", "${MCP_NAME}"] } } }

  Read-only by default; add "--allow-write" after "${MCP_NAME}" to allow changes.
  Every call is logged to ${PANEL_LOG_DIR}/mcp-audit.log.

Customize the starter, maintenance, suspended and error pages in ${CONF_DIR}/pages.
EOF
}

main() {
	: >"$LOG_FILE"
	chmod 0600 "$LOG_FILE"
	check_system
	info "Installing ${PANEL_NAME} on Ubuntu ${OS_VERSION} (${OS_CODENAME}, ${ARCH})"
	install_base
	install_nginx
	install_apache
	detect_web_user
	install_php
	install_wpcli
	install_node
	install_python
	install_mariadb
	install_phpmyadmin
	configure_ssh
	configure_firewall
	install_agent
	install_panel
	summary
}

main "$@"
