#!/usr/bin/env bash
# Ike installer for Ubuntu 22.04, 24.04 and 26.04 LTS.
# Run on a FRESH KVM VPS as root:
#   curl -sSL https://raw.githubusercontent.com/Mastermegamind/ike/main/installer/install.sh | sudo bash
#
# Every component is the server admin's choice; run with --help for options.
# Licensed under the GNU AGPL-3.0.

set -euo pipefail

PANEL_NAME="ike"
AGENT_NAME="ike-agent"
MCP_NAME="ike-mcp"
REPO="${IKE_REPO:-Mastermegamind/ike}"          # override with IKE_REPO=owner/name
IKE_VERSION="${IKE_VERSION:-latest}"     # or a tag such as v0.3.0
SUPPORTED_UBUNTU="22.04 24.04 26.04"
ALL_PHP="5.6 7.0 7.1 7.2 7.3 7.4 8.0 8.1 8.2 8.3 8.4 8.5"

# ---- admin choices (defaults) ----
WEB_STACK="nginx"           # nginx | apache | hybrid | caddy | openlitespeed
DEFAULT_WEB_SERVER=""       # hybrid only: nginx | nginx+apache
PHP_VERSIONS="8.2 8.3 8.4 8.5"
DEFAULT_PHP=""
NODE_VERSIONS="22"
PYTHON_VERSIONS="3.12"
PMA_CHANNEL="stable"        # stable | 6 | none
JAVA_VERSIONS=""            # e.g. "17 21"
GO_VERSIONS=""              # e.g. "1.24"
DART_VERSIONS=""            # e.g. "latest" or "3.9.4"
INSTALL_MAVEN=0
INSTALL_PKG_TOOLS=1          # Poetry, Pipenv, pipx, uv, PEAR/PECL, Phive
GRADLE_VERSION=""
INSTALL_DOCKER=0
DOCKER_FOR_ACCOUNTS=0
INSTALL_PACK=0
INSTALL_K3S=0
INSTALL_MARIADB=1
INSTALL_DNS=0               # PowerDNS for hosting DNS zones
INSTALL_MAIL=0              # Postfix + Dovecot email hosting
INSTALL_RSPAMD=1            # spam filter and DKIM signing (with --mail)
INSTALL_CLAMAV=1            # antivirus for mail and malware scans (with --mail)
INSTALL_WEBMAIL=1           # Roundcube (with --mail)
WEBMAIL_PORT=2096
SERVER_HOSTNAME=""
POSTGRES_VERSION=""         # "" = none, "default" = Ubuntu's, or 14-18 from apt.postgresql.org
REDIS_KIND=""               # redis | valkey: the server-wide engine (the first of them chosen)
CACHE_ENGINES=""            # per-account cache engines: any of redis valkey dragonfly
INSTALL_TURN=0              # TURN server (coturn) for WebRTC calls
INSTALL_LIVEKIT=0           # LiveKit WebRTC media server
ADD_ONLY=""                 # --add: install only these on a server that already runs Ike
MONGODB_VERSION=""          # e.g. 8.0
JOIN_URL=""                 # join another server's panel instead of being managed here
JOIN_TOKEN=""
JOIN_FINGERPRINT=""
DISK_QUOTAS=0               # Linux user quotas so per-account disk limits are enforced
QUOTAS_ACTIVE=0
FIREWALL="ufw"              # ufw | nftables | firewalld
FW_EXTRA=""                 # ports later steps open (DNS, mail, webmail), kept open by the agent
INSTALL_MODSEC=0            # ModSecurity web application firewall with the OWASP Core Rule Set
INSTALL_SCANNER=0           # ClamAV for malware scans of websites (always on with --mail)
FORCE=0
AGENT_BINARY=""
MCP_BINARY=""
CLI_BINARY=""
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
CADDY_SITES_DIR="/etc/caddy/${PANEL_NAME}"
OLS_SITES_DIR="/usr/local/lsws/conf/${PANEL_NAME}"
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

# add_cache ENGINE records a cache engine once. The first of Redis/Valkey is
# also the server-wide one.
add_cache() {
	case " $CACHE_ENGINES " in *" $1 "*) ;; *) CACHE_ENGINES="${CACHE_ENGINES:+$CACHE_ENGINES }$1" ;; esac
	if [ -z "$REDIS_KIND" ] && [ "$1" != "dragonfly" ]; then REDIS_KIND="$1"; fi
}

usage() {
	cat <<EOF
Usage: install.sh [options]

Web server
  --web nginx|apache|hybrid|caddy|openlitespeed
                              nginx: Nginx only (default)
                              apache: Apache only
                              hybrid: Nginx in front; each website picks Nginx or Nginx+Apache (.htaccess)
                              caddy: Caddy only (Ike still issues the certificates; no ModSecurity)
                              openlitespeed: OpenLiteSpeed from LiteSpeed's repository, PHP through
                              Ike's PHP-FPM pools, .htaccess rewrites (no ModSecurity)
  --default-web-server X      hybrid only: "nginx" or "nginx+apache" for new websites

Languages
  --php "7.4 8.3"             PHP versions, or "all" for ${ALL_PHP}
                              (default: "${PHP_VERSIONS}")
  --default-php 8.3           PHP version for new websites (default: newest installed)
  --node "20 22 24"           Node.js major versions, or "none" (default: "${NODE_VERSIONS}")
  --python "3.12 3.13"        Python versions, or "none" (default: "${PYTHON_VERSIONS}")
  --java "17 21"              OpenJDK versions (default: none)
  --maven                     Also install Maven for Java builds
  --gradle 8.14               Also install Gradle (projects with gradlew don't need it)
  --no-package-tools          Skip Poetry, Pipenv, pipx, uv, PEAR/PECL and Phive
  --go "1.24 1.25"            Go versions (default: none)
  --dart "latest"             Dart SDK: latest or an exact version like 3.9.4 (default: none)

Containers
  --docker                    Install Docker Engine with Compose and Buildx
  --docker-for-accounts       Let account owners run containers and Compose stacks (not just admins)
  --buildpacks                Install the Cloud Native Buildpacks tool (pack) for Git deploys without a Dockerfile
  --k3s                       Install Kubernetes (k3s, single node) for server admins

DNS and email
  --dns                       Run a DNS server (PowerDNS) for your domains' zones
  --mail                      Email hosting: Postfix, Dovecot, Rspamd (spam, DKIM), ClamAV and Roundcube webmail
  --no-antivirus              With --mail: skip ClamAV (saves about 1 GB of memory)
  --no-webmail                With --mail: skip Roundcube
  --hostname mail.example.com This server's name (mail greeting, reverse DNS); default: hostname -f

Security
  --firewall ufw|nftables|firewalld
                              The firewall Ike drives (default: ufw). nftables: Ike keeps its own "inet ike"
                              table, loaded at boot by ike-firewall.service. fail2ban bans through the same one.
  --disk-quotas               Enforce per-account disk limits with Linux user quotas (ext4; XFS after a reboot).
  --modsecurity               Web application firewall (ModSecurity + OWASP Core Rule Set), switched on per website.
                              With Nginx this uses Ubuntu's Nginx package, which the ModSecurity module is built for.
  --clamav                    ClamAV for malware scans of websites (included with --mail)

Databases
  --no-mariadb                Do not install MariaDB
  --postgres [VERSION]        PostgreSQL: Ubuntu's version, or 14-18 from the PostgreSQL project's repository
  --redis --valkey --dragonfly
                              Cache engines for apps: each account gets a private instance on a unix socket
                              and picks its engine. Use one or several; the first of Redis/Valkey is also
                              the server-wide one (replication).

Adding to a server that already runs Ike
  --add LIST                  Install only these, keep everything else as it is, and tell the agent:
                              any of redis, valkey, dragonfly, turn, livekit (comma-separated).
                              Example: install.sh --add dragonfly,turn,livekit

Real-time media
  --turn                      TURN server (coturn): lets WebRTC calls connect through strict firewalls and
                              mobile networks. Switched on in the panel; opens 3478, 5349 and 40000-49999/udp.
  --livekit                   LiveKit media server for video calls, rooms and live streams. Switched on in the
                              panel, behind a website of yours (wss://); opens 7881/tcp and 50000-60000/udp.
  --mongodb [VERSION]         MongoDB Community from mongodb.org (default 8.0)
  --phpmyadmin stable|6|none  phpMyAdmin channel (default: stable; "6" = 6.0 development snapshot)

Multiple servers
  --join URL                  Join this server to a panel on another server (https://panel.example.com:2087)
  --join-token TOKEN          The one-time token from "Servers → Add server" in that panel
  --join-fingerprint FP       That panel's certificate fingerprint (shown with the token)

Other
  --force                     Install even if the server is not fresh
  --agent-binary PATH         Use a locally built ${AGENT_NAME}
  --mcp-binary PATH           Use a locally built ${MCP_NAME}
  --cli-binary PATH           Use a locally built ike command-line tool
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
		--java) JAVA_VERSIONS="${2:?}"; shift 2 ;;
		--maven) INSTALL_MAVEN=1; shift ;;
		--gradle) GRADLE_VERSION="${2:?}"; shift 2 ;;
		--no-package-tools) INSTALL_PKG_TOOLS=0; shift ;;
		--go) GO_VERSIONS="${2:?}"; shift 2 ;;
		--dart) DART_VERSIONS="${2:?}"; shift 2 ;;
		--docker) INSTALL_DOCKER=1; shift ;;
		--docker-for-accounts) INSTALL_DOCKER=1; DOCKER_FOR_ACCOUNTS=1; shift ;;
		--buildpacks) INSTALL_DOCKER=1; INSTALL_PACK=1; shift ;;
		--k3s) INSTALL_K3S=1; shift ;;
		--no-mariadb) INSTALL_MARIADB=0; shift ;;
		--dns) INSTALL_DNS=1; shift ;;
		--modsecurity) INSTALL_MODSEC=1; shift ;;
		--firewall) FIREWALL="${2:?}"; shift 2 ;;
		--disk-quotas) DISK_QUOTAS=1; shift ;;
		--join) JOIN_URL="${2:?}"; shift 2 ;;
		--join-token) JOIN_TOKEN="${2:?}"; shift 2 ;;
		--join-fingerprint) JOIN_FINGERPRINT="${2:?}"; shift 2 ;;
		--postgres) if [[ "${2:-}" =~ ^1[4-9]$ ]]; then POSTGRES_VERSION="$2"; shift 2; else POSTGRES_VERSION="default"; shift; fi ;;
		--redis) add_cache redis; shift ;;
		--valkey) add_cache valkey; shift ;;
		--dragonfly) add_cache dragonfly; shift ;;
		--turn) INSTALL_TURN=1; shift ;;
		--add)
			ADD_ONLY="${2:?}"
			for e in ${ADD_ONLY//,/ }; do
				case "$e" in
					redis|valkey|dragonfly) add_cache "$e" ;;
					turn) INSTALL_TURN=1 ;;
					livekit) INSTALL_LIVEKIT=1 ;;
					*) die "--add takes redis, valkey, dragonfly, turn or livekit (got: $e)" ;;
				esac
			done
			shift 2 ;;
		--livekit) INSTALL_LIVEKIT=1; shift ;;
		--mongodb) if [[ "${2:-}" =~ ^[5-9]\.[0-9]$ ]]; then MONGODB_VERSION="$2"; shift 2; else MONGODB_VERSION="8.0"; shift; fi ;;
		--clamav) INSTALL_SCANNER=1; shift ;;
		--mail) INSTALL_MAIL=1; shift ;;
		--no-antivirus) INSTALL_CLAMAV=0; shift ;;
		--no-webmail) INSTALL_WEBMAIL=0; shift ;;
		--hostname) SERVER_HOSTNAME="${2:?}"; shift 2 ;;
		--phpmyadmin) PMA_CHANNEL="${2:?}"; shift 2 ;;
		--force) FORCE=1; shift ;;
		--agent-binary) AGENT_BINARY="${2:?}"; shift 2 ;;
		--mcp-binary) MCP_BINARY="${2:?}"; shift 2 ;;
		--cli-binary) CLI_BINARY="${2:?}"; shift 2 ;;
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
	echo "Web server: nginx (fastest), apache (Apache only), hybrid (Nginx in front, Apache per website for .htaccess)," >&2
	echo "            caddy (Caddy only) or openlitespeed (OpenLiteSpeed, reads .htaccess rewrites)" >&2
	ask "Web server" "$WEB_STACK"; WEB_STACK="$REPLY"
	if [ "$WEB_STACK" = "hybrid" ]; then
		ask "New websites use (nginx or nginx+apache)" "nginx+apache"; DEFAULT_WEB_SERVER="$REPLY"
	fi
	echo "PHP versions separated by spaces, or 'all' for ${ALL_PHP}" >&2
	ask "PHP versions" "$PHP_VERSIONS"; PHP_VERSIONS="$REPLY"
	ask "Node.js versions (or none)" "$NODE_VERSIONS"; NODE_VERSIONS="$REPLY"
	ask "Python versions (or none)" "$PYTHON_VERSIONS"; PYTHON_VERSIONS="$REPLY"
	ask "Java versions, e.g. 21 (or none)" "${JAVA_VERSIONS:-none}"; JAVA_VERSIONS="$REPLY"
	ask "Go versions, e.g. 1.24 (or none)" "${GO_VERSIONS:-none}"; GO_VERSIONS="$REPLY"
	ask "Dart SDK (latest, a version, or none)" "${DART_VERSIONS:-none}"; DART_VERSIONS="$REPLY"
	ask "Install Docker for containers and Compose (yes/no)" "no"
	if [ "$REPLY" = "yes" ]; then
		INSTALL_DOCKER=1
		ask "Let account owners run containers too (yes/no)" "no"
		[ "$REPLY" = "yes" ] && DOCKER_FOR_ACCOUNTS=1
		ask "Install buildpacks for deploys without a Dockerfile (yes/no)" "no"
		[ "$REPLY" = "yes" ] && INSTALL_PACK=1
	fi
	ask "Install Kubernetes (k3s) for admins (yes/no)" "no"
	[ "$REPLY" = "yes" ] && INSTALL_K3S=1
	ask "Run a DNS server for your domains (yes/no)" "no"
	[ "$REPLY" = "yes" ] && INSTALL_DNS=1
	ask "Host email (Postfix, Dovecot, spam filter, webmail) (yes/no)" "no"
	if [ "$REPLY" = "yes" ]; then
		INSTALL_MAIL=1
		ask "Scan mail for viruses with ClamAV (needs about 1 GB of memory) (yes/no)" "yes"
		[ "$REPLY" = "no" ] && INSTALL_CLAMAV=0
		ask "Install Roundcube webmail (yes/no)" "yes"
		[ "$REPLY" = "no" ] && INSTALL_WEBMAIL=0
		ask "This server's hostname (used in mail headers and reverse DNS)" "$(hostname -f 2>/dev/null || hostname)"
		SERVER_HOSTNAME="$REPLY"
	fi
	ask "Enforce disk limits per account with Linux quotas (yes/no)" "yes"
	[ "$REPLY" = "yes" ] && DISK_QUOTAS=1
	ask "Firewall (ufw, nftables or firewalld)" "$FIREWALL"; FIREWALL="$REPLY"
	ask "Web application firewall, ModSecurity (yes/no)" "no"
	[ "$REPLY" = "yes" ] && INSTALL_MODSEC=1
	if [ "$INSTALL_MAIL" -eq 0 ]; then
		ask "ClamAV for malware scans of websites (needs about 1 GB of memory) (yes/no)" "no"
		[ "$REPLY" = "yes" ] && INSTALL_SCANNER=1
	fi
	ask "Install MariaDB (yes/no)" "yes"
	if [ "$REPLY" = "no" ]; then INSTALL_MARIADB=0; fi
	if [ "$INSTALL_MARIADB" -eq 1 ]; then
		ask "phpMyAdmin (stable, 6 for the 6.0 preview, none)" "$PMA_CHANNEL"; PMA_CHANNEL="$REPLY"
	fi
	ask "PostgreSQL (no, yes, or a version 14-18)" "no"
	case "$REPLY" in yes) POSTGRES_VERSION="default" ;; 1[4-9]) POSTGRES_VERSION="$REPLY" ;; esac
	ask "Caches for apps (no, or one or more of: redis valkey dragonfly)" "no"
	local e
	for e in $REPLY; do
		case "$e" in redis|valkey|dragonfly) add_cache "$e" ;; esac
	done
	ask "TURN server for video and voice calls (yes/no)" "no"
	[ "$REPLY" = "yes" ] && INSTALL_TURN=1
	ask "LiveKit media server for video rooms (yes/no)" "no"
	[ "$REPLY" = "yes" ] && INSTALL_LIVEKIT=1
	ask "MongoDB (no, yes, or a version like 8.0)" "no"
	case "$REPLY" in yes) MONGODB_VERSION="8.0" ;; [5-9].[0-9]) MONGODB_VERSION="$REPLY" ;; esac
	ask "First panel admin username" "$ADMIN_USER"; ADMIN_USER="$REPLY"
	ask "Admin email" "${ADMIN_EMAIL:-admin@$(hostname -f 2>/dev/null || hostname)}"; ADMIN_EMAIL="$REPLY"
	ask "Install now? (yes/no)" "yes"
	[ "$REPLY" = "yes" ] || die "Cancelled. Nothing was changed."
}

