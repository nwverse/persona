#!/bin/sh
# Install jarvisd (docs/release.md "Publishing jarvisd"). scripts/release/publish.sh serves this file as <site>/install.sh with
# JARVIS_DL filled in:
#
#   curl -fsSL <site>/install.sh | sh
#
# Puts the binary for this system at ~/.jarvis/bin/jarvisd (where the hub looks for it on every computer it reaches
# over ssh), after checking it against the published SHA256SUMS. JARVIS_DL=<url> installs from somewhere else.
#
# One hub is enough for a home (docs/plan-hub-center.md section 0); every other computer gets the node service: the
# resident node and the connector jarvis-link, paired with the hub (docs/link.md "Installing the node service"):
#
#   curl -fsSL <site>/install.sh | sh -s -- --node --pair '<link from jarvisd hub pair --machine <id>>'   # this one connects to the hub
#   curl -fsSL <site>/install.sh | sh -s -- --node --listen [:18663] [--addr <address the hub reaches this one at>]  # the hub connects here
#
# --pair and --listen imply --node (JARVIS_ROLE=node, JARVIS_PAIR, JARVIS_LISTEN do the same). Without a role and with
# a terminal it asks, and says so when it hears a hub on this LAN already (mDNS). --hub is the machine that runs the hub.
#
# On the machine that runs the hub it also asks who may open the hub's admin UI (docs/admin.md): with a terminal it
# asks; without one, give it as `sh -s -- --admin-access lan` (or JARVIS_ADMIN_ACCESS=lan), else lan applies and a
# line says so. --admin-cidrs a,b (JARVIS_ADMIN_CIDRS) goes with cidrs.
# Then herdr (the default engine, docs/engines.md) if this computer has none: herdr's own installer puts it in
# ~/.local/bin, where the node looks. JARVIS_SKIP_HERDR=1 leaves it out (Orca / Herdr App machines); a failed herdr
# install only warns. scripts/deploy/ensure-herdr.sh does the same for deploy.sh: keep the two in step.
set -eu

# >>> admin access (scripts/deploy.sh reads this block from here: keep it POSIX sh and self-contained)
admin_access_help() {
	cat <<'HELP'
hub 管理界面（<hub 地址>/admin：加电脑、管设备、改网络）谁能打开？
  1) loopback  只有 hub 这台机器自己（Docker 里是容器自己），别的电脑要用 ssh 隧道。最安全，用着最麻烦
  2) lan       本机 + 和 hub 同一网段的局域网（默认）。家里 / 办公室的局域网里都能打开，公网和别的网段打不开
  3) cidrs     本机 + 你列出的网段，例如 VPN 的网段；其中的公网网段也只能经 TLS
  4) any       任何地方经 TLS 端口都能打开（局域网照旧）。暴露在公网上，只靠管理员密码和登录限流挡着；没开 TLS 时等于 lan
以后在 hub 机器上用 jarvisd hub admin access <范围> 改；网页里只能收窄，不能放宽。
HELP
}

# admin_access_valid <access> <cidrs>: the scope is one of the four, and the networks look like networks.
admin_access_valid() {
	case "$1" in loopback | lan | any) [ -z "$2" ] ;; cidrs) [ -n "$2" ] && case "$2" in *[!0-9A-Fa-f.:/,]*) false ;; *) true ;; esac ;; *) false ;; esac
}

# ask_admin_access: ADMIN_ACCESS (and ADMIN_CIDRS) from the terminal; returns 1 when there is none (curl | sh still has
# /dev/tty when run by a person; CI and cron do not).
ask_admin_access() {
	(: </dev/tty) 2>/dev/null || return 1
	admin_access_help >/dev/tty
	while :; do
		printf '选哪个？[1-4，回车 = 2 lan] ' >/dev/tty
		read -r answer </dev/tty || return 1
		case "$answer" in
		'' | 2 | lan) ADMIN_ACCESS=lan ;;
		1 | loopback) ADMIN_ACCESS=loopback ;;
		3 | cidrs) ADMIN_ACCESS=cidrs ;;
		4 | any) ADMIN_ACCESS=any ;;
		*) echo "请输入 1 到 4" >/dev/tty && continue ;;
		esac
		break
	done
	ADMIN_CIDRS=
	while [ "$ADMIN_ACCESS" = cidrs ] && ! admin_access_valid cidrs "$ADMIN_CIDRS"; do
		printf '网段，逗号分隔（例如 100.64.0.0/10,192.0.2.0/24）：' >/dev/tty
		read -r ADMIN_CIDRS </dev/tty || return 1
		ADMIN_CIDRS=$(printf '%s' "$ADMIN_CIDRS" | tr -d ' ')
	done
	if [ "$ADMIN_ACCESS" = any ]; then
		printf '管理界面会对公网开放。输入 any 确认，别的改用 lan：' >/dev/tty
		read -r confirm </dev/tty || return 1
		[ "$confirm" = any ] || { echo "没有确认，改用 lan" >/dev/tty && ADMIN_ACCESS=lan; }
	fi
	return 0
}

