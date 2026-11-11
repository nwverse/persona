package node

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeclient"
)

// `personad node doctor` looks at what a node on this machine needs for the sessions Persona starts to reach the
// phone: the sockets hooks and MCP dial, the files claude loads (compared with what this binary would write now, the
// same bytes the node writes at startup) and the programs those files run. It only reads. --fix writes Persona's
// own generated files again (writeGeneratedConfig, the startup path) and, on a Mac and outside the node, the resident
// node's LaunchAgent's ProcessType (launchagent.go); nothing else: the user's own Claude Code settings are only ever
// looked at. The path-access check (preflight.go) reads what a claude start reads, in a child it kills when a read
// hangs. Background: a redeploy once deleted node.sock and every hook and MCP call fell back to the terminal without a
// word (docs/lessons/server.md).

// Check statuses.
const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

// DoctorCheck is one line of the report; the names are stable for scripts.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// State is current / stale / missing for a generated file or directory, "" for the other checks.
	State  string   `json:"state"`
	Detail string   `json:"detail"`
	Hint   string   `json:"hint"`
	Items  []string `json:"items"` // what differs, which commands, ...; never null
}

// DoctorReport is what `node doctor` prints (--json: as it is).
type DoctorReport struct {
	OK     bool          `json:"ok"` // no check failed
	Dir    string        `json:"dir"`
	Checks []DoctorCheck `json:"checks"`
	// Fixed names what --fix put right: the generated files it wrote again; on a Mac launchd-process-type when it
	// wrote ProcessType into the LaunchAgent and launchd-reload when it loaded the agent again.
	Fixed []string `json:"fixed"`
}

// DoctorOptions says where to look; DefaultDoctorOptions fills them in for this machine (tests give their own).
type DoctorOptions struct {
	Dir string // the node's directory (~/.persona)
	Bin string // the program the generated files should run: this binary
	// Sockets: node.sock, runtime.sock and herdr's API socket.
	NodeSock, RuntimeSock, HerdrSock string
	// ClaudeConfigDir is where the user's own Claude Code settings are (read, never written).
	ClaudeConfigDir string
	// ClaudeVersion asks claude's version ("" when unknown).
	ClaudeVersion func(context.Context) string
	// LaunchAgent is the resident node's launchd agent on a Mac (NodeLaunchAgentPath; pickNodeLaunchdLabel says which
	// when a Mac keeps its previously installed label); "" elsewhere, and the launchd-process-type check is
	// left out.
	LaunchAgent string
	// LaunchdTarget is that agent as launchctl names it (gui/<uid>/com.persona.node, or the older label).
	LaunchdTarget string
	// Launchctl runs launchctl and returns its output; nil: launchd is not asked (the check reads the plist only).
	Launchctl func(ctx context.Context, args ...string) (string, error)
	// InsideNode: the doctor runs in the resident node (node.doctor) or under it. --fix then leaves the LaunchAgent
	// alone: loading it again stops the node, and with it the doctor, half way.
	InsideNode bool
	// RestartWait caps the wait, after --fix loaded the agent again, for the new node to answer (0: nodeRestartWait).
	RestartWait time.Duration
	// Preflight is the path-access check (preflight.go); nil leaves it out.
	Preflight func(context.Context) DoctorCheck
	// Fix writes the generated files again when they are stale or missing, and (outside the node) writes
	// ProcessType=Interactive into the LaunchAgent and loads it again.
	Fix bool
}

// DefaultDoctorOptions is this machine's: the same places the node itself uses, without creating or moving any.
func DefaultDoctorOptions() (DoctorOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return DoctorOptions{}, err
	}
	bin, err := os.Executable()
	if err != nil {
		return DoctorOptions{}, err
	}
	if p, err := filepath.EvalSymlinks(bin); err == nil {
		bin = p
	}
	opts := DoctorOptions{
		Dir: identity.DataDir(home), Bin: bin,
		NodeSock: nodeclient.SocketPath(), RuntimeSock: RuntimeSocketPath(), HerdrSock: herdr.SocketPath(),
		ClaudeConfigDir: claudeDirOf(home), ClaudeVersion: claudeVersion,
	}
	opts.withLaunchd(home)
	// Under the node's agent (a claude the node started runs the doctor) a reload would stop the doctor too: under
	// either label, today's or the one a Mac set up before the rename keeps.
	opts.InsideNode = isNodeLaunchdLabel(os.Getenv("XPC_SERVICE_NAME"))
	if runtime.GOOS != "windows" {
		opts.Preflight = preflightFromNode(opts.NodeSock, home, opts.ClaudeConfigDir)
	}
	return opts, nil
}

