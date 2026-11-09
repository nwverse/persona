#!/bin/bash
# Install the existing hub unit using the same resolved root as the deployed binary (Linux: systemd user service;
# Mac: restart the launchd agent that already runs it). personad hub setup (server/internal/hub/setup_service.go)
# writes the same units: keep the two in step.
#
# The program the hub's service runs stays the one it runs (docs/server/server.md "Programs"): <root>/bin/persona-hub
# (`persona-hub run`) once its owner switched to it, else personad (`personad hub`). This script never switches an
# install from one to the other.
set -euo pipefail
# unit_program <unit file>: the program a systemd unit runs, read as systemd reads it and as the hub's self-update does
# (unitPrograms in server/internal/hub/selfupdate_services.go; scripts/test/test_hub_switch_deploy.py runs both over
# one table): the last ExecStart in [Service] (key and value trimmed), the drop-ins' after the unit's, an empty one
# resetting it; its first word unquoted, with %h, %% and $$ replaced, resolved through symlinks; "" when that is not an
# absolute path. With SPLIT_HUB set, an ExecStart that names a persona-hub other than that one fails: the unit is left
# for its owner. `hub-systemd.sh --unit-program <file>` runs only this.
unit_program() {
  SERVICE_FILE="$1" python3 - <<'PY'
import glob, os, re, sys
service, home = os.environ['SERVICE_FILE'], os.path.expanduser('~')
def program(value):
    """The first word of an ExecStart value, unquoted, with the specifiers systemd replaces in a path."""
    value = value.lstrip('-@:+!')
    if value[:1] in ('"', "'"):
        word, i = [], 1
        while i < len(value) and value[i] != value[0]:
            if value[i] == '\\' and i + 1 < len(value):
                i += 1
            word.append(value[i])
            i += 1
        word = ''.join(word)
    else:
        word = value.split()[0] if value.split() else ''
    return re.sub(r'%%|%h|\$\$', lambda m: {'%%': '%', '%h': home, '$$': '$'}[m.group()], word)
start = ''
for path in [service] + sorted(glob.glob(glob.escape(service) + '.d/*.conf')):
    try:
        text = open(path).read()
    except OSError:
        continue
    section = ''
    for line in text.replace('\\\n', ' ').splitlines():
        line = line.strip()
        if line.startswith('['):
            section = line
            continue
        key, sep, value = line.partition('=')
        if sep and section == '[Service]' and key.strip() == 'ExecStart':
            start = value.strip()  # empty resets it
word = program(start)
runs = os.path.realpath(word) if word.startswith('/') else ''
split = os.environ.get('SPLIT_HUB')
if split and runs != os.path.realpath(split) and re.search('(^|[\\s/"\'=])persona-hub($|[\\s"\';])', start):
    sys.exit('hub: ' + os.path.basename(service) + ' runs ' + start + ', a persona-hub other than ' + split
             + ': the unit is left as it is; point its ExecStart at ' + split + ' (or at personad hub) and deploy again')
print(runs)
PY
}
if [ "${1:-}" = --unit-program ]; then
  unit_program "$2"
  exit
fi
if [ -z "${_PERSONA_HUB_UNIT:-}" ]; then
  _env=$(python3 "$(dirname "${BASH_SOURCE[0]}")/../persona_identity.py" --shell --service hub --prepare) || exit $?
  eval "$_env"; unset _env