# admin_access_reminder [if-hub]: the one line printed when nobody chose (no terminal, no option).
admin_access_reminder() {
	echo "提醒：${1:+如果这台跑 hub，}hub 管理界面（/admin）的访问范围没有选，用默认的 lan（hub 本机和同一局域网）；要改在 hub 机器上运行 jarvisd hub admin access <loopback|lan|cidrs|any>" >&2
}
# <<< admin access

for suffix in ADMIN_ACCESS ADMIN_CIDRS ROLE PAIR MACHINE LISTEN DL HERDR_INSTALL WITH SKIP_HERDR HOME; do
	eval 'if [ "${PERSONA_'"$suffix"'+x}" = x ]; then export JARVIS_'"$suffix"'="${PERSONA_'"$suffix"'}"; elif [ "${JARVIS_'"$suffix"'+x}" = x ]; then export PERSONA_'"$suffix"'="${JARVIS_'"$suffix"'}"; fi'
done

ADMIN_ACCESS="${JARVIS_ADMIN_ACCESS:-}"
ADMIN_CIDRS="${JARVIS_ADMIN_CIDRS:-}"
ROLE="${JARVIS_ROLE:-}"
PAIR="${JARVIS_PAIR:-}"
MACHINE="${JARVIS_MACHINE:-}"
LISTEN="${JARVIS_LISTEN:-}"
LISTEN_SET="${JARVIS_LISTEN:+1}"
while [ $# -gt 0 ]; do
	case "$1" in
	--admin-access) ADMIN_ACCESS="${2:-}" && shift ;;
	--admin-access=*) ADMIN_ACCESS="${1#*=}" ;;
	--admin-cidrs) ADMIN_CIDRS="${2:-}" && shift ;;
	--admin-cidrs=*) ADMIN_CIDRS="${1#*=}" ;;
	--node) ROLE=node ;;
	--hub) ROLE=hub ;;
	--pair) PAIR="${2:-}" && shift ;;
	--pair=*) PAIR="${1#*=}" ;;
	--machine) MACHINE="${2:-}" && shift ;;
	--machine=*) MACHINE="${1#*=}" ;;
	--listen)
		LISTEN_SET=1
		case "${2:-}" in '' | -*) ;; *) LISTEN="$2" && shift ;; esac ;;
	--listen=*) LISTEN_SET=1 && LISTEN="${1#*=}" ;;
	--addr) ADDRS="${ADDRS:-} ${2:-}" && LISTEN_SET=1 && shift ;;
	--addr=*) ADDRS="${ADDRS:-} ${1#*=}" && LISTEN_SET=1 ;;
	*) echo "jarvisd: unknown option $1 (--node [--pair <link> [--machine <id>] | --listen [addr] [--addr <host>]], --hub, --admin-access <loopback|lan|cidrs|any>, --admin-cidrs a,b)" >&2 && exit 2 ;;
	esac
	shift
done
if [ -n "$PAIR" ] || [ -n "$LISTEN_SET" ]; then
	[ "$ROLE" != hub ] || { echo "jarvisd: --pair / --listen are for the node service, not --hub" >&2 && exit 2; }
	ROLE=node
fi
if [ -n "$PAIR" ] && [ -n "$LISTEN_SET" ]; then
	echo "jarvisd: --pair (this computer dials the hub) or --listen (the hub dials it), not both" >&2
	exit 2