if [ "$WIZARD" -eq 1 ]; then run_wizard; fi

[ "$PHP_VERSIONS" = "all" ] && PHP_VERSIONS="$ALL_PHP"
[ "$NODE_VERSIONS" = "none" ] && NODE_VERSIONS=""
[ "$PYTHON_VERSIONS" = "none" ] && PYTHON_VERSIONS=""
[ "$JAVA_VERSIONS" = "none" ] && JAVA_VERSIONS=""
[ "$GO_VERSIONS" = "none" ] && GO_VERSIONS=""
[ "$DART_VERSIONS" = "none" ] && DART_VERSIONS=""
for v in $JAVA_VERSIONS; do case "$v" in 8|1[1-9]|2[0-9]) ;; *) die "--java takes major versions like \"17 21\"" ;; esac; done
for v in $GO_VERSIONS; do case "$v" in 1.[2-9][0-9]) ;; *) die "--go takes versions like 1.24" ;; esac; done
for v in $DART_VERSIONS; do case "$v" in latest|[3-9].[0-9]*.[0-9]*) ;; *) die "--dart takes latest or a version like 3.9.4" ;; esac; done

case "$WEB_STACK" in nginx|apache|hybrid|caddy|openlitespeed) ;; *) die "--web must be nginx, apache, hybrid, caddy or openlitespeed" ;; esac
if [ "$INSTALL_MODSEC" -eq 1 ] && { [ "$WEB_STACK" = "caddy" ] || [ "$WEB_STACK" = "openlitespeed" ]; }; then
	die "--modsecurity needs --web nginx, apache or hybrid"
fi
case "$FIREWALL" in ufw|nftables|firewalld) ;; *) die "--firewall must be ufw, nftables or firewalld" ;; esac
case "$DEFAULT_WEB_SERVER" in
	"") [ "$WEB_STACK" = "hybrid" ] && DEFAULT_WEB_SERVER="nginx+apache" ;;
	nginx|nginx+apache) [ "$WEB_STACK" = "hybrid" ] || die "--default-web-server only applies to --web hybrid" ;;
	*) die "--default-web-server must be nginx or nginx+apache" ;;
esac
case "$GRADLE_VERSION" in ""|[0-9]*.[0-9]*) ;; *) die "--gradle takes a version like 8.14" ;; esac
case "$PMA_CHANNEL" in stable|6|none) ;; *) die "--phpmyadmin must be stable, 6 or none" ;; esac
[ "$INSTALL_MARIADB" -eq 0 ] && PMA_CHANNEL="none"

