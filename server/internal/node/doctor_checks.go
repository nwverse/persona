package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/nodeclient"
)

const doctorDialTimeout = 3 * time.Second

func checkNodeSocket(ctx context.Context, path string) DoctorCheck {
	c := DoctorCheck{Name: "node.sock"}
	if _, err := os.Stat(path); err != nil {
		c.Status, c.Detail = DoctorFail, "不存在"
		c.Hint = "没有 node 在监听：hook 和 MCP 会静默退回终端。确认 hub 已连上这台电脑；node 重启会重建"
		return c
	}
	ctx, cancel := context.WithTimeout(ctx, doctorDialTimeout)
	defer cancel()
	line, err := nodeclient.DialAt(ctx, path, sockRequest{Kind: "ping"})
	if err != nil {
		c.Status, c.Detail = DoctorFail, "文件在但连不上或没有应答（"+shorten(err.Error(), 80)+"）"
		c.Hint = "可能是旧 node 退出后留下的文件；重启这台电脑上的 node"
		return c
	}
	if !json.Valid(line) {
		c.Status, c.Detail = DoctorFail, "应答不是 JSON"
		c.Hint = "这个 socket 不是 node 的；重启 node"
		return c
	}
	c.Status, c.Detail = DoctorOK, "已应答"
	return c
}

func checkRuntimeSocket(ctx context.Context, path string) DoctorCheck {
	c := DoctorCheck{Name: "runtime.sock"}
	if _, err := os.Stat(path); err != nil {
		c.Status, c.Detail = DoctorWarn, "不存在"
		c.Hint = "没有 node 在监听：DSH、pi 这类 runtime 连不上；没用到可忽略"
		return c
	}
	ctx, cancel := context.WithTimeout(ctx, doctorDialTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		c.Status, c.Detail = DoctorFail, "文件在但连不上（"+shorten(err.Error(), 80)+"）"
		c.Hint = "可能是旧 node 退出后留下的文件；重启这台电脑上的 node"
		return c
	}
	conn.Close()
	c.Status, c.Detail = DoctorOK, "可连接"
	return c
}

func checkClaudeVersion(ctx context.Context, ask func(context.Context) string) DoctorCheck {
	c := DoctorCheck{Name: "claude-version"}
	v := ""
	if ask != nil {
		v = ask(ctx)
	}
	switch {
	case v == "":
		c.Status, c.Detail = DoctorWarn, "读不到 claude 的版本"
		c.Hint = "没装 Claude Code，或登录 shell 里找不到它；读不到时 Persona 按不支持 --plugin-dir 处理，hooks 走 --settings"
	case !versionAtLeast(v, pluginMinVersion):
		c.Status, c.Detail = DoctorWarn, fmt.Sprintf("%s（低于 %d.%d.%d，不支持 --plugin-dir）", v, pluginMinVersion[0], pluginMinVersion[1], pluginMinVersion[2])
		c.Hint = "hooks 退回 --settings，persona-office 技能不可用；升级 Claude Code"
	default:
		c.Status, c.Detail = DoctorOK, v+"，支持 --plugin-dir"
	}
	return c
}

// checkHerdr dials herdr's API socket. The engine is chosen by the hub when it connects and is not kept on this
// machine, so a socket that is not there is a warning: it only matters when herdr is this machine's engine.
func checkHerdr(ctx context.Context, path string) DoctorCheck {
	c := DoctorCheck{Name: "herdr"}
	if path == "" {
		c.Status, c.Detail = DoctorWarn, "找不到 herdr 的 socket 位置"
		c.Hint = "设了 HERDR_SOCKET_PATH 的话确认它指向 herdr；这台电脑的引擎不是 herdr 时可忽略"
		return c
	}
	ctx, cancel := context.WithTimeout(ctx, doctorDialTimeout)
	defer cancel()
	conn, err := herdr.Dial(ctx, path)
	if err != nil {
		c.Status, c.Detail = DoctorWarn, "连不上 herdr 的 socket"
		c.Hint = "引擎是 herdr 时先启动 herdr；这台电脑用别的引擎时可忽略"
		return c
	}
	conn.Close()
	c.Status, c.Detail = DoctorOK, "herdr 在监听"
	return c
}

// checkGlobalPermissionHooks looks at the user's own settings.json for synchronous PermissionRequest hooks (other
// tools put them there) and says how many; it never changes the file.
func checkGlobalPermissionHooks(configDir string) DoctorCheck {
	c := DoctorCheck{Name: "global-permission-hooks", Items: []string{}}
	b, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		c.Status, c.Detail = DoctorOK, "没有全局 settings.json"
		return c
	}
	if err != nil {
		c.Status, c.Detail = DoctorWarn, "读不了全局 settings.json（"+shorten(err.Error(), 60)+"）"
		return c
	}
	var f struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &f) != nil {
		c.Status, c.Detail = DoctorWarn, "全局 settings.json 不是合法 JSON"
		c.Hint = "Claude Code 自己也读不了它；Persona 不会改这个文件"
		return c
	}
	for _, m := range f.Hooks["PermissionRequest"] {
		for _, h := range m.Hooks {
			if !h.Async && (h.Type == "command" || h.Type == "") {
				c.Items = append(c.Items, shorten(h.Command, 80))
			}
		}
	}
	if len(c.Items) == 0 {
		c.Status, c.Detail = DoctorOK, "没有同步的 PermissionRequest 钩子"
		return c
	}
	c.Status = DoctorWarn
	c.Detail = fmt.Sprintf("%d 个同步的 PermissionRequest 钩子（%s）", len(c.Items), c.Items[0])
	c.Hint = "它们和 Persona 的手机审批同时拦着权限请求，可能互相影响；Persona 不会改这个文件，需要时自己检查"
	return c
}