fi
case "$ROLE" in '' | hub | node) ;; *) echo "jarvisd: JARVIS_ROLE must be hub or node" >&2 && exit 2 ;; esac
if [ -n "$ADMIN_ACCESS" ] && ! admin_access_valid "$ADMIN_ACCESS" "$ADMIN_CIDRS"; then
	echo "jarvisd: --admin-access must be loopback, lan, cidrs (with --admin-cidrs) or any" >&2
	exit 2
fi

lexical_path() {
	rest="$1"
	result=
	while [ -n "$rest" ]; do
		part="${rest%%/*}"
		if [ "$rest" = "$part" ]; then rest=; else rest="${rest#*/}"; fi
		case "$part" in '' | .) ;; ..) result="${result%/*}" ;; *) result="$result/$part" ;; esac
	done
	printf '%s' "${result:-/}"
}

DL="${JARVIS_DL:-@JARVIS_DL@}"
HERDR_INSTALL="${JARVIS_HERDR_INSTALL:-https://herdr.dev/install.sh}"
STATE="${JARVIS_HOME:-}"
if [ -z "$STATE" ]; then
	if [ -e "$HOME/.jarvis" ] || [ -L "$HOME/.jarvis" ]; then STATE="$HOME/.jarvis"; else STATE="$HOME/.persona"; fi
fi
case "$STATE" in '~') STATE="$HOME" ;; '~/'*) STATE="$HOME/${STATE#\~/}" ;; esac
case "$STATE" in /*) ;; *) STATE="$HOME/$STATE" ;; esac
STATE="$(lexical_path "$STATE")"
DEST="$STATE/bin"

fail() {
	echo "jarvisd: $*" >&2
	exit 1
}

state_alias() {
	if ln -s "${1##*/}" "$2"; then
		[ "$1" -ef "$2" ] || fail "state directories changed during initialization: retry without moving either directory"
		return 0
	fi
	if [ -e "$2" ] || [ -L "$2" ]; then
		[ "$1" -ef "$2" ] || fail "state directories changed during initialization: retry without moving either directory"
	elif [ -d "$STATE" ]; then
		return 0
	elif [ -z "${JARVIS_HOME:-}" ] && [ -d "$1" ]; then
		STATE="$1"
		DEST="$STATE/bin"
	else
		fail "could not create state alias $2"
	fi
}

prepare_state() {
	case "$STATE" in
	"$HOME/.persona" | "$HOME/.jarvis")
		if [ ! -e "$HOME/.persona" ] && [ ! -L "$HOME/.persona" ] && [ ! -e "$HOME/.jarvis" ] && [ ! -L "$HOME/.jarvis" ]; then
			mkdir -p "$HOME/.jarvis"
			state_alias "$HOME/.jarvis" "$HOME/.persona"
		elif [ ! -e "$HOME/.jarvis" ] && [ ! -L "$HOME/.jarvis" ] && [ -d "$HOME/.persona" ]; then
			state_alias "$HOME/.persona" "$HOME/.jarvis"
		elif [ ! -e "$HOME/.persona" ] && [ ! -L "$HOME/.persona" ] && [ -d "$HOME/.jarvis" ]; then
			state_alias "$HOME/.jarvis" "$HOME/.persona"
		fi ;;
	esac
	if [ -L "$STATE" ] && [ ! -d "$STATE" ]; then
		case "$STATE" in "$HOME/.persona" | "$HOME/.jarvis") ;; *) fail "unavailable state symlink $STATE: restore its target before upgrading" ;; esac
		target="$(readlink "$STATE")"
		case "$target" in /*) raw="$target" ;; *) raw="${STATE%/*}/$target" ;; esac
		while [ "${raw%/}" != "$raw" ] || [ "${raw%/.}" != "$raw" ]; do
			if [ "${raw%/.}" != "$raw" ]; then raw="${raw%/.}"; else raw="${raw%/}"; fi
		done
		target="$(lexical_path "$raw")"
		case "$target" in "$HOME/.persona" | "$HOME/.jarvis") ;; *) fail "unavailable state symlink $STATE: restore its target before upgrading" ;; esac
		actual_parent="$(CDPATH= cd -P "${raw%/*}" 2>/dev/null && pwd -P)" || fail "unavailable state symlink $STATE"
		expected_parent="$(CDPATH= cd -P "${target%/*}" 2>/dev/null && pwd -P)" || fail "unavailable state symlink $STATE"
		[ "$actual_parent" = "$expected_parent" ] || fail "unavailable state symlink $STATE: restore its target before upgrading"
		[ ! -e "$target" ] && [ ! -L "$target" ] || fail "unavailable state symlink $STATE"
		mkdir -p "$target"
	fi
	mkdir -p "$STATE"
	case "$STATE" in
	"$HOME/.jarvis") alias="$HOME/.persona" ;;
	"$HOME/.persona") alias="$HOME/.jarvis" ;;
	*) return 0 ;;
	esac
	if [ ! -e "$alias" ] && [ ! -L "$alias" ]; then
		state_alias "$STATE" "$alias"
	fi
}

binary_alias() {
	[ ! -d "$DEST/$1" ] || fail "$DEST/$1 is a directory, not a binary alias"
	stage="$DEST/.$1.${tmp##*/}.alias"
	[ ! -e "$stage" ] && [ ! -L "$stage" ] || fail "binary alias staging path already exists"
	if ! ln -s "$2" "$stage"; then
		[ ! -e "$stage" ] && [ ! -L "$stage" ] || fail "binary alias staging path changed"
		printf '%s\n' '#!/bin/sh' "exec \"\$(dirname \"\$0\")/$2\" \"\$@\"" > "$stage"
		chmod 755 "$stage"
	fi
	mv -f "$stage" "$DEST/$1"
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "no build for $(uname -s) (Linux and macOS only)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no build for $(uname -m)" ;;
esac
name="jarvisd-$os-$arch"
archive="$name.gz"
command -v gzip >/dev/null 2>&1 || fail "needs gzip to unpack the download"

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q "$1" -O "$2"; }
else
	fail "needs curl or wget"