for v in $PHP_VERSIONS; do [[ "$v" =~ ^[5-9]\.[0-9]$ ]] || die "Invalid PHP version: $v"; done
for v in $NODE_VERSIONS; do [[ "$v" =~ ^[1-9][0-9]?$ ]] || die "Invalid Node.js version: $v (use the major version, e.g. 22)"; done
for v in $PYTHON_VERSIONS; do [[ "$v" =~ ^3\.[0-9]{1,2}$ ]] || die "Invalid Python version: $v"; done
[ -z "$DEFAULT_PHP" ] || [[ "$DEFAULT_PHP" =~ ^[5-9]\.[0-9]$ ]] || die "Invalid --default-php"
[ -z "$SERVER_HOSTNAME" ] || [[ "$SERVER_HOSTNAME" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$ ]] || die "--hostname must be a full name like mail.example.com"
if [ "$INSTALL_MAIL" -eq 0 ]; then INSTALL_RSPAMD=0; INSTALL_CLAMAV=0; INSTALL_WEBMAIL=0; fi
if [ -n "$JOIN_URL" ]; then
	[[ "$JOIN_URL" =~ ^https://[A-Za-z0-9.:-]+(/)?$ ]] || die "--join takes the panel's address, like https://panel.example.com:2087"
	[[ "$JOIN_TOKEN" =~ ^[0-9a-f]{48}$ ]] || die "--join-token is the 48-character token from the panel"
	[[ "$JOIN_FINGERPRINT" =~ ^[0-9A-Fa-f:]{64,95}$ ]] || die "--join-fingerprint is shown in the panel next to the token"
fi

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
		fail2ban zip unzip xz-utils bzip2 restic acl openssl cron logrotate python3 rsync unattended-upgrades quota \
		git certbot
	systemctl enable --now cron >>"$LOG_FILE" 2>&1
}

# Prefer nginx.org's stable repo; fall back to Ubuntu's nginx if it doesn't
# support this release yet.
install_nginx() {
	uses_nginx || return 0
	info "Installing Nginx"
	local key=/usr/share/keyrings/nginx-archive-keyring.gpg url="https://nginx.org/packages/ubuntu"
	NGINX_SOURCE="ubuntu"
	if [ "$INSTALL_MODSEC" -eq 1 ]; then
		info "Using Ubuntu's Nginx: the ModSecurity module is built for it"
		rm -f /etc/apt/sources.list.d/nginx.list /etc/apt/preferences.d/99nginx
	elif curl -fsS --max-time 15 "${url}/dists/${OS_CODENAME}/Release" -o /dev/null 2>>"$LOG_FILE" \
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
	uses_apache || return 0
	info "Installing Apache (${WEB_STACK} mode)"
	apt_install apache2
	a2dismod -q php* mpm_prefork >>"$LOG_FILE" 2>&1 || true
	a2enmod -q mpm_event proxy proxy_fcgi proxy_http proxy_wstunnel proxy_balancer slotmem_shm lbmethod_byrequests lbmethod_bybusyness rewrite headers remoteip setenvif alias ssl http2 socache_shmcb >>"$LOG_FILE" 2>&1
	a2dissite -q 000-default >>"$LOG_FILE" 2>&1 || true
	mkdir -p "$APACHE_SITES_DIR"

	if [ "$WEB_STACK" = "hybrid" ]; then
		APACHE_LISTEN="127.0.0.1:8080"
		echo "Listen 127.0.0.1:8080" >/etc/apache2/ports.conf
	else
		APACHE_LISTEN="*:80"
		printf 'Listen 80\n<IfModule ssl_module>\n    Listen 443\n</IfModule>\n' >/etc/apache2/ports.conf
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

uses_nginx()  { [ "$WEB_STACK" = "nginx" ] || [ "$WEB_STACK" = "hybrid" ]; }
uses_apache() { [ "$WEB_STACK" = "apache" ] || [ "$WEB_STACK" = "hybrid" ]; }

# Caddy: Ike writes one file per website into ${CADDY_SITES_DIR} and keeps
# issuing certificates itself, so Caddy's automatic HTTPS stays off.
install_caddy() {
	[ "$WEB_STACK" = "caddy" ] || return 0
	info "Installing Caddy"
	apt_install caddy
	install -d -m 0750 -o root -g caddy "$CADDY_SITES_DIR" "$CADDY_SITES_DIR/certs" "${CADDY_SITES_DIR}-extra"
	cat >/etc/caddy/Caddyfile <<EOF
# Managed by ${PANEL_NAME}. Websites live in ${CADDY_SITES_DIR}, one file each.
{
	# ${PANEL_NAME} issues and renews certificates (the SSL page in the panel).
	auto_https off
	servers {
		protocols h1 h2 h3
	}
}

# Requests for domains not hosted here are closed without a response.
http:// {
	abort
}

import ${CADDY_SITES_DIR}-extra/*.caddy
import ${CADDY_SITES_DIR}/*.caddy
EOF
	# certbot runs this after renewing: Caddy reads a copy of each certificate
	# that its own user may open.
	install -d -m 0755 /usr/local/lib/${PANEL_NAME}
	cat >/usr/local/lib/${PANEL_NAME}/caddy-deploy-hook <<EOF
#!/bin/sh
# Written by the ${PANEL_NAME} installer.
set -e
name=\$(basename "\${RENEWED_LINEAGE:?}")
dir=${CADDY_SITES_DIR}/certs/\$name
[ -d "\$dir" ] || exit 0
install -m 0640 -o root -g caddy "\$RENEWED_LINEAGE/fullchain.pem" "\$dir/fullchain.pem"
install -m 0640 -o root -g caddy "\$RENEWED_LINEAGE/privkey.pem" "\$dir/privkey.pem"
systemctl reload caddy
EOF
	chmod 0755 /usr/local/lib/${PANEL_NAME}/caddy-deploy-hook
	caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile >>"$LOG_FILE" 2>&1 || die "Caddy configuration test failed. See $LOG_FILE"
	systemctl enable --now caddy >>"$LOG_FILE" 2>&1
	systemctl reload caddy >>"$LOG_FILE" 2>&1 || systemctl restart caddy
}

# OpenLiteSpeed from LiteSpeed's repository. PHP runs in Ike's PHP-FPM pools
# (not lsphp), so PHP versions and limits match the other web servers.
install_openlitespeed() {
	[ "$WEB_STACK" = "openlitespeed" ] || return 0
	info "Installing OpenLiteSpeed"
	local key=/usr/share/keyrings/litespeed-archive-keyring.gpg
	curl -fsS --max-time 30 https://rpms.litespeedtech.com/debian/lst_repo.gpg | gpg --dearmor --yes -o "$key" 2>>"$LOG_FILE" \
		|| die "Could not reach LiteSpeed's package repository (rpms.litespeedtech.com)."
	echo "deb [signed-by=${key}] http://rpms.litespeedtech.com/debian/ ${OS_CODENAME} main" >/etc/apt/sources.list.d/lst_debian_repo.list
	apt-get update -q >>"$LOG_FILE" 2>&1
	apt_install openlitespeed
	local conf=/usr/local/lsws/conf/httpd_config.conf
	install -d -m 0750 -o root -g www-data "$OLS_SITES_DIR"
	# Workers run as www-data, like Nginx and Apache on Ubuntu.
	sed -i -E 's/^(user[[:space:]]+).*/\1www-data/; s/^(group[[:space:]]+).*/\1www-data/' "$conf"
	# A default certificate for the HTTPS listener; each website brings its own (SNI).
	[ -f "$OLS_SITES_DIR/default.key" ] || openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=$(hostname -f)" \
		-keyout "$OLS_SITES_DIR/default.key" -out "$OLS_SITES_DIR/default.crt" >>"$LOG_FILE" 2>&1
	chmod 0640 "$OLS_SITES_DIR/default.key"
	chgrp www-data "$OLS_SITES_DIR/default.key"
	[ -f "$OLS_SITES_DIR/sites.conf" ] || cat >"$OLS_SITES_DIR/sites.conf" <<EOF
# Managed by ${PANEL_NAME}. Rewritten by the agent whenever a website changes.
listener ike-http {
  address                 *:80
  secure                  0
}

listener ike-https {
  address                 *:443
  secure                  1
  keyFile                 ${OLS_SITES_DIR}/default.key
  certFile                ${OLS_SITES_DIR}/default.crt
}
EOF
	touch "$OLS_SITES_DIR/extra.conf" "$OLS_SITES_DIR/webmail.conf"
	chgrp www-data "$OLS_SITES_DIR"/*.conf
	chmod 0640 "$OLS_SITES_DIR"/*.conf
	grep -q "include ${OLS_SITES_DIR}/sites.conf" "$conf" || printf '\n# Managed by %s\ninclude %s/extra.conf\ninclude %s/webmail.conf\ninclude %s/sites.conf\n' \
		"$PANEL_NAME" "$OLS_SITES_DIR" "$OLS_SITES_DIR" "$OLS_SITES_DIR" >>"$conf"
	/usr/local/lsws/bin/openlitespeed -t >>"$LOG_FILE" 2>&1 || die "OpenLiteSpeed configuration test failed. See $LOG_FILE"
	systemctl enable --now lsws >>"$LOG_FILE" 2>&1
	/usr/local/lsws/bin/lswsctrl restart >>"$LOG_FILE" 2>&1
}

# Which user/group serves files? Nginx decides when it is installed, else Apache.
detect_web_user() {
	if [ "$WEB_STACK" = "caddy" ]; then
		WEB_USER="caddy"
	elif [ "$WEB_STACK" = "apache" ] || [ "$WEB_STACK" = "openlitespeed" ]; then
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
		local exts="mysql curl gd mbstring xml zip intl bcmath opcache readline soap imagick redis"
		[ -n "$POSTGRES_VERSION" ] && exts="$exts pgsql"
		[ -n "$MONGODB_VERSION" ] && exts="$exts mongodb"
		for ext in $exts; do
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

# Package managers deploys and SSH users can use, besides the ones that come
# with each language (npm, pip, Go modules, pub) and Composer:
#   Python: uv, Poetry, Pipenv and pipx, installed with uv as tools
#   PHP:    PEAR and PECL (php-pear), Phive (signature checked)
#   Java:   Gradle (--gradle VERSION; Maven is --maven)
install_package_tools() {
	[ "$INSTALL_PKG_TOOLS" -eq 1 ] || return 0
	info "Installing package managers"
	local t
	if [ -x "${OPT_DIR}/bin/uv" ]; then
		ln -sfn "${OPT_DIR}/bin/uv" /usr/local/bin/uv
		[ -x "${OPT_DIR}/bin/uvx" ] && ln -sfn "${OPT_DIR}/bin/uvx" /usr/local/bin/uvx
		install -d -m 0755 "${OPT_DIR}/tools"
		for t in poetry pipenv pipx; do
			if UV_TOOL_DIR="${OPT_DIR}/tools" UV_TOOL_BIN_DIR=/usr/local/bin UV_PYTHON_INSTALL_DIR="${RUNTIMES_DIR}/python/.uv" \
				"${OPT_DIR}/bin/uv" tool install --quiet "$t" >>"$LOG_FILE" 2>&1; then
				PKG_TOOLS_INSTALLED="${PKG_TOOLS_INSTALLED:-} ${t}"
			else
				warn "${t} could not be installed; skipping."
			fi
		done
		chmod -R a+rX "${OPT_DIR}/tools"
	fi
	if [ -n "${PHP_INSTALLED:-}" ]; then
		apt_install php-pear && PKG_TOOLS_INSTALLED="${PKG_TOOLS_INSTALLED:-} pear pecl"
		install_phive
	fi
	install_gradle
}

# Phive, the PHAR installer, checked against its developers' GPG signature.
install_phive() {
	local tmp key=0x9D8A98B29B2D5D79
	tmp=$(mktemp -d)
	if download "https://phar.io/releases/phive.phar" "${tmp}/phive.phar" && download "https://phar.io/releases/phive.phar.asc" "${tmp}/phive.phar.asc" \
		&& GNUPGHOME="$tmp" gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys "$key" >>"$LOG_FILE" 2>&1 \
		&& GNUPGHOME="$tmp" gpg --batch --verify "${tmp}/phive.phar.asc" "${tmp}/phive.phar" >>"$LOG_FILE" 2>&1; then
		install -m 0755 "${tmp}/phive.phar" /usr/local/bin/phive
		PKG_TOOLS_INSTALLED="${PKG_TOOLS_INSTALLED:-} phive"
	else
		warn "Phive could not be downloaded or its signature didn't check out; skipping."
	fi
	rm -rf "$tmp"
}

# Gradle from gradle.org, checked against its published SHA-256.
install_gradle() {
	[ -n "$GRADLE_VERSION" ] || return 0
	[ -n "${JAVA_INSTALLED:-}" ] || { warn "Gradle needs Java (--java); skipping."; return 0; }
	local tmp sha zip="gradle-${GRADLE_VERSION}-bin.zip"
	tmp=$(mktemp -d)
	if download "https://services.gradle.org/distributions/${zip}.sha256" "${tmp}/sha"; then
		sha=$(tr -d '[:space:]' <"${tmp}/sha")
		apt_install unzip
		download "https://services.gradle.org/distributions/${zip}" "${tmp}/${zip}" "$sha"
		rm -rf "${OPT_DIR}/gradle"
		unzip -q "${tmp}/${zip}" -d "$tmp"
		mv "${tmp}/gradle-${GRADLE_VERSION}" "${OPT_DIR}/gradle"
		ln -sfn "${OPT_DIR}/gradle/bin/gradle" /usr/local/bin/gradle
		PKG_TOOLS_INSTALLED="${PKG_TOOLS_INSTALLED:-} gradle-${GRADLE_VERSION}"
	else
		warn "Gradle ${GRADLE_VERSION} isn't on gradle.org; skipping."
	fi
	rm -rf "$tmp"
}

# Composer, the PHP package manager, checked against its published SHA-256.
install_composer() {
	[ -n "${PHP_INSTALLED:-}" ] || return 0
	info "Installing Composer"
	local tmp
	tmp=$(mktemp -d)
	if download https://getcomposer.org/download/latest-stable/composer.phar.sha256sum "${tmp}/sum" \
		&& download https://getcomposer.org/download/latest-stable/composer.phar "${tmp}/composer.phar" "$(cut -d' ' -f1 "${tmp}/sum")"; then
		install -m 0755 "${tmp}/composer.phar" /usr/local/bin/composer
		COMPOSER_INSTALLED=1
	else
		warn "Could not download Composer; Git deploys of PHP projects won't run composer install."
	fi
	rm -rf "$tmp"
}

# Java from Ubuntu's OpenJDK packages (security updates come with the OS).
install_java() {
	[ -n "$JAVA_VERSIONS" ] || return 0
	info "Installing Java ${JAVA_VERSIONS}"
	local v home
	mkdir -p "${RUNTIMES_DIR}/java"
	for v in $JAVA_VERSIONS; do
		if ! pkg_available "openjdk-${v}-jdk-headless"; then
			warn "OpenJDK ${v} isn't available on Ubuntu ${OS_VERSION}; skipping."
			continue
		fi
		apt_install "openjdk-${v}-jdk-headless"
		home="/usr/lib/jvm/java-${v}-openjdk-${ARCH}"
		[ -x "${home}/bin/java" ] || { warn "Java ${v} installed but not found in ${home}; skipping."; continue; }
		ln -sfn "$home" "${RUNTIMES_DIR}/java/${v}"
		JAVA_INSTALLED="${JAVA_INSTALLED:-} ${v}"
	done
	[ "$INSTALL_MAVEN" -eq 1 ] && apt_install maven
	return 0
}

# Go from go.dev, checked against the SHA-256 go.dev publishes.
install_go() {
	[ -n "$GO_VERSIONS" ] || return 0
	info "Installing Go ${GO_VERSIONS}"
	local index v line ver sha file tmp
	index=$(mktemp)
	download "https://go.dev/dl/?mode=json&include=all" "$index" || { warn "Could not reach go.dev; skipping Go."; return 0; }
	for v in $GO_VERSIONS; do
		line=$(python3 -c 'import json,sys
v,arch=sys.argv[2],sys.argv[3]
for r in json.load(open(sys.argv[1])):
    if r["stable"] and (r["version"]=="go"+v or r["version"].startswith("go"+v+".")):
        for f in r["files"]:
            if f["os"]=="linux" and f["arch"]==arch and f["kind"]=="archive":
                print(r["version"], f["sha256"], f["filename"]); sys.exit()' "$index" "$v" "$ARCH")
		[ -n "$line" ] || { warn "Go ${v} not found on go.dev; skipping."; continue; }
		read -r ver sha file <<<"$line"
		tmp=$(mktemp -d)
		download "https://go.dev/dl/${file}" "${tmp}/${file}" "$sha" || { warn "Go ${ver} download failed; skipping."; rm -rf "$tmp"; continue; }
		rm -rf "${RUNTIMES_DIR}/go/${v}"
		mkdir -p "${RUNTIMES_DIR}/go/${v}"
		tar -xzf "${tmp}/${file}" -C "${RUNTIMES_DIR}/go/${v}" --strip-components=1 --no-same-owner
		rm -rf "$tmp"
		GO_INSTALLED="${GO_INSTALLED:-} ${v}(${ver})"
	done
	rm -f "$index"
}

# Dart SDK from Google's archive, checked against its .sha256sum file.
install_dart() {
	[ -n "$DART_VERSIONS" ] || return 0
	info "Installing Dart ${DART_VERSIONS}"
	local v ver arch base file tmp minor
	mkdir -p "${RUNTIMES_DIR}/dart"
	arch="x64"; [ "$ARCH" = "arm64" ] && arch="arm64"
	for v in $DART_VERSIONS; do
		ver="$v"
		if [ "$v" = "latest" ]; then
			tmp=$(mktemp)
			download https://storage.googleapis.com/dart-archive/channels/stable/release/latest/VERSION "$tmp" \
				|| { warn "Could not reach the Dart archive; skipping Dart."; continue; }
			ver=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$tmp")
			rm -f "$tmp"
		fi
		base="https://storage.googleapis.com/dart-archive/channels/stable/release/${ver}/sdk"
		file="dartsdk-linux-${arch}-release.zip"
		tmp=$(mktemp -d)
		if ! download "${base}/${file}.sha256sum" "${tmp}/sum" \
			|| ! download "${base}/${file}" "${tmp}/${file}" "$(cut -d' ' -f1 "${tmp}/sum")"; then
			warn "Dart ${ver} download failed; skipping."
			rm -rf "$tmp"
			continue
		fi
		minor="${ver%.*}"
		rm -rf "${RUNTIMES_DIR}/dart/${minor}"
		unzip -q "${tmp}/${file}" -d "$tmp"
		mv "${tmp}/dart-sdk" "${RUNTIMES_DIR}/dart/${minor}"
		rm -rf "$tmp"
		DART_INSTALLED="${DART_INSTALLED:-} ${minor}(${ver})"
	done
}

# Docker Engine from Docker's repository (key fingerprint checked), with
# Compose and Buildx. Falls back to Ubuntu's packages for brand-new releases.
install_docker() {
	[ "$INSTALL_DOCKER" -eq 1 ] || return 0
	info "Installing Docker"
	local key=/etc/apt/keyrings/docker.asc fpr want="9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
	install -m 0755 -d /etc/apt/keyrings
	if curl -fsS --max-time 15 "https://download.docker.com/linux/ubuntu/dists/${OS_CODENAME}/Release" -o /dev/null 2>>"$LOG_FILE" \
		&& download https://download.docker.com/linux/ubuntu/gpg "$key"; then
		fpr=$(gpg --show-keys --with-colons "$key" 2>/dev/null | awk -F: '/^fpr/ {print $10; exit}')
		[ "$fpr" = "$want" ] || die "Docker's signing key has an unexpected fingerprint (${fpr}). Not installing."
		echo "deb [arch=${ARCH} signed-by=${key}] https://download.docker.com/linux/ubuntu ${OS_CODENAME} stable" >/etc/apt/sources.list.d/docker.list
		apt-get update -q >>"$LOG_FILE" 2>&1
		apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
	else
		warn "Docker has no packages for Ubuntu ${OS_VERSION} yet; using Ubuntu's docker.io."
		rm -f /etc/apt/sources.list.d/docker.list
		apt_install docker.io docker-compose-v2 docker-buildx
	fi
	mkdir -p /etc/docker
	[ -f /etc/docker/daemon.json ] || cat >/etc/docker/daemon.json <<'JSON'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true
}
JSON
	systemctl enable --now docker >>"$LOG_FILE" 2>&1
	systemctl restart docker
	DOCKER_INSTALLED="$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo installed)"
}

# Cloud Native Buildpacks CLI from GitHub, checked against its .sha256 file.
install_pack() {
	[ "$INSTALL_PACK" -eq 1 ] || return 0
	info "Installing buildpacks (pack)"
	local tag file tmp
	tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/buildpacks/pack/releases/latest 2>>"$LOG_FILE" | sed 's#.*/tag/##')
	[ -n "$tag" ] || { warn "Could not find the latest pack release; skipping."; return 0; }
	file="pack-${tag}-linux.tgz"
	[ "$ARCH" = "arm64" ] && file="pack-${tag}-linux-arm64.tgz"
	tmp=$(mktemp -d)
	if download "https://github.com/buildpacks/pack/releases/download/${tag}/${file}.sha256" "${tmp}/sum" \
		&& download "https://github.com/buildpacks/pack/releases/download/${tag}/${file}" "${tmp}/${file}" "$(cut -d' ' -f1 "${tmp}/sum")"; then
		tar -xzf "${tmp}/${file}" -C "$tmp"
		install -m 0755 "${tmp}/pack" /usr/local/bin/pack
		PACK_INSTALLED="$tag"
	else
		warn "pack download failed; buildpack deploys won't be available."
	fi
	rm -rf "$tmp"
}

# Kubernetes (k3s) as a single-node cluster. Its own ingress and load
# balancer are off: Nginx keeps ports 80 and 443 and proxies to services.
install_k3s() {
	[ "$INSTALL_K3S" -eq 1 ] || return 0
	info "Installing Kubernetes (k3s)"
	local tag enc bin sums tmp
	tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/k3s-io/k3s/releases/latest 2>>"$LOG_FILE" | sed 's#.*/tag/##')
	[ -n "$tag" ] || { warn "Could not find the latest k3s release; skipping."; return 0; }
	enc="${tag//+/%2B}"
	bin="k3s"; sums="sha256sum-amd64.txt"
	[ "$ARCH" = "arm64" ] && { bin="k3s-arm64"; sums="sha256sum-arm64.txt"; }
	tmp=$(mktemp -d)
	if ! download "https://github.com/k3s-io/k3s/releases/download/${enc}/${sums}" "${tmp}/sums" \
		|| ! download "https://github.com/k3s-io/k3s/releases/download/${enc}/${bin}" "${tmp}/k3s" "$(awk -v f="$bin" '$2==f {print $1}' "${tmp}/sums")"; then
		warn "k3s download failed; skipping Kubernetes."
		rm -rf "$tmp"
		return 0
	fi
	install -m 0755 "${tmp}/k3s" /usr/local/bin/k3s
	rm -rf "$tmp"
	cat >/etc/systemd/system/k3s.service <<'EOF'
[Unit]
Description=Kubernetes (k3s), managed by Ike
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=/usr/local/bin/k3s server --disable traefik --disable servicelb --write-kubeconfig-mode 0600
KillMode=process
Delegate=yes
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
	systemctl daemon-reload
	systemctl enable --now k3s >>"$LOG_FILE" 2>&1 || warn "k3s didn't start; check: journalctl -u k3s"
	K3S_INSTALLED="$tag"
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

install_postgres() {
	[ -n "$POSTGRES_VERSION" ] || return 0
	if [ "$POSTGRES_VERSION" = "default" ]; then info "Installing PostgreSQL (Ubuntu's version)"; else info "Installing PostgreSQL ${POSTGRES_VERSION}"; fi
	if [ "$POSTGRES_VERSION" = "default" ]; then
		apt_install postgresql postgresql-contrib
	else
		local key=/usr/share/keyrings/postgresql.gpg
		curl -fsS --max-time 30 https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor --yes -o "$key" \
			|| die "Could not download the PostgreSQL signing key"
		echo "deb [signed-by=${key}] https://apt.postgresql.org/pub/repos/apt ${OS_CODENAME}-pgdg main" >/etc/apt/sources.list.d/pgdg.list
		apt-get update -q >>"$LOG_FILE" 2>&1
		apt_install "postgresql-${POSTGRES_VERSION}" "postgresql-contrib-${POSTGRES_VERSION}" 2>/dev/null || apt_install "postgresql-${POSTGRES_VERSION}"
	fi
	POSTGRES_INSTALLED=$(find /usr/lib/postgresql -mindepth 1 -maxdepth 1 -printf "%f\n" | sort -V | tail -1)
	local hba="/etc/postgresql/${POSTGRES_INSTALLED}/main/pg_hba.conf"
	# Apps sign in with passwords over 127.0.0.1; the postgres superuser only through its own Linux user.
	grep -q "^host\s\+all\s\+all\s\+127.0.0.1/32\s\+scram-sha-256" "$hba" \
		|| echo "host    all    all    127.0.0.1/32    scram-sha-256" >>"$hba"
	systemctl enable --now postgresql >>"$LOG_FILE" 2>&1
	systemctl reload postgresql >>"$LOG_FILE" 2>&1 || true
}

has_cache() { case " $CACHE_ENGINES " in *" $1 "*) return 0 ;; esac; return 1; }

