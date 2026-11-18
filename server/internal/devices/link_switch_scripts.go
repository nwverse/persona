package devices

import (
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/nodeproto"
)

// The remote commands of a switch to Persona Link (link_switch.go). Each is a fixed POSIX sh script run through
// posixCommand, so any login shell runs it alike; only shellQuote'd values go into it (the operation id, the sha-256 of
// the build sent, the directories a test moves). None of them ever carries the pairing link: install reads it on
// stdin. Every path is checked under its current name and its older one (compat), as persona-link looks for both.

// What each script answers with besides its output's key=value lines; 255 is ssh's own failure, as always.
const (
	lsExitStageWrite     = 10 // the bin directory or the staged file could not be written
	lsExitStageCopy      = 11 // the gzipped build on stdin did not arrive whole
	lsExitStageSum       = 12 // what arrived is not the build sent (sha-256), or nothing here can tell
	lsExitStageVersion   = 13 // the staged build does not run `version` here
	lsExitInstallExists  = 20 // Persona Link appeared since the probe: nothing placed
	lsExitInstallStaged  = 21 // the staged build is gone: nothing placed
	lsExitInstallPlace   = 22 // the staged build could not be linked into place: nothing placed
	lsExitUninstallStays = 30 // something this switch should have removed is still there
)

// lsStagePrefix names the staged build in the data directory's bin (with the operation id): a hidden name nothing
// runs, which install links into place.
const lsStagePrefix = ".persona-link-switch-"

// The names under the data directory and the user's systemd units, current and older.
var (
	lsLinkBin        = "persona-link"
	lsLinkUnit       = "persona-link.service"
	lsNodeUnit       = nodeproto.ProgramPersonad + "-node.service"
	lsLegacyLinkBin  = compat.ConnectorBinary
	lsLegacyLinkUnit = compat.ConnectorBinary + ".service"
	lsLegacyNodeUnit = compat.DaemonBinary + "-node.service"
)

// lsPaths are where a switch looks on the computer: the systemd user manager's run directory and the logins that keep
// it running (loginctl enable-linger). Tests point them into a temporary directory.
type lsPaths struct {
	systemd, linger string
}

var defaultLSPaths = lsPaths{systemd: "/run/systemd/system", linger: "/var/lib/systemd/linger"}

// lsIn is name under the directory in shell variable dir, as one sh word.
func lsIn(dir, name string) string { return `"$` + dir + `"/` + shellQuote(name) }

// lsPrelude sets what every script reads: D the data directory, B its bin, O the older data directory, U the
// systemd user units, e() whether a path is there (a dangling link counts), and XDG_RUNTIME_DIR for systemctl --user
// when the ssh session has none.
func lsPrelude() []string {
	return []string{
		"set -u",
		`[ -n "${XDG_RUNTIME_DIR:-}" ] || { XDG_RUNTIME_DIR="/run/user/$(id -u)"; export XDG_RUNTIME_DIR; }`,
		`D="$HOME/.persona"`, `B="$D/bin"`, `O="$HOME"/` + shellQuote(compat.StateDir),
		`U="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"`,
		`e() { [ -e "$1" ] || [ -L "$1" ]; }`,
	}
}

// lsLinkPaths are what says Persona Link is on the computer already: its program, its pairing (link.json) and its
// service, in either data directory and under either name.
func lsLinkPaths() string {
	return strings.Join([]string{lsIn("B", lsLinkBin), lsIn("B", lsLegacyLinkBin), lsIn("D", "link.json"),
		lsIn("O", "bin/"+lsLinkBin), lsIn("O", "bin/"+lsLegacyLinkBin), lsIn("O", "link.json"),
		lsIn("U", lsLinkUnit), lsIn("U", lsLegacyLinkUnit)}, " ")
}

// lsSum sets s to the sha-256 (hex) of the file in shell variable file, "" when nothing here computes one.
func lsSum(file string) string {
	return `if command -v sha256sum >/dev/null 2>&1; then s=$(sha256sum < "$` + file + `"); ` +
		`elif command -v shasum >/dev/null 2>&1; then s=$(shasum -a 256 < "$` + file + `"); else s=; fi; s="${s%% *}"`
}

func lsScript(lines ...string) string { return posixCommand(strings.Join(lines, "; ")) }

// lsStaged is the staged build's path for operation op, as an sh word.
func lsStaged(op string) string { return lsIn("B", lsStagePrefix+op) }