fi
if command -v sha256sum >/dev/null 2>&1; then
	sum() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sum() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "needs sha256sum or shasum to check the download"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fetch "$DL/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not download $DL/SHA256SUMS"

# get <name> <installed as>: downloads <name>.gz, checks it against SHA256SUMS and puts it in $DEST.
get() {
	archive="$1.gz"
	fetch "$DL/$archive" "$tmp/$archive" || fail "could not download $DL/$archive"
	want="$(awk -v n="$archive" '$2 == n || $2 == "*" n { print $1 }' "$tmp/SHA256SUMS")"
	[ -n "$want" ] || fail "$archive is not in SHA256SUMS"
	[ "$(sum "$tmp/$archive")" = "$want" ] || fail "checksum mismatch for $archive: not installed"
	gzip -dc "$tmp/$archive" > "$tmp/$1" || fail "could not unpack $archive: not installed"
	[ -s "$tmp/$1" ] || fail "$archive contains an empty binary: not installed"
	prepare_state
	mkdir -p "$DEST"
	chmod 755 "$tmp/$1"
	mv "$tmp/$1" "$DEST/$2"
}

get "$name" jarvisd
binary_alias personad jarvisd
version="$("$DEST/jarvisd" version 2>/dev/null || echo unknown)"
echo "jarvisd $version installed at $DEST/jarvisd"
case ":$PATH:" in
*":$DEST:"*) ;;
*) echo "(not on PATH: run it as $DEST/jarvisd, or add $DEST to PATH)" ;;
esac

# On the computer that runs the hub, JARVIS_WITH=wechat also installs jarvisd-wechat, the WeChat bot's program, next
# to jarvisd (docs/wechat.md). Other computers do not need it; a hub that updates itself gets it with its next update
# anyway. (Telegram's connector is an extension now, installed from the hub's extension market: #1704.)
for p in wechat; do
	case ",${JARVIS_WITH:-}," in
	*,$p,*)
		get "jarvisd-$p-$os-$arch" "jarvisd-$p"
		binary_alias "personad-$p" "jarvisd-$p"
		echo "jarvisd-$p installed at $DEST/jarvisd-$p"
		;;
	esac
done

# The places the node looks besides PATH (server/internal/node/node.go: ssh sessions get a minimal PATH).
has_herdr() {
	command -v herdr >/dev/null 2>&1 && return 0
	for d in "$HOME/.local/bin" "$HOME/bin" /opt/homebrew/bin /usr/local/bin; do
		[ -x "$d/herdr" ] && return 0
	done
	return 1
}
if [ "${JARVIS_SKIP_HERDR:-}" = 1 ]; then
	:
elif has_herdr; then
	echo "herdr: already installed"