# cache_unit NAME DESCRIPTION EXEC TYPE writes a per-account systemd template.
cache_unit() {
	cat >"/etc/systemd/system/$1@.service" <<EOF
[Unit]
Description=$2 for hosting account %i
After=network.target

[Service]
Type=$4
User=%i
Group=%i
ExecStart=$3
Slice=ike-acct-%i.slice
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/home/%i/.redis
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
LimitNOFILE=65536
LimitMEMLOCK=infinity
Restart=on-failure
RestartSec=3
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
EOF
}

# install_dragonfly installs Dragonfly from its GitHub release. Its own
# service stays off: Ike runs one instance per account.
install_dragonfly() {
	local tag file tmp sum
	if [ -x /usr/bin/dragonfly ]; then
		DRAGONFLY_INSTALLED=$(/usr/bin/dragonfly --version 2>/dev/null | awk '{print $2; exit}')
		return 0
	fi
	tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/dragonflydb/dragonfly/releases/latest 2>>"$LOG_FILE" | sed 's#.*/tag/##')
	[ -n "$tag" ] || { warn "Could not find the latest Dragonfly release; skipping Dragonfly."; return 1; }
	file="dragonfly_${ARCH}.deb"
	tmp=$(mktemp -d)
	sum=""
	if download "https://github.com/dragonflydb/dragonfly/releases/download/${tag}/${file}.sha256" "${tmp}/sum"; then
		sum=$(cut -d' ' -f1 "${tmp}/sum")
	else
		warn "Dragonfly ${tag} publishes no checksum for ${file}; relying on HTTPS from GitHub."
	fi
	if ! download "https://github.com/dragonflydb/dragonfly/releases/download/${tag}/${file}" "${tmp}/${file}" "$sum"; then
		warn "Dragonfly download failed; skipping it."
		rm -rf "$tmp"
		return 1
	fi
	if ! apt_install "${tmp}/${file}"; then
		warn "Dragonfly didn't install; skipping it."
		rm -rf "$tmp"
		return 1
	fi
	rm -rf "$tmp"
	systemctl disable --now dragonfly >>"$LOG_FILE" 2>&1 || true
	# Dragonfly has no client of its own; the panel talks to it with redis-cli.
	if ! has_cache redis && ! has_cache valkey; then apt_install redis-tools; fi
	DRAGONFLY_INSTALLED="$tag"
}

install_redis() {
	[ -n "$CACHE_ENGINES" ] || return 0
	info "Installing caches for apps: ${CACHE_ENGINES} (a private instance per account)"
	install -d -m 0755 -o root -g root /etc/ike/redis
	local got="" e
	for e in $CACHE_ENGINES; do
		case "$e" in
			redis)
				apt_install redis-server redis-tools
				# With a server-wide Valkey, Redis runs only per account (port 6379 is
				# Valkey's), unless the spam filter uses it.
				if [ "$REDIS_KIND" = "valkey" ] && ! systemctl is-enabled --quiet rspamd 2>/dev/null; then
					systemctl disable --now redis-server >>"$LOG_FILE" 2>&1 || true
				fi
				got="$got redis" ;;
			valkey)
				if ! pkg_available valkey-server; then
					warn "Valkey isn't packaged for Ubuntu ${OS_VERSION}; skipped. Use --redis or --dragonfly."
					continue
				fi
				apt_install valkey-server valkey-tools 2>/dev/null || apt_install valkey-server
				# With Redis too, Redis keeps port 6379; Valkey runs only per account.
				if [ "$REDIS_KIND" != "valkey" ]; then systemctl disable --now valkey-server >>"$LOG_FILE" 2>&1 || true; fi
				got="$got valkey" ;;
			dragonfly)
				if install_dragonfly; then got="$got dragonfly"; fi ;;
		esac
	done
	CACHE_ENGINES="${got# }"
	case " $CACHE_ENGINES " in *" $REDIS_KIND "*) ;; *) REDIS_KIND="" ;; esac
	if [ "$REDIS_KIND" = "valkey" ]; then
		cache_unit ike-redis "Valkey" "/usr/bin/valkey-server /etc/ike/redis/%i.conf --supervised systemd --daemonize no" notify
	elif [ "$REDIS_KIND" = "redis" ]; then
		cache_unit ike-redis "Redis" "/usr/bin/redis-server /etc/ike/redis/%i.conf --supervised systemd --daemonize no" notify
	fi
	if has_cache redis && [ "$REDIS_KIND" != "redis" ]; then
		cache_unit ike-redis-server "Redis" "/usr/bin/redis-server /etc/ike/redis/%i.conf --supervised systemd --daemonize no" notify
	fi
	if has_cache valkey && [ "$REDIS_KIND" != "valkey" ]; then
		cache_unit ike-valkey "Valkey" "/usr/bin/valkey-server /etc/ike/redis/%i.conf --supervised systemd --daemonize no" notify
	fi
	if has_cache dragonfly; then
		cache_unit ike-dragonfly "Dragonfly" "/usr/bin/dragonfly --flagfile=/etc/ike/redis/%i.dragonfly.flags" simple
	fi
	systemctl daemon-reload
}

# ------------------------------------------------------- real-time media ----

# install_turn installs coturn. The agent writes its config and starts it
# when the admin switches TURN on in the panel.
install_turn() {
	[ "$INSTALL_TURN" -eq 1 ] || return 0
	info "Installing the TURN server (coturn)"
	apt_install coturn
	systemctl disable --now coturn >>"$LOG_FILE" 2>&1 || true
	# Older packages only start when this is set.
	if [ -f /etc/default/coturn ]; then
		sed -i 's/^#\?TURNSERVER_ENABLED=.*/TURNSERVER_ENABLED=1/' /etc/default/coturn
	fi
	install -d -m 0755 -o root -g root "${CONF_DIR}/realtime"
	install -d -m 0750 -o root -g turnserver "${CONF_DIR}/realtime/turn"
	TURN_INSTALLED=$(dpkg-query -W -f '${Version}' coturn 2>/dev/null || echo installed)
}

# install_livekit installs the LiveKit server from its GitHub release, checked
# against the release's checksum file, with a service the agent configures.
install_livekit() {
	[ "$INSTALL_LIVEKIT" -eq 1 ] || return 0
	info "Installing LiveKit"
	local tag ver file tmp sums sum
	tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/livekit/livekit/releases/latest 2>>"$LOG_FILE" | sed 's#.*/tag/##')
	[ -n "$tag" ] || { warn "Could not find the latest LiveKit release; skipping LiveKit."; return 0; }
	ver="${tag#v}"
	file="livekit_${ver}_linux_${ARCH}.tar.gz"
	tmp=$(mktemp -d)
	sum=""
	for sums in checksums.txt "livekit_${ver}_checksums.txt"; do
		if download "https://github.com/livekit/livekit/releases/download/${tag}/${sums}" "${tmp}/sums"; then
			sum=$(awk -v f="$file" '$2==f {print $1}' "${tmp}/sums")
			[ -n "$sum" ] && break
		fi
	done
	[ -n "$sum" ] || warn "LiveKit ${tag} has no checksum for ${file}; relying on HTTPS from GitHub."
	if ! download "https://github.com/livekit/livekit/releases/download/${tag}/${file}" "${tmp}/${file}" "$sum"; then
		warn "LiveKit download failed; skipping it."
		rm -rf "$tmp"
		return 0
	fi
	tar -xzf "${tmp}/${file}" -C "$tmp"
	install -m 0755 "${tmp}/livekit-server" /usr/local/bin/livekit-server
	rm -rf "$tmp"
	id livekit >/dev/null 2>&1 || useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin livekit
	install -d -m 0750 -o root -g livekit /etc/livekit
	install -d -m 0755 -o root -g root "${CONF_DIR}/realtime"
	cat >/etc/systemd/system/livekit.service <<'EOF'
[Unit]
Description=LiveKit media server, managed by Ike
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=livekit
Group=livekit
ExecStart=/usr/local/bin/livekit-server --config /etc/livekit/livekit.yaml
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
LimitNOFILE=500000
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
	systemctl daemon-reload
	LIVEKIT_INSTALLED="$tag"
}