// withLaunchd points the launchd-process-type check at the resident node's agent, on a Mac only.
func (o *DoctorOptions) withLaunchd(home string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// launchctl is asked only when both labels have a plist; bounded, as everything else here.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o.withLaunchAgent(ctx, home, launchdDomain(), os.Getenv("XPC_SERVICE_NAME"), runLaunchctl)
}

// withLaunchAgent points the check at the agent pickNodeLaunchdLabel picks in domain, asking launchctl.
func (o *DoctorOptions) withLaunchAgent(ctx context.Context, home, domain, self string,
	launchctl func(context.Context, ...string) (string, error)) {
	label := pickNodeLaunchdLabel(ctx, home, domain, self, launchctl)
	o.LaunchAgent, o.LaunchdTarget, o.Launchctl = NodeLaunchAgentPath(home, label), domain+"/"+label, launchctl
}

// RunDoctor runs the checks. With opts.Fix it first puts right what it may (the generated files; outside the node, the
// LaunchAgent's ProcessType), then checks again.
func RunDoctor(ctx context.Context, opts DoctorOptions) DoctorReport {
	if pre := opts.Preflight; pre != nil { // read once: nothing a fix does changes what it reads
		var once sync.Once
		var c DoctorCheck
		opts.Preflight = func(ctx context.Context) DoctorCheck {
			once.Do(func() { c = pre(ctx) })
			return c
		}
	}
	rep := doctorChecks(ctx, opts)
	if !opts.Fix {
		return rep
	}
	var fixed []string
	var failed []DoctorCheck
	var stale []string
	for _, c := range rep.Checks {
		if c.State == "stale" || c.State == "missing" {
			stale = append(stale, c.Name)
		}
	}
	if len(stale) > 0 {
		if err := writeGeneratedConfig(opts.Dir, opts.Bin); err != nil {
			failed = append(failed, DoctorCheck{Name: "fix", Status: DoctorFail, Detail: "重新生成失败：" + err.Error(),
				Hint: "检查 " + opts.Dir + " 的权限和磁盘空间", Items: []string{}})
		} else {
			fixed = append(fixed, stale...)
		}
	}
	var notBack []string
	logPath := ""
	if opts.LaunchAgent != "" && !opts.InsideNode {
		a := inspectLaunchAgent(ctx, opts)
		wrote, reloaded, err := fixLaunchAgent(ctx, opts, a)
		if err != nil {
			failed = append(failed, DoctorCheck{Name: "fix", Status: DoctorFail, Detail: err.Error(),
				Hint: "也可以重新安装常驻 node 的服务（scripts/deploy/node-launchd.sh install）", Items: []string{opts.LaunchAgent}})
		}
		if wrote {
			fixed = append(fixed, launchdCheck)
		}
		if reloaded {
			fixed = append(fixed, launchdReloaded)
			// bootstrap returns before the new node listens: checked at once, its sockets would look dead.
			notBack, logPath = waitForRestart(ctx, opts, rep), a.logPath
		}
	}
	if len(fixed) == 0 && len(failed) == 0 {
		return rep
	}
	rep = doctorChecks(ctx, opts)
	notBackYet(&rep, notBack, opts.RestartWait, logPath)
	rep.Fixed = append(rep.Fixed, fixed...)
	if len(failed) > 0 {
		rep.Checks = append(rep.Checks, failed...)
		rep.OK = false
	}
	return rep
}