else
	echo "herdr: not found, installing it (JARVIS_SKIP_HERDR=1 skips this)"
	if fetch "$HERDR_INSTALL" "$tmp/herdr-install.sh" && sh "$tmp/herdr-install.sh" && has_herdr; then
		echo "herdr: installed"
	else
		echo "jarvisd: warning: could not install herdr; install it yourself (https://herdr.dev) before using Agents on this computer" >&2
	fi
fi
# fetch_link puts jarvis-link (the connector, Persona Link) at $DEST/jarvis-link, checked against SHA256SUMS like
# jarvisd, and persona-link next to it as its new name (#1159): the file, the download and the service keep the old one.
fetch_link() {
	[ -z "${LINK_FETCHED:-}" ] || return 0
	lname="jarvis-link-$os-$arch"
	larchive="$lname.gz"
	fetch "$DL/$larchive" "$tmp/$larchive" || { echo "jarvisd: could not download $DL/$larchive" >&2 && return 1; }
	lwant="$(awk -v n="$larchive" '$2 == n || $2 == "*" n { print $1 }' "$tmp/SHA256SUMS")"
	[ -n "$lwant" ] || { echo "jarvisd: $larchive is not in SHA256SUMS" >&2 && return 1; }
	[ "$(sum "$tmp/$larchive")" = "$lwant" ] || { echo "jarvisd: checksum mismatch for $larchive: not installed" >&2 && return 1; }
	gzip -dc "$tmp/$larchive" > "$tmp/$lname" && [ -s "$tmp/$lname" ] || { echo "jarvisd: could not unpack $larchive: not installed" >&2 && return 1; }
	chmod 755 "$tmp/$lname"
	mv "$tmp/$lname" "$DEST/jarvis-link"
	binary_alias persona-link jarvis-link
	LINK_FETCHED=1
	echo "persona-link $("$DEST/jarvis-link" version 2>/dev/null || echo unknown) installed at $DEST/persona-link (also $DEST/jarvis-link)"
}

# install_node makes this computer the node service: the resident node and the connector, paired with the hub
# (docs/link.md). jarvis-link does the work; this only picks how it pairs. It runs the real file, and
# PERSONA_LINK_NAME only makes what it prints say persona-link (the alias fetch_link made).
install_node() {
	fetch_link || fail "the node service needs Persona Link (jarvis-link)"
	if [ -z "$PAIR" ] && [ -z "$LISTEN_SET" ] && (: </dev/tty) 2>/dev/null; then
		cat >/dev/tty <<'ASK'
这台电脑怎么和 hub 连？
  1) 我去连 hub：在 hub 机器上运行 jarvisd hub pair --machine <这台的 id> --add，把拿到的链接粘贴过来（笔记本、在 NAT 后面的电脑）
  2) 让 hub 来连：这台监听一个端口，打印一条链接粘贴到 hub 管理界面「电脑 › 添加电脑 › hub 来连」（服务器、地址固定的电脑）
ASK
		printf '选哪个？[1-2，回车 = 1] ' >/dev/tty
		read -r how </dev/tty || how=
		case "$how" in
		2) LISTEN_SET=1 ;;
		*)
			printf '粘贴配对链接（回车跳过，以后再 persona-link pair）：' >/dev/tty
			read -r PAIR </dev/tty || PAIR=
			;;
		esac
	fi
	set --
	if [ -n "$PAIR" ]; then
		set -- --pair "$PAIR"
		[ -z "$MACHINE" ] || set -- "$@" --machine "$MACHINE"
	elif [ -n "$LISTEN_SET" ]; then
		set -- --listen
		[ -z "$LISTEN" ] || set -- "$@" "$LISTEN"
		for a in ${ADDRS:-}; do set -- "$@" --addr "$a"; done # addresses have no spaces
	fi
	if [ "$os" = darwin ]; then
		# On a Mac the connector is the menu bar app (Persona Link, docs/link.md); here: the resident node and the pairing.
		PERSONA_LINK_NAME=persona-link "$DEST/jarvis-link" node install || return 1
		case "${1:-}" in
		--pair) PERSONA_LINK_NAME=persona-link "$DEST/jarvis-link" pair "$PAIR" ${MACHINE:+--machine "$MACHINE"} || return 1 ;;
		--listen) shift && PERSONA_LINK_NAME=persona-link "$DEST/jarvis-link" pair --listen "$@" || return 1 ;;
		esac
		echo "Mac 上的连接器是菜单栏 App「Persona Link」：装上它（它带着连接器和 personad，配对也在它的菜单里）；没有 App 时先前台跑 $DEST/persona-link run。"
		return 0
	fi
	PERSONA_LINK_NAME=persona-link "$DEST/jarvis-link" install "$@"
}