install_mongodb() {
	[ -n "$MONGODB_VERSION" ] || return 0
	info "Installing MongoDB ${MONGODB_VERSION}"
	local key=/usr/share/keyrings/mongodb-server-${MONGODB_VERSION}.gpg
	local url="https://repo.mongodb.org/apt/ubuntu"
	if ! curl -fsS --max-time 15 "${url}/dists/${OS_CODENAME}/mongodb-org/${MONGODB_VERSION}/Release" -o /dev/null 2>>"$LOG_FILE"; then
		warn "MongoDB ${MONGODB_VERSION} has no packages for Ubuntu ${OS_VERSION} yet; skipped. Run the installer again with --mongodb once it does."
		MONGODB_VERSION=""
		return 0
	fi
	curl -fsS --max-time 30 "https://pgp.mongodb.com/server-${MONGODB_VERSION}.asc" | gpg --dearmor --yes -o "$key" || die "Could not download MongoDB's signing key"
	echo "deb [arch=amd64,arm64 signed-by=${key}] ${url} ${OS_CODENAME}/mongodb-org/${MONGODB_VERSION} multiverse" >/etc/apt/sources.list.d/mongodb-org.list
	apt-get update -q >>"$LOG_FILE" 2>&1
	apt_install mongodb-org mongodb-mongosh mongodb-database-tools
	systemctl enable --now mongod >>"$LOG_FILE" 2>&1
	local _
	for _ in $(seq 1 30); do mongosh --quiet --eval 'db.runCommand({ping:1}).ok' >/dev/null 2>&1 && break; sleep 1; done
	if [ ! -f /etc/ike/mongodb.json ]; then
		local pw
		pw=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
		# The script (with the password) goes through stdin, not the command line.
		printf 'db = connect("mongodb://127.0.0.1:27017/admin");\ndb.createUser({user: "ike-admin", pwd: "%s", roles: ["root"]});\n' "$pw" \
			| mongosh --quiet --nodb >>"$LOG_FILE" 2>&1 || die "Could not create the MongoDB admin user"
		umask 077
		printf '{"admin_password": "%s"}\n' "$pw" >/etc/ike/mongodb.json
		umask 022
	fi
	# Sign-in required from now on, and only this server can connect.
	if ! grep -q "^security:" /etc/mongod.conf; then
		printf '\nsecurity:\n  authorization: enabled\n' >>/etc/mongod.conf
	fi
	sed -i 's/^\(\s*bindIp:\).*/\1 127.0.0.1/' /etc/mongod.conf
	systemctl restart mongod
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

	if uses_nginx; then
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
	elif [ "$WEB_STACK" = "caddy" ]; then
		cat >"${CADDY_SITES_DIR}-extra/phpmyadmin.caddy" <<EOF
# phpMyAdmin on localhost only. Reach it with:  ssh -L ${PMA_PORT}:127.0.0.1:${PMA_PORT} root@this-server
http://127.0.0.1:${PMA_PORT} {
	bind 127.0.0.1
	root * ${PMA_DIR}
	@hidden path /libraries/* /setup/* /templates/* /vendor/*
	respond @hidden 403
	php_fastcgi unix//run/php/ike-pma.sock
	file_server
}
EOF
		caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile >>"$LOG_FILE" 2>&1 && systemctl reload caddy
	elif [ "$WEB_STACK" = "openlitespeed" ]; then
		cat >"${OLS_SITES_DIR}/phpmyadmin-vhost.conf" <<EOF
# phpMyAdmin on localhost only.
docRoot                   ${PMA_DIR}/
index  {
  useServer               0
  indexFiles              index.php
}
extprocessor ike_pma {
  type                    fcgi
  address                 uds://run/php/ike-pma.sock
  maxConns                4
  initTimeout             60
  retryTimeout            0
  persistConn             1
  autoStart               0
}
scripthandler  {
  add                     fcgi:ike_pma php
}
rewrite  {
  enable                  1
  rules                   <<<END_rules
RewriteRule ^/?(libraries|setup|templates|vendor)/ - [F,L]
  END_rules
}
EOF
		cat >"${OLS_SITES_DIR}/extra.conf" <<EOF
# Managed by ${PANEL_NAME}.
virtualhost ike-phpmyadmin {
  vhRoot                  ${PMA_DIR}/
  configFile              ${OLS_SITES_DIR}/phpmyadmin-vhost.conf
  allowSymbolLink         1
  enableScript            1
  restrained              1
}
listener ike-phpmyadmin {
  address                 127.0.0.1:${PMA_PORT}
  secure                  0
  map                     ike-phpmyadmin *
}
EOF
		chgrp www-data "${OLS_SITES_DIR}"/*.conf && chmod 0640 "${OLS_SITES_DIR}"/*.conf
		/usr/local/lsws/bin/openlitespeed -t >>"$LOG_FILE" 2>&1 && /usr/local/lsws/bin/lswsctrl restart >>"$LOG_FILE" 2>&1
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
	# Website SFTP users: chrooted to one website, files owned by the account.
	getent group ike-sftpsite    >/dev/null || groupadd --system ike-sftpsite
	getent group ike-sftpsite-ro >/dev/null || groupadd --system ike-sftpsite-ro
	install -d -m 0755 -o root -g root "/etc/ssh/${PANEL_NAME}" "$SSH_KEYS_DIR" "${STATE_DIR}/sftp"

	local cfg=/etc/ssh/sshd_config backup
	backup="${cfg}.${PANEL_NAME}.bak.$(date +%s)"
	cp -a "$cfg" "$backup"

	# Match blocks go at the very END of sshd_config: everything after a Match line
	# belongs to it, so appending is the only placement that can't capture the
	# admin's own settings.
	sed -i "/^# BEGIN ${PANEL_NAME}/,/^# END ${PANEL_NAME}/d" "$cfg"
	# The agent adds this block itself on servers installed before it existed.
	sed -i "/^# BEGIN ike-sftpsite/,/^# END ike-sftpsite/d" "$cfg"
	cat >>"$cfg" <<EOF
# BEGIN ${PANEL_NAME} (managed, do not edit between these markers)
# SFTP accounts are chrooted; each key in ${SSH_KEYS_DIR} forces internal-sftp
# (optionally read-only) with authorized_keys options written by Ike.
Match Group ike-sftp
    ChrootDirectory %h
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
# Website SFTP users: each sees one website folder, bind-mounted into its jail.
# Password sign-in is allowed here (designers' FTP apps); fail2ban guards it.
Match Group ike-sftpsite
    ChrootDirectory ${STATE_DIR}/sftp/%u
    ForceCommand internal-sftp
    AuthorizedKeysFile ${SSH_KEYS_DIR}/%u
    PasswordAuthentication yes
    KbdInteractiveAuthentication no
    AllowTcpForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTunnel no
    PermitTTY no
Match Group ike-sftpsite-ro
    ChrootDirectory ${STATE_DIR}/sftp/%u
    ForceCommand internal-sftp -R
    AuthorizedKeysFile ${SSH_KEYS_DIR}/%u
    PasswordAuthentication yes
    KbdInteractiveAuthentication no
    AllowTcpForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTunnel no
    PermitTTY no
# END ${PANEL_NAME}
EOF
	if ! sshd -t >>"$LOG_FILE" 2>&1; then
		cp -a "$backup" "$cfg"
		die "sshd rejected the new configuration; your original config was restored. See $LOG_FILE"
	fi
	systemctl reload ssh >>"$LOG_FILE" 2>&1 || systemctl reload sshd >>"$LOG_FILE" 2>&1 || true
}

# fw_allow PORT PROTO NOTE opens a port in the chosen firewall. With nftables
# the port is also remembered so the agent keeps it in Ike's table.
fw_allow() {
	case "$FIREWALL" in
		ufw) ufw allow "$1/$2" comment "$3" ;;
		firewalld)
			firewall-cmd --zone=public --add-port="$1/$2"
			firewall-cmd --permanent --zone=public --add-port="$1/$2" ;;
		nftables)
			case " $FW_EXTRA " in
				*" $1/$2 "*) ;;
				*) FW_EXTRA="${FW_EXTRA} $1/$2"
				   nft add rule inet ike input "$2" dport "$1" accept comment "\"$3\"" ;;
			esac ;;
	esac
}

# nft_ruleset writes Ike's starting table. The agent rewrites it from its own
# record once it runs.
nft_ruleset() {
	local p rules=""
	for p in $SSH_PORTS; do rules="${rules}		tcp dport ${p} accept comment \"SSH\"
"; done
	for p in 80 443 $PANEL_PORTS; do rules="${rules}		tcp dport ${p} accept comment \"Required\"
"; done
	install -d -m 0755 "$CONF_DIR"
	cat >"$CONF_DIR/nftables.nft" <<NFT
#!/usr/sbin/nft -f
# Written by Ike. Edit open ports and blocked addresses in the panel; changes here are overwritten.

table inet ike
delete table inet ike

table inet ike {
	set blocked4 {
		type ipv4_addr
		flags interval
		auto-merge
	}

	set blocked6 {
		type ipv6_addr
		flags interval
		auto-merge
	}

	chain input {
		type filter hook input priority filter - 10; policy drop;
		ip saddr @blocked4 drop
		ip6 saddr @blocked6 drop
		ct state established,related accept
		ct state invalid drop
		iif "lo" accept
		meta l4proto icmp accept
		meta l4proto ipv6-icmp accept
${rules}	}
}
NFT
	chmod 0600 "$CONF_DIR/nftables.nft"
}

configure_firewall() {
	info "Configuring the firewall (${FIREWALL}) and fail2ban"
	SSH_PORTS=$(sshd -T 2>/dev/null | awk '$1=="port" {print $2}')
	SSH_PORTS="${SSH_PORTS:-22}"
	local p banaction="" banaction_all=""
	case "$FIREWALL" in
	ufw)
		apt_install ufw
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
		;;
	firewalld)
		apt_install firewalld
		if command -v ufw >/dev/null 2>&1; then ufw --force disable >>"$LOG_FILE" 2>&1 || true; fi
		# firewalld's public zone already lets SSH (port 22) in; open the
		# configured SSH ports and the panel before anything else changes.
		systemctl enable --now firewalld >>"$LOG_FILE" 2>&1
		{
			firewall-cmd --set-default-zone=public
			for p in $SSH_PORTS 80 443 $PANEL_PORTS; do
				firewall-cmd --permanent --zone=public --add-port="${p}/tcp"
			done
			firewall-cmd --reload
		} >>"$LOG_FILE" 2>&1
		banaction="firewallcmd-rich-rules"
		banaction_all="firewallcmd-allports"
		;;
	nftables)
		apt_install nftables
		if command -v ufw >/dev/null 2>&1; then ufw --force disable >>"$LOG_FILE" 2>&1 || true; fi
		nft_ruleset
		nft --check --file "$CONF_DIR/nftables.nft" >>"$LOG_FILE" 2>&1 || die "nftables rejected Ike's starting rules. See $LOG_FILE"
		cat >"/etc/systemd/system/${PANEL_NAME}-firewall.service" <<UNIT
[Unit]
Description=Ike firewall rules (nftables table inet ike)
DefaultDependencies=no
Before=network-pre.target
Wants=network-pre.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft --file ${CONF_DIR}/nftables.nft
ExecReload=/usr/sbin/nft --file ${CONF_DIR}/nftables.nft
ExecStop=/usr/sbin/nft delete table inet ike

[Install]
WantedBy=sysinit.target
UNIT
		systemctl daemon-reload
		systemctl enable --now "${PANEL_NAME}-firewall" >>"$LOG_FILE" 2>&1 || die "Could not load the nftables rules. See $LOG_FILE"
		banaction="nftables-multiport"
		banaction_all="nftables-allports"
		;;
	esac

	local jails=""
	if [ -n "$banaction" ]; then
		jails="[DEFAULT]
banaction = ${banaction}
banaction_allports = ${banaction_all}

"
	fi
	jails="${jails}[sshd]
enabled  = true
maxretry = 5
findtime = 10m
bantime  = 1h"
	if uses_nginx; then
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

# ------------------------------------------------------------ identity ----

detect_identity() {
	SERVER_HOSTNAME="${SERVER_HOSTNAME:-$(hostname -f 2>/dev/null || hostname)}"
	PUBLIC_IPV4="${PUBLIC_IPV4:-$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src") print $(i+1)}' | head -1)}"
	PUBLIC_IPV6="${PUBLIC_IPV6:-$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src") print $(i+1)}' | head -1)}"
	case "$PUBLIC_IPV4" in 10.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[01].*|100.6[4-9].*|100.[7-9][0-9].*|100.1[01][0-9].*|100.12[0-7].*)
		# behind NAT: ask the outside world
		PUBLIC_IPV4=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || echo "$PUBLIC_IPV4") ;;
	esac
	case "$PUBLIC_IPV6" in fe80:*|fd*|fc*|"") PUBLIC_IPV6="" ;; esac
}

# ------------------------------------------------------------------ DNS ----

install_dns() {
	[ "$INSTALL_DNS" -eq 1 ] || return 0
	info "Installing the DNS server (PowerDNS, bind backend)"
	# systemd-resolved listens on 127.0.0.53:53 only, so PowerDNS can take the public addresses.
	apt_install pdns-server pdns-backend-bind
	install -d -m 0750 -o root -g pdns "${STATE_DIR}/dns" "${STATE_DIR}/dns/zones"
	install -d -m 0770 -o root -g pdns "${STATE_DIR}/dns/secondary"
	install -d -m 0700 -o root -g root "${STATE_DIR}/dns/state"
	[ -f /etc/powerdns/ike-zones.conf ] || : >/etc/powerdns/ike-zones.conf
	chmod 0644 /etc/powerdns/ike-zones.conf
	local listen="0.0.0.0"
	[ -s /proc/net/if_inet6 ] && listen="0.0.0.0, ::"
	cat >/etc/powerdns/pdns.d/ike.conf <<EOF
# Written by the Ike installer. Zones are managed in the panel.
launch=bind
bind-config=/etc/powerdns/ike-zones.conf
bind-dnssec-db=/var/lib/powerdns/bind-dnssec-db.sqlite3
local-address=${listen}
primary=yes
secondary=yes
default-ttl=3600
version-string=anonymous
max-tcp-connections=200
EOF
	# The Ubuntu package ships a bind.conf that loads the system named.conf.
	rm -f /etc/powerdns/pdns.d/bind.conf /etc/powerdns/pdns.d/pdns.local.gsqlite3.conf
	if [ ! -f /var/lib/powerdns/bind-dnssec-db.sqlite3 ]; then
		install -d -m 0750 -o pdns -g pdns /var/lib/powerdns
		pdnsutil create-bind-db /var/lib/powerdns/bind-dnssec-db.sqlite3 >>"$LOG_FILE" 2>&1 || die "PowerDNS DNSSEC database could not be created"
		chown pdns:pdns /var/lib/powerdns/bind-dnssec-db.sqlite3
	fi
	systemctl enable pdns >>"$LOG_FILE" 2>&1
	systemctl restart pdns || die "PowerDNS did not start. Check: journalctl -u pdns"
	fw_allow 53 tcp 'DNS' >>"$LOG_FILE" 2>&1
	fw_allow 53 udp 'DNS' >>"$LOG_FILE" 2>&1
}

# ------------------------------------------------------------- security ----

install_modsecurity() {
	[ "$INSTALL_MODSEC" -eq 1 ] || return 0
	info "Installing the web application firewall (ModSecurity, OWASP Core Rule Set)"
	apt_install modsecurity-crs
	install -d -m 0755 -o root -g root "${CONF_DIR}/modsec"
	if uses_nginx; then
		apt_install libnginx-mod-http-modsecurity
		cat >"${CONF_DIR}/modsec/main.conf" <<EOF
# Written by the Ike installer: shared by every website with the firewall on.
Include /etc/nginx/modsecurity.conf
SecRuleEngine DetectionOnly
SecRequestBodyLimit 1073741824
SecRequestBodyLimitAction ProcessPartial
SecResponseBodyAccess Off
SecAuditEngine RelevantOnly
SecAuditLogRelevantStatus "^(?:5|4(?!04))"
SecAuditLogParts ABIJDEFHZ
SecAuditLogType Serial
SecAuditLog ${PANEL_LOG_DIR}/modsec_audit.log
Include /etc/modsecurity/crs/crs-setup.conf
Include /usr/share/modsecurity-crs/rules/*.conf
EOF
		nginx -t >>"$LOG_FILE" 2>&1 || die "Nginx rejected the ModSecurity module; see ${LOG_FILE}"
		systemctl reload nginx
	fi
	if uses_apache; then
		apt_install libapache2-mod-security2
		[ -f /etc/modsecurity/modsecurity.conf ] || cp /etc/modsecurity/modsecurity.conf-recommended /etc/modsecurity/modsecurity.conf
		# Off for the whole server; each website turns it on in its own vhost.
		sed -i 's/^SecRuleEngine .*/SecRuleEngine Off/; s/^SecResponseBodyAccess .*/SecResponseBodyAccess Off/; s/^SecRequestBodyLimitAction .*/SecRequestBodyLimitAction ProcessPartial/' /etc/modsecurity/modsecurity.conf
		a2enmod security2 >>"$LOG_FILE" 2>&1
		apache2ctl configtest >>"$LOG_FILE" 2>&1 || die "Apache rejected ModSecurity; see ${LOG_FILE}"
		systemctl restart apache2
	fi
}