fi
ROOT="$_PERSONA_STATE_DIR"
BIN="$_PERSONA_HUB_BIN"
UNIT="$_PERSONA_HUB_UNIT"
SPLIT_HUB="$ROOT/bin/persona-hub"
# The programs a deploy replaces besides personad: a hub run as persona-hub goes back on all of them (switch_or_restart).
SPLIT_PROGRAMS=(persona-hub persona-manager)
# put_back <program>: its .prev back in place, through a symlink to the file it names (an install under the former name
# keeps personad as a link to that file), so that every name of it runs the old build again. Non-zero when it could not
# (callers test it, so set -e does not stop it on the way).
put_back() {
  local file
  file=$(python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$1") && [ -n "$file" ] || return 1
  cp -p "$1.prev" "$file.new" && mv "$file.new" "$file"
}
# use_split_hub: the service runs this root's persona-hub, so the deploy switches to it and the unit keeps it.
use_split_hub() {
  if [ ! -x "$SPLIT_HUB" ]; then
    echo "hub: the hub's service runs $SPLIT_HUB, which is not there: deploy it, or rerun personad hub setup" >&2
    exit 1
  fi
  BIN="$SPLIT_HUB"
}
# Blue-green (docs/operations/hub-restart.md "Blue-green switch"): the running hub's edge starts the new binary next to the one serving
# and switches once it is up, nothing restarts. It returns 0 once switched; 1 (no edge here, or one older than the new
# binary's protocol: `hub switch` exits 3; or a binary that cannot run it) to have the caller restart the service;
# it exits the script when the new binary did not come up (exit 1: the hub serves on the old one, back in place, or
# the message names each program whose old build could not be put back).
switch_or_restart() {
  local prev=() rc=0 name failed=()
  [ -x "$ROOT/bin/${BIN##*/}.prev" ] && prev=(--prev "$ROOT/bin/${BIN##*/}.prev")
  if [ "$BIN" = "$SPLIT_HUB" ] && [ ${#prev[@]} -eq 0 ]; then
    # Nothing to go back to: a failed switch would leave the old persona-hub serving beside the new personad and
    # persona-manager. A restart runs the build that is on disk.
    echo "hub: no $SPLIT_HUB.prev to go back to if the switch fails (deploy.sh keeps one): restarting the service"
    return 1
  fi
  # The program given, not hub switch's default: the deploy says which one the hub runs.
  "$BIN" hub switch --binary "$BIN" --timeout 8m ${prev[@]+"${prev[@]}"} || rc=$?
  case $rc in
    0) echo "hub: switched to the new binary (blue-green, nothing restarted)"; return 0 ;;
    1) ;;
    *) echo "hub: no edge to switch on (hub switch: exit $rc): restarting the service"; return 1 ;;
  esac
  if [ ${#prev[@]} -gt 0 ]; then
    put_back "$BIN" || failed+=("$BIN")
    if [ "$BIN" = "$SPLIT_HUB" ]; then
      # The hub runs as persona-hub, which starts persona-manager beside it and goes by personad for updates:
      # the old build of each goes back with it (deploy.sh kept a .prev of each it replaced).
      for name in "${_PERSONA_HUB_BIN##*/}" "${SPLIT_PROGRAMS[@]}"; do
        [ "$ROOT/bin/$name" != "$BIN" ] && [ -x "$ROOT/bin/$name.prev" ] || continue
        put_back "$ROOT/bin/$name" || failed+=("$ROOT/bin/$name")
      done
    fi
    if [ ${#failed[@]} -gt 0 ]; then
      printf 'hub: the new binary did not take over; the old one serves on, but could not put the old build back for %s (still the new build: copy its .prev over it)\n' "${failed[*]}" >&2
    else
      echo "hub: the new binary did not take over; the old one serves on and is back in place" >&2
    fi
  else
    echo "hub: the new binary did not take over; the old one serves on" >&2
  fi
  exit 1
}
if [ "$(uname -s)" = Darwin ] && ! command -v systemctl >/dev/null 2>&1; then
  # A Mac hub runs under a launchd agent the user set up (no systemd): restart the one whose program is this root's
  # personad with "hub", or its persona-hub (through ~/.persona or another path to it, whichever the agent names).
  # Nothing is installed here.
  found=$(python3 - <<'PY'
import glob, os, plistlib
bin_dir = os.path.join(os.environ['PERSONA_HOME'], 'bin')
personad, split = (os.path.realpath(os.path.join(bin_dir, name)) for name in ('personad', 'persona-hub'))
for path in sorted(glob.glob(os.path.expanduser('~/Library/LaunchAgents/*.plist'))):
    try:
        with open(path, 'rb') as f:
            job = plistlib.load(f)
    except Exception:
        continue
    # launchd runs Program when there is one, with ProgramArguments as its argv; else ProgramArguments[0].
    argv = job.get('ProgramArguments') or []
    run = job.get('Program') or (argv[0] if argv else '')
    program, args = (os.path.realpath(os.path.expanduser(run)) if run else ''), argv[1:]
    if program == personad and args[:1] == ['hub']:
        print('personad\t' + job.get('Label', ''))
        break
    # persona-hub runs the hub with no arguments, "run", or personad's "hub [run]".
    if program == split and args in ([], ['run'], ['hub'], ['hub', 'run']):
        print('persona-hub\t' + job.get('Label', ''))
        break
PY
)
  label=${found#*$'\t'}
  if [ -z "$found" ] || [ -z "$label" ]; then
    echo "hub: no launchd agent in ~/Library/LaunchAgents runs $ROOT/bin/personad hub or $SPLIT_HUB; restart the hub yourself" >&2
    exit 1
  fi
  [ "${found%%$'\t'*}" != persona-hub ] || use_split_hub
  switch_or_restart && exit 0
  launchctl kickstart -k "gui/$(id -u)/$label"
  echo "hub: restarted launchd agent $label"
  exit 0
fi
# The listening sockets belong to $UNIT.socket (socket activation, docs/operations/hub-restart.md "No gap"): restarting the
# hub never closes the port, connections made meanwhile wait for the next process instead of being refused.
# PERSONA_HUB_SOCKET=0 goes back to the hub listening itself (the rollback): the socket unit is removed.
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
SOCKET_FILE="$UNIT_DIR/$UNIT.socket"
SERVICE_FILE="$UNIT_DIR/$UNIT.service"
socket_before=$(cat "$SOCKET_FILE" 2>/dev/null || true)
service_before=$(cat "$SERVICE_FILE" "$SERVICE_FILE.d/90-persona-compat.conf" 2>/dev/null || true)
# The program the unit runs now (unit_program): persona-hub stays persona-hub; an ExecStart naming another persona-hub
# stops the deploy here, before the unit is rewritten.
runs=$(SPLIT_HUB="$SPLIT_HUB" unit_program "$SERVICE_FILE") || exit 1
if [ -n "$runs" ] && [ "$runs" = "$(python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$SPLIT_HUB")" ]; then
  use_split_hub
fi
PERSONA_HUB_SOCKET="${PERSONA_HUB_SOCKET:-1}" _PERSONA_HUB_RUNS="$BIN" python3 - <<'PY'
import json, os, re
from pathlib import Path
root = os.environ['PERSONA_HOME']
unit_name = os.environ['_PERSONA_HUB_UNIT']
binary = os.environ['_PERSONA_HUB_RUNS']
# personad runs the hub as `personad hub`, persona-hub (the hub's own program) as `persona-hub run`.
split = os.path.basename(binary) == 'persona-hub'
command = ' run' if split else ' hub'
description = 'Persona hub (persona-hub)' if split else 'Persona hub (personad)'
legacy_home = os.environ['_PERSONA_LEGACY_HOME_KEY']
legacy_edge = os.environ['_PERSONA_LEGACY_EDGE_KEY']
def quote(value):
    return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('%', '%%') + '"'
def stream(addr):
    # hub.json's Go address as a ListenStream: ":18660" (every address) is just the port for systemd.
    return addr[1:] if addr.startswith(':') else addr
try:
    cfg = json.loads((Path(root) / 'hub.json').read_text())
except (OSError, ValueError):
    cfg = {}
listens = [cfg.get('listen') or ':18660']
if (cfg.get('public') or {}).get('tls_listen'):
    listens.append(cfg['public']['tls_listen'])
use_socket = os.environ['PERSONA_HUB_SOCKET'] != '0'
unit_dir = Path(os.environ.get('XDG_CONFIG_HOME') or str(Path.home() / '.config')) / 'systemd/user'
# PERSONA_HUB_EDGE=0 runs the hub without its edge (docs/operations/hub-restart.md "Blue-green switch"): what was given once stays in the unit
# across deploys until it is given again (PERSONA_HUB_EDGE=1 goes back to the edge).
edge = os.environ.get('PERSONA_HUB_EDGE')
if edge is None:
    try:
        m = re.search(r'(?:PERSONA_HUB_EDGE|' + re.escape(legacy_edge) + r')=([^"\s]*)', (unit_dir / (unit_name + '.service')).read_text())
        edge = m.group(1) if m else ''
    except OSError:
        edge = ''
# Keep an explicit enabling value in an upgrade drop-in so it overrides an older disabling value.
unit_dir.mkdir(parents=True, exist_ok=True)
unit = [
    '[Unit]', 'Description=' + description, 'After=network-online.target',
]
if use_socket:
    unit += ['Wants=' + unit_name + '.socket', 'After=' + unit_name + '.socket']
unit += [
    '', '[Service]',
    'ExecStart=' + quote(binary).replace('$', '$$') + command,
    'Restart=always', 'RestartSec=3', 'TimeoutStopSec=20',
    'Environment=' + quote('PERSONA_HOME=' + root),
    'Environment=' + quote(legacy_home + '=' + root),
    'Environment=PATH=%h/.local/bin:%h/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin',
    *(['Environment=' + quote(key + '=' + edge) for key in ('PERSONA_HUB_EDGE', legacy_edge)] if edge else []), '',
    '[Install]', 'WantedBy=default.target', '',
]
service = unit_dir / (unit_name + '.service')
if service.exists() and service.read_text() != '\n'.join(unit):
    # Preserve operator EnvironmentFile, drop-ins, credentials and other service settings.
    dropin = service.with_name(service.name + '.d') / '90-persona-compat.conf'
    dropin.parent.mkdir(parents=True, exist_ok=True)
    dropin.write_text('\n'.join([
        '[Service]', 'ExecStart=',
        'ExecStart=' + quote(binary).replace('$', '$$') + command,
        *['Environment=' + quote(key + '=' + root) for key in ('PERSONA_HOME', legacy_home)],
        *(['Environment=' + quote(key + '=' + edge) for key in ('PERSONA_HUB_EDGE', legacy_edge)] if edge else []), '',
    ]))
else:
    service.write_text('\n'.join(unit))
socket = unit_dir / (unit_name + '.socket')
if use_socket:
    socket.write_text('\n'.join([
        '[Unit]', "Description=Persona hub's listening sockets (docs/operations/hub-restart.md)", '', '[Socket]',
        *['ListenStream=' + stream(a) for a in listens],
        # An address-bound listener (personad hub setup binds TLS to the LAN address) comes up even while that
        # address is gone (DHCP) instead of failing the socket and the plaintext port with it. Same as hub/setup_service.go.
        'Backlog=4096', 'NoDelay=true', *(['FreeBind=true'] if any(not a.startswith(':') for a in listens) else []),
        'Service=' + unit_name + '.service', '',
        '[Install]', 'WantedBy=sockets.target', '',
    ]))
elif socket.exists():
    socket.unlink()
PY
socket_after=$(cat "$SOCKET_FILE" 2>/dev/null || true)
service_after=$(cat "$SERVICE_FILE" "$SERVICE_FILE.d/90-persona-compat.conf" 2>/dev/null || true)
systemctl --user daemon-reload
systemctl --user enable "$UNIT" >/dev/null 2>&1
# The units as they were and the hub running: switch to the new binary instead of restarting.
if [ "$socket_before" = "$socket_after" ] && [ "$service_before" = "$service_after" ] && systemctl --user is-active --quiet "$UNIT"; then
  if switch_or_restart; then
    systemctl --user is-active "$UNIT"
    exit 0
  fi
fi
if [ -z "$socket_after" ]; then
  # Rolling back (or never used): the hub listens itself again once the socket lets go of the port.
  if systemctl --user is-active --quiet $UNIT.socket 2>/dev/null || [ -n "$socket_before" ]; then
    systemctl --user stop "$UNIT"
    systemctl --user disable --now $UNIT.socket >/dev/null 2>&1 || true
    systemctl --user start "$UNIT"
  else
    systemctl --user restart "$UNIT"
  fi
elif ! systemctl --user is-active --quiet $UNIT.socket || [ "$socket_before" != "$socket_after" ]; then
  # First time, or other addresses: the running hub holds the port itself; one stop to hand it to the socket.
  # Both in one stop: stopping the service alone while the socket listens lets the next connection start it again
  # on the old socket, and the socket then cannot start while it runs (seen 2026-11-08: the socket left inactive).
  systemctl --user stop $UNIT.socket "$UNIT"
  systemctl --user enable $UNIT.socket >/dev/null 2>&1
  systemctl --user start $UNIT.socket
  systemctl --user start "$UNIT"
else
  systemctl --user restart "$UNIT" # the socket keeps listening: nothing is refused meanwhile
fi
systemctl --user is-active "$UNIT"