# Who may open the admin UI, on the machine that runs the hub (one with a hub.json, or one the person says is).
set_admin_access() {
	if [ "$ADMIN_ACCESS" = cidrs ]; then
		"$DEST/jarvisd" hub admin access cidrs --cidrs "$ADMIN_CIDRS"
	else
		"$DEST/jarvisd" hub admin access "$ADMIN_ACCESS"
	fi
}
if [ "$ROLE" = node ]; then
	install_node
	exit $?
fi
if [ -n "$ADMIN_ACCESS" ]; then
	set_admin_access
elif [ -f "$STATE/hub.json" ]; then
	if [ -z "$("$DEST/jarvisd" hub admin access --get 2>/dev/null || true)" ]; then
		if ask_admin_access; then set_admin_access; else admin_access_reminder; fi
	fi
elif [ "$ROLE" = hub ]; then
	if ask_admin_access; then set_admin_access; else admin_access_reminder; fi
elif (: </dev/tty) 2>/dev/null; then
	# A hub on this LAN already: this computer only needs the node service (docs/plan-hub-center.md section 0).
	found=
	if fetch_link 2>/dev/null; then found="$("$DEST/jarvis-link" discover --wait 3 2>/dev/null || true)"; fi
	if [ -n "$found" ]; then
		{
			echo "局域网里已经有 hub："
			echo "$found" | sed 's/^/  /'
			echo "全家只要一台 hub；这台装 node 服务（常驻 node + 连接器）就够了。"
		} >/dev/tty
		printf '装成 node 服务？[Y/n] ' >/dev/tty
		read -r as_node </dev/tty || as_node=
		case "$as_node" in n | N | no) ;; *) install_node && exit 0 || exit 1 ;; esac
	fi
	printf '这台电脑要跑 hub 吗？[y/N] ' >/dev/tty
	read -r is_hub </dev/tty || is_hub=
	case "$is_hub" in
	y | Y | yes)
		ROLE=hub
		if ask_admin_access; then set_admin_access; else admin_access_reminder; fi
		;;
	*)
		printf '装成 node 服务、连到你的 hub？[Y/n] ' >/dev/tty
		read -r as_node </dev/tty || as_node=
		case "$as_node" in n | N | no) ;; *) install_node && exit 0 || exit 1 ;; esac
		;;
	esac
else
	admin_access_reminder if-hub
fi

# The machine that runs the hub: personad hub setup does the rest (hub.json, the database, the service, the phone's
# QR code and the admin URL, docs/onboarding.md 4.1). Running it again is safe, so an upgrade with --hub runs it too.
if [ "$ROLE" = hub ]; then
	echo ""
	echo "接着把这台电脑设成 hub（$DEST/personad hub setup）："
	if "$DEST/personad" hub setup; then
		exit 0
	fi
	echo "hub 还没装好：按上面的「下一步」处理后，运行 $DEST/personad hub setup 接着装（已经做好的不会重做）。" >&2
	exit 1
fi
# A hub that has run has a token in hub.json; set_admin_access above may have written one with admin.access only.
if [ -f "$STATE/hub.json" ] && grep -Eq '"token"[[:space:]]*:[[:space:]]*"[^"]' "$STATE/hub.json"; then
	echo "这台已经是 hub，personad 升级好了；重启 hub 后用上新版本（Linux：systemctl --user restart jarvisd）。要检查一遍：$DEST/personad hub setup"
	exit 0
fi
echo "要让这台电脑跑 hub：运行 $DEST/personad hub setup（或重跑安装命令并加 --hub）。全家只要一台 hub。"
echo "（跑 hub 的那台加 JARVIS_WITH=wechat 会一起装微信机器人。）"
echo "其他电脑装 node 服务：安装命令加 --node --pair '<personad hub pair --machine <id> 打印的链接>'（或 --listen：让 hub 来连）。"
