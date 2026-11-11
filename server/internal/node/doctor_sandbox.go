package node

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/extension/sandbox"
)

// checkAgentSandbox is the doctor's line for the agent sandbox: whether it is on and where from, whether this machine
// can run it, and each kind of start's last status.
func checkAgentSandbox(dir string) DoctorCheck {
	c := DoctorCheck{Name: "agent-sandbox", Status: DoctorOK, Items: []string{}}
	on, from := agentSandboxOn(dir)
	support := agentSandboxProbe() // macOS runs sandbox-exec true; Linux runs a real default-allow bwrap probe
	sup := sandbox.AgentSupport(support)
	last := readAgentSandboxStatus(dir)
	wheres := make([]string, 0, len(last))
	for w := range last {
		wheres = append(wheres, w)
	}
	slices.Sort(wheres)
	for _, w := range wheres {
		r := last[w]
		item := fmt.Sprintf("%s：%s（%s）", w, agentSandboxWords[r.State], r.At.Local().Format("01-02 15:04"))
		if r.Why != "" {
			item += "：" + r.Why
		}
		c.Items = append(c.Items, item)
	}
	switch {
	case !on:
		c.Detail = "未开启（默认关闭）"
		if from != "" {
			c.Detail = "未开启（" + from + "）"
		}
		switch sup.State {
		case sandbox.AgentActive:
			c.Detail += "；本机可以开启"
			c.Hint = "开启：touch " + filepath.Join(dir, sandbox.AgentOnFile) + "；见 docs/server/agent-sandbox.md"
		case sandbox.AgentUnsupported:
			c.Detail += "；不支持：" + sup.Why
		default:
			c.Detail += "；工具不可用：" + sup.Why
		}
	case sup.State == sandbox.AgentUnsupported:
		c.Status, c.Detail = DoctorWarn, "已开启，但不支持："+sup.Why
		c.Hint = "agent 照常启动，不进沙箱"
	case sup.State == sandbox.AgentUnavailable:
		c.Status, c.Detail = DoctorWarn, "已开启，但工具不可用："+sup.Why
		c.Hint = "agent 照常启动，不进沙箱"
	default:
		c.Detail = "已开启（" + from + "）；" + agentSandboxLimits(support.OS)
		c.Hint = "环境变量 " + agentSandboxEnv + " 只对设置它的进程生效（node 看 launchd 的环境，personad launch 看 pane 的 shell）"
		for _, w := range wheres {
			if s := last[w].State; s == sandbox.AgentSkipped || s == sandbox.AgentUnavailable {
				c.Status = DoctorWarn
				c.Detail += "；有启动被跳过，见下面的原因"
				c.Hint = "被跳过的启动照常运行、不在沙箱里"
				break
			}
		}
	}
	if len(c.Items) > 0 {
		c.Detail += "｜最近：" + strings.Join(c.Items, "；")
	}
	return c
}

// agentSandboxLimits is what the doctor reminds of while the sandbox is on (docs/server/agent-sandbox.md): what it does not
// stop, which agents it does not cover, and platform-specific behavior that needs a restart or stops working.
func agentSandboxLimits(goos string) string {
	limits := "挡不住 docker；在 herdr 或 Orca 里不经托管启动器（personad launch）启动的 agent，" +
		"比如直接用 claude 或 ~/.zshrc 里的启动器，包括 herdr 重启后接着跑的，不在沙箱里"
	switch goos {
	case "darwin":
		return limits + "；沙箱里 ps、top、sudo 这类 setuid 程序起不来"
	case "linux":
		return limits + "；node 重启后沙箱内 agent 也要一并重启（node.sock 是启动时快照）"
	default:
		return limits
	}
}

// agentSandboxWords are the statuses in the doctor's words.
var agentSandboxWords = map[string]string{
	sandbox.AgentOff: "未开启", sandbox.AgentActive: "已在沙箱里", sandbox.AgentSkipped: "跳过",
	sandbox.AgentUnsupported: "不支持", sandbox.AgentUnavailable: "工具不可用",
}