install_scanner() {
	[ "$INSTALL_SCANNER" -eq 1 ] || [ "$INSTALL_CLAMAV" -eq 1 ] || return 0
	info "Installing ClamAV for malware scans"
	apt_install clamav clamav-freshclam
	systemctl enable --now clamav-freshclam >>"$LOG_FILE" 2>&1 || true
	install -d -m 0700 -o root -g root "${STATE_DIR}/quarantine" "${STATE_DIR}/scans" "${STATE_DIR}/scans/reports"
}

# ----------------------------------------------------------------- mail ----

install_mail() {
	[ "$INSTALL_MAIL" -eq 1 ] || return 0
	info "Installing email hosting (Postfix, Dovecot$( [ "$INSTALL_RSPAMD" = 1 ] && echo ", Rspamd")$( [ "$INSTALL_CLAMAV" = 1 ] && echo ", ClamAV"))"
	local host="$SERVER_HOSTNAME"
	echo "postfix postfix/main_mailer_type select Internet Site" | debconf-set-selections
	echo "postfix postfix/mailname string ${host}" | debconf-set-selections
	apt_install postfix postfix-lmdb dovecot-core dovecot-imapd dovecot-pop3d dovecot-lmtpd dovecot-sieve dovecot-managesieved
	[ "$INSTALL_RSPAMD" = 1 ] && apt_install rspamd redis-server
	[ "$INSTALL_CLAMAV" = 1 ] && apt_install clamav-daemon clamav-freshclam

	getent group vmail >/dev/null || groupadd --system vmail
	getent passwd vmail >/dev/null || useradd --system -g vmail -d /var/mail/vhosts -s /usr/sbin/nologin vmail
	local vuid vgid
	vuid=$(id -u vmail); vgid=$(id -g vmail)
	install -d -m 0770 -o vmail -g vmail /var/mail/vhosts
	install -d -m 0755 -o root -g root "${CONF_DIR}/mail" /etc/dovecot/sieve
	install -d -m 0700 -o root -g root "${STATE_DIR}/mail" "${STATE_DIR}/mail/domains"
	local f
	for f in domains mailboxes aliases senders sni dkim_selectors; do
		[ -f "${CONF_DIR}/mail/${f}" ] || : >"${CONF_DIR}/mail/${f}"
	done
	[ -f "${CONF_DIR}/mail/users" ] || : >"${CONF_DIR}/mail/users"
	[ -f "${CONF_DIR}/mail/relay_password" ] || : >"${CONF_DIR}/mail/relay_password"
	chown root:dovecot "${CONF_DIR}/mail/users"; chmod 0640 "${CONF_DIR}/mail/users"
	chmod 0600 "${CONF_DIR}/mail/relay_password"
	postmap -F "lmdb:${CONF_DIR}/mail/sni"

	# Default certificate: the server's own Let's Encrypt certificate when it
	# exists, otherwise a self-signed one until a domain gets its own.
	local cert="/etc/ssl/certs/ssl-cert-snakeoil.pem" key="/etc/ssl/private/ssl-cert-snakeoil.key"
	if [ -f "/etc/letsencrypt/live/${host}/fullchain.pem" ]; then
		cert="/etc/letsencrypt/live/${host}/fullchain.pem"; key="/etc/letsencrypt/live/${host}/privkey.pem"
	elif [ ! -f "$cert" ]; then
		apt_install ssl-cert
		make-ssl-cert generate-default-snakeoil >>"$LOG_FILE" 2>&1
	fi

	local milter=""
	[ "$INSTALL_RSPAMD" = 1 ] && milter="inet:127.0.0.1:11332"
	postconf -e \
		"myhostname = ${host}" \
		"myorigin = \$myhostname" \
		"mydestination = localhost" \
		"mynetworks = 127.0.0.0/8 [::ffff:127.0.0.0]/104 [::1]/128" \
		"inet_interfaces = all" \
		"inet_protocols = all" \
		"smtpd_banner = \$myhostname ESMTP" \
		"biff = no" \
		"append_dot_mydomain = no" \
		"recipient_delimiter = +" \
		"mailbox_size_limit = 0" \
		"message_size_limit = 52428800" \
		"smtpd_tls_cert_file = ${cert}" \
		"smtpd_tls_key_file = ${key}" \
		"smtpd_tls_security_level = may" \
		"smtpd_tls_auth_only = yes" \
		"smtpd_tls_mandatory_protocols = >=TLSv1.2" \
		"smtpd_tls_protocols = >=TLSv1.2" \
		"smtp_tls_security_level = may" \
		"smtp_tls_loglevel = 1" \
		"tls_server_sni_maps = lmdb:${CONF_DIR}/mail/sni" \
		"virtual_mailbox_domains = texthash:${CONF_DIR}/mail/domains" \
		"virtual_mailbox_maps = texthash:${CONF_DIR}/mail/mailboxes" \
		"virtual_alias_maps = texthash:${CONF_DIR}/mail/aliases" \
		"virtual_transport = lmtp:unix:private/dovecot-lmtp" \
		"smtpd_sasl_type = dovecot" \
		"smtpd_sasl_path = private/auth" \
		"smtpd_sasl_auth_enable = no" \
		"smtpd_sender_login_maps = texthash:${CONF_DIR}/mail/senders" \
		"smtpd_helo_required = yes" \
		"disable_vrfy_command = yes" \
		"strict_rfc821_envelopes = yes" \
		"smtpd_relay_restrictions = permit_mynetworks, permit_sasl_authenticated, defer_unauth_destination" \
		"smtpd_recipient_restrictions = permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination, reject_unknown_recipient_domain, check_policy_service inet:127.0.0.1:12340" \
		"smtpd_milters = ${milter}" \
		"non_smtpd_milters = ${milter}" \
		"milter_default_action = accept" \
		"milter_protocol = 6" \
		"relayhost =" \
		"smtp_sasl_auth_enable = no" \
		"compatibility_level = 3.6" >>"$LOG_FILE" 2>&1 || die "Postfix configuration failed"
	# Submission (587, STARTTLS) and submissions (465, TLS): signed-in users only,
	# and only as addresses they own.
	local sub_opts="-o syslog_name=postfix/submission -o smtpd_tls_security_level=encrypt -o smtpd_sasl_auth_enable=yes \
-o smtpd_tls_auth_only=yes -o smtpd_reject_unlisted_recipient=no \
-o smtpd_client_restrictions=permit_sasl_authenticated,reject \
-o smtpd_sender_restrictions=reject_sender_login_mismatch \
-o smtpd_relay_restrictions=permit_sasl_authenticated,reject \
-o smtpd_recipient_restrictions=permit_sasl_authenticated,reject \
-o milter_macro_daemon_name=ORIGINATING"
	# shellcheck disable=SC2086
	postconf -M "submission/inet=submission inet n - y - - smtpd ${sub_opts}" >>"$LOG_FILE" 2>&1
	# shellcheck disable=SC2086
	postconf -M "submissions/inet=submissions inet n - y - - smtpd ${sub_opts/syslog_name=postfix\/submission/syslog_name=postfix\/submissions} -o smtpd_tls_wrappermode=yes" >>"$LOG_FILE" 2>&1
	postfix check >>"$LOG_FILE" 2>&1 || die "Postfix rejected its configuration; see ${LOG_FILE}"

	local dv
	dv=$(dovecot --version 2>/dev/null | cut -d' ' -f1)
	DOVECOT_LISTEN="*"
	[ -s /proc/net/if_inet6 ] && DOVECOT_LISTEN="*, ::"
	[ -f /etc/dovecot/dovecot.conf.ike-orig ] || cp /etc/dovecot/dovecot.conf /etc/dovecot/dovecot.conf.ike-orig
	: >/etc/dovecot/conf.d/99-ike-sni.conf
	case "$dv" in
		2.3*) write_dovecot23 "$cert" "$key" "$vuid" "$vgid" ;;
		*) write_dovecot24 "$cert" "$key" "$vuid" "$vgid" "$dv" ;;
	esac
	cat >/etc/dovecot/sieve/ike-junk.sieve <<'EOF'
# Written by Ike: spam goes to Junk.
require ["fileinto", "mailbox"];
if anyof (header :contains "X-Spam" "Yes", header :contains "X-Spam-Flag" "YES") {
  fileinto :create "Junk";
  stop;
}
EOF
	sievec /etc/dovecot/sieve/ike-junk.sieve >>"$LOG_FILE" 2>&1 || true
	doveconf -n >/dev/null 2>>"$LOG_FILE" || die "Dovecot ${dv} rejected its configuration; see ${LOG_FILE}"

	if [ "$INSTALL_RSPAMD" = 1 ]; then
		install -d -m 0750 -o _rspamd -g _rspamd /var/lib/rspamd/dkim
		cat >/etc/rspamd/local.d/worker-proxy.inc <<'EOF'
# Written by Ike: Postfix talks to Rspamd as a milter.
milter = yes;
timeout = 120s;
upstream "local" {
  default = yes;
  self_scan = yes;
}
bind_socket = "127.0.0.1:11332";
EOF
		cat >/etc/rspamd/local.d/milter_headers.conf <<'EOF'
# Written by Ike
use = ["x-spamd-bar", "x-spam-level", "authentication-results"];
extended_spam_headers = false;
EOF
		cat >/etc/rspamd/local.d/redis.conf <<'EOF'
servers = "127.0.0.1:6379";
EOF
		cat >/etc/rspamd/local.d/dkim_signing.conf <<EOF