func doctorChecks(ctx context.Context, opts DoctorOptions) DoctorReport {
	rep := DoctorReport{Dir: opts.Dir, Fixed: []string{}}
	add := func(c DoctorCheck) {
		if c.Items == nil {
			c.Items = []string{}
		}
		rep.Checks = append(rep.Checks, c)
	}
	var pre chan DoctorCheck
	if opts.Preflight != nil { // reads in a child of its own, alongside the other checks
		pre = make(chan DoctorCheck, 1)
		go func() { pre <- opts.Preflight(ctx) }()
	}
	add(checkNodeSocket(ctx, opts.NodeSock))
	add(checkRuntimeSocket(ctx, opts.RuntimeSock))
	for _, c := range checkGenerated(opts) {
		add(c)
	}
	add(checkCommandPaths(opts.Dir))
	add(checkClaudeVersion(ctx, opts.ClaudeVersion))
	add(checkHerdr(ctx, opts.HerdrSock))
	add(checkGlobalPermissionHooks(opts.ClaudeConfigDir))
	add(checkAgentSandbox(opts.Dir))
	if opts.LaunchAgent != "" {
		add(checkLaunchAgent(inspectLaunchAgent(ctx, opts), opts))
	}
	if pre != nil {
		add(<-pre)
	}
	rep.OK = !slices.ContainsFunc(rep.Checks, func(c DoctorCheck) bool { return c.Status == DoctorFail })
	return rep
}

// shorten cuts s to at most n runes, with … when it cut.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// DoctorCommand is `personad node doctor [--json] [--fix]`: it prints the report to out and says whether a check
// failed (the caller exits non-zero then).
func DoctorCommand(ctx context.Context, args []string, out io.Writer) (failed bool, err error) {
	if len(args) > 0 && args[0] == preflightChildArg { // the preflight's reads, in a child of the doctor's (preflight.go)
		preflightChild(args[1:], out)
		return false, nil
	}
	flags := flag.NewFlagSet("node doctor", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	fix := flags.Bool("fix", false, "重新生成 Persona 自己生成的文件；Mac 上给常驻 node 的 LaunchAgent 写上 ProcessType=Interactive 并重新加载（不碰用户的全局配置）")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	if flags.NArg() != 0 {
		return false, errors.New("node doctor 不接受位置参数")
	}
	opts, err := DefaultDoctorOptions()
	if err != nil {
		return false, err
	}
	opts.Fix = *fix
	rep := RunDoctor(ctx, opts)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return !rep.OK, enc.Encode(rep)
	}
	PrintDoctor(out, rep)
	return !rep.OK, nil
}

// doctorLabels are the line prefixes of the text report, the same words `hub doctor` uses (hub/doctor.go).
var doctorLabels = map[string]string{DoctorOK: "正常", DoctorWarn: "注意", DoctorFail: "有问题"}

// PrintDoctor writes rep, one line per check: label | name: what was found; what to do about it (full-width separators).
func PrintDoctor(out io.Writer, rep DoctorReport) {
	fmt.Fprintf(out, "node 自检（%s）\n", rep.Dir)
	counts := map[string]int{}
	for _, c := range rep.Checks {
		counts[c.Status]++
		line := fmt.Sprintf("%s｜%s：%s", doctorLabels[c.Status], c.Name, c.Detail)
		if c.Hint != "" && c.Status != DoctorOK {
			line += "；" + c.Hint
		}
		fmt.Fprintln(out, line)
	}
	var regenerated []string
	for _, f := range rep.Fixed {
		switch f {
		case launchdCheck:
			fmt.Fprintln(out, "已给常驻 node 的 LaunchAgent 写上 ProcessType=Interactive")
		case launchdReloaded:
			fmt.Fprintln(out, "已重新加载常驻 node 的 LaunchAgent（node 已重启）")
		default:
			regenerated = append(regenerated, f)
		}
	}
	if len(regenerated) > 0 {
		fmt.Fprintf(out, "已重新生成：%s\n", strings.Join(regenerated, "、"))
	}
	fmt.Fprintf(out, "共 %d 项：%d 正常，%d 注意，%d 有问题\n", len(rep.Checks), counts[DoctorOK], counts[DoctorWarn], counts[DoctorFail])
}