// lsProbeScript looks at the computer and changes nothing: it prints os, arch, systemd (a user manager answers),
// linger, link (Persona Link there already), custom (another data directory than ~/.persona would be used),
// node_unit (a node service exists), node_bin (a node program to run as one) and end=1 last. Exit 0.
func lsProbeScript(p lsPaths) string {
	current, legacy := compat.EnvNames("PERSONA_HOME")
	lines := append(lsPrelude(),
		`echo "os=$(uname -s)"`, `echo "arch=$(uname -m)"`,
		`if command -v systemctl >/dev/null 2>&1 && [ -e `+shellQuote(p.systemd)+` ] && systemctl --user show-environment >/dev/null 2>&1; then echo systemd=1; else echo systemd=0; fi`,
		`if [ -e `+shellQuote(p.linger)+`/"$(id -un)" ]; then echo linger=1; else echo linger=0; fi`,
		`k=0; for p in `+lsLinkPaths()+`; do if e "$p"; then k=1; fi; done; echo "link=$k"`,
		`c=0; if [ -n "${`+current+`:-}" ] || [ -n "${`+legacy+`:-}" ]; then c=1; fi; if e "$O" && ! [ "$O" -ef "$D" ]; then c=1; fi; echo "custom=$c"`,
		`n=0; for p in `+lsIn("U", lsNodeUnit)+` `+lsIn("U", lsLegacyNodeUnit)+`; do if e "$p"; then n=1; fi; done; echo "node_unit=$n"`,
		`x=0; for p in `+lsIn("B", nodeproto.ProgramPersonad)+` `+lsIn("B", nodeproto.ProgramPersonaNode)+` `+lsIn("B", compat.DaemonBinary)+
			`; do if [ -f "$p" ] && [ -x "$p" ]; then x=1; fi; done; echo "node_bin=$x"`,
		"echo end=1")
	return lsScript(lines...)
}

// lsStageScript writes the gzipped build on stdin to the staged name for op, checks it is the build of sha-256 sum and
// runs here (`version`), and prints staged=1; on any failure it removes what it wrote (lsExitStage*).
func lsStageScript(op, sum string) string {
	return lsScript("set -u", `B="$HOME/.persona/bin"`, "f="+lsStaged(op),
		`mkdir -p "$B" || exit `+strconv.Itoa(lsExitStageWrite),
		`rm -f "$f"`,
		`if ! gzip -dc > "$f"; then rm -f "$f"; exit `+strconv.Itoa(lsExitStageCopy)+`; fi`,
		`chmod 755 "$f" || { rm -f "$f"; exit `+strconv.Itoa(lsExitStageWrite)+`; }`,
		lsSum("f"),
		`[ "$s" = `+shellQuote(sum)+` ] || { rm -f "$f"; exit `+strconv.Itoa(lsExitStageSum)+`; }`,
		`"$f" version </dev/null >/dev/null 2>&1 || { rm -f "$f"; exit `+strconv.Itoa(lsExitStageVersion)+`; }`,
		"echo staged=1")
}

// lsInstallScript refuses when Persona Link appeared since the probe, links the staged build of op into place as
// bin/persona-link (never over a file there), says placed=1, and runs its installer with the pairing link on stdin
// (`install --pair -` reads it to EOF): the script's exit is the installer's from there.
func lsInstallScript(op string) string {
	lines := append(lsPrelude(), "f="+lsStaged(op), "t="+lsIn("B", lsLinkBin),
		`for p in `+lsLinkPaths()+`; do if e "$p"; then rm -f "$f"; exit `+strconv.Itoa(lsExitInstallExists)+`; fi; done`,
		`[ -f "$f" ] || exit `+strconv.Itoa(lsExitInstallStaged),
		`ln "$f" "$t" 2>/dev/null || { rm -f "$f"; exit `+strconv.Itoa(lsExitInstallPlace)+`; }`,
		`rm -f "$f"`, "echo placed=1",
		`"$t" install --pair -`)
	return lsScript(lines...)
}

// lsUninstallScript undoes what the switch of op may have set up and checks it is gone: the staged build always;
// with node, the node service persona-link installed (there was none before); with purge, Persona Link's service,
// pairing, logs and program. Either runs only through the program this switch sent (bin/persona-link of sha-256 sum),
// never another one found there. clean=1, or lsExitUninstallStays when anything of it is still there.
func lsUninstallScript(op, sum string, node, purge bool) string {
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	lines := append(lsPrelude(), "f="+lsStaged(op), "t="+lsIn("B", lsLinkBin),
		"node="+flag(node), "purge="+flag(purge), "want="+shellQuote(sum),
		`rm -f "$f"`,
		`ours=0; if [ -f "$t" ] && ! [ -L "$t" ]; then `+lsSum("t")+`; if [ -n "$want" ] && [ "$s" = "$want" ]; then ours=1; fi; fi`,
		`if [ "$node" = 1 ] && [ "$ours" = 1 ] && e `+lsIn("U", lsNodeUnit)+`; then "$t" node uninstall </dev/null >/dev/null 2>&1; echo "node_uninstall=$?"; fi`,
		`if [ "$purge" = 1 ] && [ "$ours" = 1 ]; then "$t" uninstall --purge </dev/null >/dev/null 2>&1; echo "purge=$?"; fi`,
		`bad=0; if e "$f"; then bad=1; fi`,
		`if [ "$node" = 1 ] && e `+lsIn("U", lsNodeUnit)+`; then bad=1; fi`,
		`if [ "$purge" = 1 ]; then for p in "$t" `+lsIn("D", "link.json")+` `+lsIn("U", lsLinkUnit)+`; do if e "$p"; then bad=1; fi; done; fi`,
		`if [ "$bad" = 0 ]; then echo clean=1; else exit `+strconv.Itoa(lsExitUninstallStays)+`; fi`)
	return lsScript(lines...)
}