# Written by Ike
enabled = true;
sign_authenticated = true;
sign_local = true;
use_domain = "header";
allow_username_mismatch = true;
selector_map = "${CONF_DIR}/mail/dkim_selectors";
path = "/var/lib/rspamd/dkim/\$domain.\$selector.key";
EOF
		cp /etc/rspamd/local.d/dkim_signing.conf /etc/rspamd/local.d/arc.conf
		if [ "$INSTALL_CLAMAV" = 1 ]; then
			cat >/etc/rspamd/local.d/antivirus.conf <<'EOF'
# Written by Ike
clamav {
  scan_mime_parts = true;
  symbol = "CLAM_VIRUS";
  type = "clamav";
  action = "reject";
  servers = "/run/clamav/clamd.ctl";
}
EOF
			systemctl enable --now clamav-freshclam >>"$LOG_FILE" 2>&1 || true
			systemctl enable clamav-daemon >>"$LOG_FILE" 2>&1 || true
			# clamd starts once freshclam has downloaded signatures (a few minutes)
			systemctl start clamav-daemon >>"$LOG_FILE" 2>&1 || warn "ClamAV starts once virus signatures are downloaded"
		fi
		rspamadm configtest >>"$LOG_FILE" 2>&1 || die "Rspamd rejected its configuration; see ${LOG_FILE}"
		systemctl enable --now redis-server rspamd >>"$LOG_FILE" 2>&1
		systemctl restart rspamd
	fi
	systemctl enable postfix dovecot >>"$LOG_FILE" 2>&1
	systemctl restart dovecot || die "Dovecot did not start. Check: journalctl -u dovecot"
	systemctl restart postfix || die "Postfix did not start. Check: journalctl -u postfix"

	local p
	for p in 25 465 587 143 993 110 995 4190; do fw_allow "$p" tcp 'Mail' >>"$LOG_FILE" 2>&1; done
	cat >/etc/fail2ban/jail.d/${PANEL_NAME}-mail.conf <<'EOF'
[postfix-sasl]
enabled  = true
backend  = systemd
maxretry = 5
bantime  = 1h

[dovecot]
enabled  = true
backend  = systemd
maxretry = 5
bantime  = 1h
EOF
	systemctl restart fail2ban >>"$LOG_FILE" 2>&1 || true
	if ! timeout 6 bash -c 'exec 3<>/dev/tcp/gmail-smtp-in.l.google.com/25' 2>/dev/null; then
		warn "Outgoing port 25 is blocked by your VPS provider. Set up an SMTP relay in the panel (Email → Settings) or ask them to unblock it."
	fi
	install_webmail
}

write_dovecot23() { # cert key uid gid
	cat >/etc/dovecot/dovecot.conf <<EOF
# Written by the Ike installer (Dovecot 2.3). Mailboxes are managed in the panel.
protocols = imap pop3 lmtp sieve
listen = ${DOVECOT_LISTEN}
log_path = syslog
mail_location = maildir:~/Maildir
mail_uid = vmail
mail_gid = vmail
first_valid_uid = ${3}
last_valid_uid = ${3}
mail_privileged_group = mail
mail_plugins = \$mail_plugins quota

ssl = required
ssl_cert = <${1}
ssl_key = <${2}
ssl_min_protocol = TLSv1.2
ssl_prefer_server_ciphers = yes

auth_mechanisms = plain login
disable_plaintext_auth = yes
passdb {
  driver = passwd-file
  args = scheme=SHA512-CRYPT username_format=%u ${CONF_DIR}/mail/users
}
userdb {
  driver = passwd-file
  args = username_format=%u ${CONF_DIR}/mail/users
  default_fields = uid=vmail gid=vmail
}

namespace inbox {
  inbox = yes
  separator = /
  mailbox Drafts {
    special_use = \Drafts
    auto = subscribe
  }
  mailbox Junk {
    special_use = \Junk
    auto = subscribe
    autoexpunge = 30d
  }
  mailbox Sent {
    special_use = \Sent
    auto = subscribe
  }
  mailbox Trash {
    special_use = \Trash
    auto = subscribe
    autoexpunge = 30d
  }
  mailbox Archive {
    special_use = \Archive
    auto = subscribe
  }
}

service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
  unix_listener auth-userdb {
    mode = 0600
    user = vmail
  }
}
service auth-worker {
  user = \$default_internal_user
}
service quota-status {
  executable = quota-status -p postfix
  inet_listener {
    address = 127.0.0.1
    port = 12340
  }
  client_limit = 1
}
service managesieve-login {
  inet_listener sieve {
    port = 4190
  }
}

protocol lmtp {
  mail_plugins = \$mail_plugins sieve
  postmaster_address = postmaster@${SERVER_HOSTNAME}
}
protocol imap {
  mail_plugins = \$mail_plugins imap_quota
  mail_max_userip_connections = 30
}

plugin {
  quota = maildir:User quota
  quota_grace = 10%%
  quota_status_success = DUNNO
  quota_status_nouser = DUNNO
  quota_status_overquota = "552 5.2.2 Mailbox is full"
  sieve = file:~/sieve;active=~/.dovecot.sieve
  sieve_before = /etc/dovecot/sieve/ike-junk.sieve
  sieve_max_redirects = 10
}

!include_try conf.d/99-ike-sni.conf
EOF
}

write_dovecot24() { # cert key uid gid version
	cat >/etc/dovecot/dovecot.conf <<EOF
# Written by the Ike installer (Dovecot 2.4). Mailboxes are managed in the panel.
dovecot_config_version = ${5}
dovecot_storage_version = ${5}
protocols {
  imap = yes
  pop3 = yes
  lmtp = yes
  sieve = yes
}
listen = ${DOVECOT_LISTEN}
log_path = syslog
mail_driver = maildir
mail_path = ~/Maildir
mail_uid = vmail
mail_gid = vmail
first_valid_uid = ${3}
last_valid_uid = ${3}
mail_privileged_group = mail
mail_plugins {
  quota = yes
}

ssl = required
ssl_server_cert_file = ${1}
ssl_server_key_file = ${2}
ssl_min_protocol = TLSv1.2

auth_mechanisms = plain login
auth_allow_cleartext = no
passdb passwd-file {
  passwd_file_path = ${CONF_DIR}/mail/users
  default_password_scheme = SHA512-CRYPT
}
userdb passwd-file {
  passwd_file_path = ${CONF_DIR}/mail/users
  fields {
    uid = vmail
    gid = vmail
  }
}

namespace inbox {
  inbox = yes
  separator = /
  mailbox Drafts {
    special_use = \Drafts
    auto = subscribe
  }
  mailbox Junk {
    special_use = \Junk
    auto = subscribe
    autoexpunge = 30d
  }
  mailbox Sent {
    special_use = \Sent
    auto = subscribe
  }
  mailbox Trash {
    special_use = \Trash
    auto = subscribe
    autoexpunge = 30d
  }
  mailbox Archive {
    special_use = \Archive
    auto = subscribe
  }
}

service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
service quota-status {
  executable = quota-status postfix
  inet_listener quota-status {
    listen = 127.0.0.1
    port = 12340
  }
  client_limit = 1
}

quota "User quota" {
  driver = maildir
}
quota_status_success = DUNNO
quota_status_nouser = DUNNO
quota_status_overquota = "552 5.2.2 Mailbox is full"

protocol lmtp {
  mail_plugins {
    sieve = yes
  }
  postmaster_address = postmaster@${SERVER_HOSTNAME}
}
protocol imap {
  mail_plugins {
    imap_quota = yes
  }
}

sieve_script personal {
  driver = file
  path = ~/sieve
  active_path = ~/.dovecot.sieve
}
sieve_script ike-junk {
  type = before
  path = /etc/dovecot/sieve/ike-junk.sieve
}

!include_try conf.d/99-ike-sni.conf
EOF
}

install_webmail() {
	[ "$INSTALL_WEBMAIL" = 1 ] || return 0
	info "Installing webmail (Roundcube on port ${WEBMAIL_PORT})"
	local php="${DEFAULT_PHP}"
	[ -n "$php" ] || php=$(echo "$PHP_INSTALLED" | tr ' ' '\n' | sort -V | tail -1)
	[ -n "$php" ] || { warn "Webmail needs PHP; skipped"; return 0; }
	echo "roundcube-core roundcube/dbconfig-install boolean true" | debconf-set-selections
	echo "roundcube-core roundcube/database-type select sqlite3" | debconf-set-selections
	echo "roundcube-core roundcube/restart-webserver boolean false" | debconf-set-selections
	echo "roundcube-core roundcube/reconfigure-webserver multiselect" | debconf-set-selections
	apt_install roundcube-core roundcube-sqlite3 roundcube-plugins "php${php}-intl" "php${php}-sqlite3" "php${php}-mbstring" "php${php}-xml" "php${php}-zip" "php${php}-gd" "php${php}-curl"
	getent passwd ike-webmail >/dev/null || useradd --system --no-create-home -d /var/lib/roundcube -s /usr/sbin/nologin -G www-data ike-webmail
	chown -R ike-webmail:www-data /var/lib/roundcube /var/log/roundcube 2>/dev/null || true
	[ -d /etc/roundcube ] && chgrp -R ike-webmail /etc/roundcube && chmod -R g+r /etc/roundcube
	cat >/etc/roundcube/config.inc.php.ike <<'EOF'
<?php
// Written by Ike. Local changes go in /etc/roundcube/ike-local.inc.php.
$config['imap_host'] = 'ssl://127.0.0.1:993';
$config['smtp_host'] = 'tls://127.0.0.1:587';
$config['smtp_user'] = '%u';
$config['smtp_pass'] = '%p';
$config['imap_conn_options'] = $config['smtp_conn_options'] = ['ssl' => ['verify_peer' => false, 'verify_peer_name' => false]];
$config['product_name'] = 'Webmail';
$config['login_autocomplete'] = 2;
$config['ip_check'] = true;
$config['session_lifetime'] = 60;
$config['plugins'] = ['archive', 'zipdownload', 'managesieve', 'markasjunk', 'newmail_notifier'];
$config['managesieve_host'] = 'tls://127.0.0.1';
$config['managesieve_conn_options'] = $config['imap_conn_options'];
$config['skin'] = 'elastic';
$config['enable_installer'] = false;
@include '/etc/roundcube/ike-local.inc.php';
EOF
	if ! grep -q "config.inc.php.ike" /etc/roundcube/config.inc.php 2>/dev/null; then
		printf "\n@include '/etc/roundcube/config.inc.php.ike';\n" >>/etc/roundcube/config.inc.php
	fi
	cat >"/etc/php/${php}/fpm/pool.d/ike-webmail.conf" <<EOF
; Written by Ike: Roundcube webmail
[ike-webmail]
user = ike-webmail
group = ike-webmail
listen = /run/php/ike-webmail.sock
listen.owner = ${WEB_USER}
listen.group = ${WEB_GROUP}
listen.mode = 0660
pm = ondemand
pm.max_children = 10
pm.process_idle_timeout = 30s
php_admin_value[upload_max_filesize] = 50M
php_admin_value[post_max_size] = 55M
php_admin_value[open_basedir] = /usr/share/roundcube:/var/lib/roundcube:/etc/roundcube:/var/log/roundcube:/usr/share/php:/usr/share/javascript:/tmp
EOF
	systemctl restart "php${php}-fpm" >>"$LOG_FILE" 2>&1
	fw_allow "$WEBMAIL_PORT" tcp 'Webmail' >>"$LOG_FILE" 2>&1
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
	info "Installing ${AGENT_NAME}, ${MCP_NAME} and the ike command"
	getent group "$PANEL_NAME" >/dev/null || groupadd --system "$PANEL_NAME"
	install -d -m 0755 -o root -g root "$CONF_DIR" "${CONF_DIR}/pages" "${OPT_DIR}" "$RUNTIMES_DIR" \
		"${RUNTIMES_DIR}/node" "${RUNTIMES_DIR}/python"
	install -d -m 0711 -o root -g root "$STATE_DIR"
	install -d -m 0700 -o root -g root "${CONF_DIR}/apps" "$STATE_DIR/sites" "$STATE_DIR/accounts" "$STATE_DIR/imports" \
		"$STATE_DIR/git" "${CONF_DIR}/git" "${CONF_DIR}/ssl" "$STATE_DIR/deploys" "$STATE_DIR/stacks"
	install -d -m 0711 -o root -g root "$STATE_DIR/deploy-tmp"
	install -d -m 0755 -o root -g root "$STATE_DIR/acme" "$STATE_DIR/acme/.well-known" "$STATE_DIR/acme/.well-known/acme-challenge"
	install -d -m 0750 -o root -g adm "${PANEL_LOG_DIR}/deploys"
	install -d -m 0755 -o root -g root "${RUNTIMES_DIR}/java" "${RUNTIMES_DIR}/go" "${RUNTIMES_DIR}/dart"
	install -d -m 0750 -o root -g adm "$PANEL_LOG_DIR"
	install -d -m 0711 -o root -g root /var/backups/ike /var/backups/ike/snapshots "$STATE_DIR/data" "$STATE_DIR/shares"
	install -d -m 0700 -o root -g root /var/backups/ike/databases "$STATE_DIR/specs" "${CONF_DIR}/cluster"

	fetch_binary "$AGENT_NAME" "/usr/local/bin/${AGENT_NAME}" "$AGENT_BINARY"
	fetch_binary "$MCP_NAME" "/usr/local/bin/${MCP_NAME}" "$MCP_BINARY"
	# The ike CLI: as root on the server it talks to the agent socket directly.
	fetch_binary "ike" "/usr/local/bin/ike" "$CLI_BINARY"

	local protected="" p
	for p in $SSH_PORTS 80 443 $PANEL_PORTS; do protected="${protected}\"${p}/tcp\", "; done
	for p in $FW_EXTRA; do protected="${protected}\"${p}\", "; done
	cat >"$CONF_DIR/agent.json" <<EOF
{
  "web_stack": "${WEB_STACK}",
  "default_web_server": "${DEFAULT_WEB_SERVER}",
  "web_user": "${WEB_USER}",
  "web_group": "${WEB_GROUP}",
  "nginx_sites_dir": "${NGINX_SITES_DIR}",
  "caddy_sites_dir": "${CADDY_SITES_DIR}",
  "ols_sites_dir": "${OLS_SITES_DIR}",
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
  "admin_email": "${ADMIN_EMAIL}",
  "hostname": "${SERVER_HOSTNAME}",
  "public_ipv4": "${PUBLIC_IPV4}",
  "public_ipv6": "${PUBLIC_IPV6}",
  "dns_server": "$( [ "$INSTALL_DNS" -eq 1 ] && echo powerdns )",
  "dns_dir": "${STATE_DIR}/dns",
  "mail_server": "$( [ "$INSTALL_MAIL" -eq 1 ] && echo postfix )",
  "mail_state": "${STATE_DIR}/mail",
  "mail_conf_dir": "${CONF_DIR}/mail",
  "spam_filter": "$( [ "$INSTALL_RSPAMD" -eq 1 ] && echo rspamd )",
  "webmail": "$( [ "$INSTALL_WEBMAIL" -eq 1 ] && echo roundcube )",
  "postgres": $( [ -n "$POSTGRES_VERSION" ] && echo true || echo false ),
  "postgres_version": "${POSTGRES_INSTALLED:-}",
  "redis": "${REDIS_KIND}",
  "cache_engines": [$(for e in $CACHE_ENGINES; do printf '"%s",' "$e"; done | sed 's/,$//')],
  "mongodb": $( [ -n "$MONGODB_VERSION" ] && echo true || echo false ),
  "data_dir": "${STATE_DIR}/data",
  "modsecurity": $( [ "$INSTALL_MODSEC" -eq 1 ] && echo true || echo false ),
  "antivirus": "$( { [ "$INSTALL_CLAMAV" -eq 1 ] || [ "$INSTALL_SCANNER" -eq 1 ]; } && echo clamav )",
  "disk_quotas": $( [ "$QUOTAS_ACTIVE" -eq 1 ] && echo true || echo false ),
  "docker_for_accounts": $( [ "$DOCKER_FOR_ACCOUNTS" -eq 1 ] && echo true || echo false ),
  "firewall": "${FIREWALL}",
  "firewall_ruleset": "${CONF_DIR}/nftables.nft",
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
	install -d -m 0700 -o ike -g "$PANEL_NAME" "${STATE_DIR}/panel" "${STATE_DIR}/panel/cluster" "${CONF_DIR}/tls"
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
  "cluster_dir": "${STATE_DIR}/panel/cluster",
  "public_ipv4": "${PUBLIC_IPV4}",
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

join_cluster() {
	[ -n "$JOIN_URL" ] || return 0
	info "Joining the panel at ${JOIN_URL}"
	/usr/local/bin/${AGENT_NAME} join "$JOIN_URL" "$JOIN_TOKEN" "$JOIN_FINGERPRINT" \
		|| warn "Joining failed. Fix the problem shown above, make a new token in the panel and run: sudo ${AGENT_NAME} join ${JOIN_URL} <token> <fingerprint>"
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
  Java          ${JAVA_INSTALLED:- none}
  Go            ${GO_INSTALLED:- none}
  Dart          ${DART_INSTALLED:- none}
  Composer      $( [ -n "${COMPOSER_INSTALLED:-}" ] && echo installed || echo "not installed" )
  Docker        ${DOCKER_INSTALLED:-not installed}$( [ -n "${PACK_INSTALLED:-}" ] && echo ", buildpacks ${PACK_INSTALLED}" )$( [ "$DOCKER_FOR_ACCOUNTS" -eq 1 ] && echo " (account owners can run containers)" )
  Kubernetes    ${K3S_INSTALLED:-not installed}
  MariaDB       $( [ "$INSTALL_MARIADB" -eq 1 ] && echo installed || echo skipped )
  phpMyAdmin    ${PMA_INSTALLED:-not installed}$( [ -n "${PMA_INSTALLED:-}" ] && echo " — ssh -L ${PMA_PORT}:127.0.0.1:${PMA_PORT} root@${ip}, then open http://localhost:${PMA_PORT}" )
  Caches        ${CACHE_ENGINES:-none}$( [ -n "${DRAGONFLY_INSTALLED:-}" ] && echo " (Dragonfly ${DRAGONFLY_INSTALLED})" )
  TURN          $( [ -n "${TURN_INSTALLED:-}" ] && echo "coturn ${TURN_INSTALLED}, switch it on under Real-time media" || echo "not installed" )
  LiveKit       $( [ -n "${LIVEKIT_INSTALLED:-}" ] && echo "${LIVEKIT_INSTALLED}, switch it on under Real-time media" || echo "not installed" )
  Server IP     ${ip}
  Firewall      ${FIREWALL}
  Package tools${PKG_TOOLS_INSTALLED:- none}
  Install log   ${LOG_FILE}

${C_BLD}Sign in to the panel${C_RST} (browsers warn about the panel's self-signed certificate; websites get free Let's Encrypt certificates from their HTTPS tab):

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

# --------------------------------------------------------------- quotas ----

# install_quotas turns on journaled user quotas for the filesystem that holds
# the account homes, so the agent's setquota limits are enforced.
#   ext4:  fstab gets usrjquota=aquota.user,jqfmt=vfsv1, then remount, quotacheck, quotaon.
#   xfs:   needs the uquota mount option at boot (rootflags for /), so a reboot.
#   other: not supported; disk limits are shown but not enforced.
install_quotas() {
	[ "$DISK_QUOTAS" -eq 1 ] || return 0
	local mnt fstype
	read -r mnt fstype < <(findmnt -n -o TARGET,FSTYPE --target /home)
	info "Disk quotas for /home (${fstype} on ${mnt})"
	if quotaon -pu "$mnt" 2>/dev/null | grep -q " is on"; then
		QUOTAS_ACTIVE=1
		return 0
	fi
	case "$fstype" in
	ext4|ext3)
		if ! modprobe quota_v2 2>>"$LOG_FILE"; then
			apt_install "linux-modules-extra-$(uname -r)" || true
			modprobe quota_v2 2>>"$LOG_FILE" || { warn "This kernel has no quota support; disk limits will be shown but not enforced."; return 0; }
		fi
		cp -a /etc/fstab "/etc/fstab.ike-$(date +%Y%m%d%H%M%S)"
		# add the options to the fstab line for this mount point (column 2)
		if ! awk -v m="$mnt" '$2==m && $1 !~ /^#/ {found=1} END {exit !found}' /etc/fstab; then
			warn "${mnt} isn't listed in /etc/fstab; add usrjquota=aquota.user,jqfmt=vfsv1 to its options yourself to enable quotas."
			return 0
		fi
		if ! awk -v m="$mnt" '$2==m && $1 !~ /^#/ && $4 ~ /usrjquota/ {found=1} END {exit !found}' /etc/fstab; then
			awk -v m="$mnt" 'BEGIN{OFS="\t"} $2==m && $1 !~ /^#/ && $4 !~ /usrjquota/ {$4=$4",usrjquota=aquota.user,jqfmt=vfsv1"} {print}' /etc/fstab >/etc/fstab.ike-new
			# same lines as before, exactly one of them changed
			if [ "$(wc -l </etc/fstab)" != "$(wc -l </etc/fstab.ike-new)" ] || [ "$(diff /etc/fstab /etc/fstab.ike-new | grep -c '^>')" -ne 1 ]; then
				rm -f /etc/fstab.ike-new
				warn "Could not update /etc/fstab safely; quotas were not enabled."
				return 0
			fi
			mv /etc/fstab.ike-new /etc/fstab
		fi
		systemctl daemon-reload
		if mount -o remount "$mnt" >>"$LOG_FILE" 2>&1 && quotacheck -ucm "$mnt" >>"$LOG_FILE" 2>&1 && quotaon -u "$mnt" >>"$LOG_FILE" 2>&1; then
			QUOTAS_ACTIVE=1
			info "Disk quotas are on for ${mnt}"
		else
			warn "Quotas are set up in /etc/fstab but could not be switched on now; they start after a reboot."
			QUOTAS_ACTIVE=1
		fi
		;;
	xfs)
		if [ "$mnt" = "/" ]; then
			if ! grep -q 'rootflags=uquota' /etc/default/grub; then
				sed -i 's/^GRUB_CMDLINE_LINUX="\(.*\)"/GRUB_CMDLINE_LINUX="\1 rootflags=uquota"/' /etc/default/grub
				update-grub >>"$LOG_FILE" 2>&1 || true
			fi
		else
			cp -a /etc/fstab "/etc/fstab.ike-$(date +%Y%m%d%H%M%S)"
			awk -v m="$mnt" 'BEGIN{OFS="\t"} $2==m && $1 !~ /^#/ && $4 !~ /uquota/ {$4=$4",uquota"} {print}' /etc/fstab >/etc/fstab.ike-new && mv /etc/fstab.ike-new /etc/fstab
		fi
		QUOTAS_ACTIVE=1
		warn "XFS quotas start after the next reboot. Reboot the server when the install has finished."
		;;
	*)
		warn "${fstype} has no Linux user quotas Ike can use; disk limits will be shown but not enforced."
		;;
	esac
}

# add_main installs only what --add named on a server that already runs Ike,
# then records the cache engines in agent.json (keeping every other setting)
# and restarts the agent so the panel offers them.
add_main() {
	: >"$LOG_FILE"
	chmod 0600 "$LOG_FILE"
	if [ ! -e "$CONF_DIR/installed" ] || [ ! -e "$CONF_DIR/agent.json" ]; then
		die "--add is for a server that already runs ${PANEL_NAME}; run the installer without it first."
	fi
	check_system
	info "Adding ${ADD_ONLY//,/, } to ${PANEL_NAME} on Ubuntu ${OS_VERSION}"
	apt-get update -q >>"$LOG_FILE" 2>&1
	if [ -n "$CACHE_ENGINES" ]; then
		# Keep the server-wide engine and the engines already installed.
		local had kind e
		kind=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("redis") or "")' "$CONF_DIR/agent.json")
		had=$(python3 -c 'import json,sys; print(" ".join(json.load(open(sys.argv[1])).get("cache_engines") or []))' "$CONF_DIR/agent.json")
		[ -n "$kind" ] && REDIS_KIND="$kind"
		local new="$CACHE_ENGINES"
		CACHE_ENGINES=""
		for e in $kind $had $new; do add_cache "$e"; done
		install_redis
		python3 - "$CONF_DIR/agent.json" "$REDIS_KIND" "$CACHE_ENGINES" <<'PY'
import json, os, sys
path, kind, engines = sys.argv[1], sys.argv[2], sys.argv[3].split()
with open(path) as f:
    cfg = json.load(f)
cfg["redis"], cfg["cache_engines"] = kind, engines
tmp = path + ".new"
with open(tmp, "w") as f:
    json.dump(cfg, f, indent=2)
os.chmod(tmp, 0o640)
os.replace(tmp, path)
PY
	fi
	install_turn
	install_livekit
	systemctl restart "${AGENT_NAME}" >>"$LOG_FILE" 2>&1 || warn "Restart ${AGENT_NAME} yourself: systemctl restart ${AGENT_NAME}"
	cat <<EOF

${C_BLD}${C_GRN}Added${C_RST}

  Caches        ${CACHE_ENGINES:-unchanged}$( [ -n "${DRAGONFLY_INSTALLED:-}" ] && echo " (Dragonfly ${DRAGONFLY_INSTALLED})" )
  TURN          $( [ -n "${TURN_INSTALLED:-}" ] && echo "coturn ${TURN_INSTALLED}" || echo "not added" )
  LiveKit       $( [ -n "${LIVEKIT_INSTALLED:-}" ] && echo "${LIVEKIT_INSTALLED}" || echo "not added" )
  Install log   ${LOG_FILE}

Switch TURN and LiveKit on in the panel under Server, Real-time media. Account owners pick their cache engine under Databases, Cache.
EOF
}

main() {
	if [ -n "$ADD_ONLY" ]; then
		add_main
		return
	fi
	: >"$LOG_FILE"
	chmod 0600 "$LOG_FILE"
	check_system
	info "Installing ${PANEL_NAME} on Ubuntu ${OS_VERSION} (${OS_CODENAME}, ${ARCH})"
	install_base
	detect_identity
	install_nginx
	install_apache
	install_caddy
	install_openlitespeed
	detect_web_user
	install_php
	install_wpcli
	install_composer
	install_node
	install_python
	install_java
	install_go
	install_dart
	install_package_tools
	install_mariadb
	install_postgres
	install_redis
	install_turn
	install_livekit
	install_mongodb
	install_phpmyadmin
	configure_ssh
	configure_firewall
	install_docker
	install_pack
	install_k3s
	install_quotas
	install_modsecurity
	install_dns
	install_mail
	install_scanner
	install_agent
	install_panel
	join_cluster
	summary
}

# IKE_INSTALL_LIB=1 loads the functions without installing (tests).
[ "${IKE_INSTALL_LIB:-0}" = 1 ] || main "$@"
