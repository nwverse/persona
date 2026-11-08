package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
)

// The mesh from the command line on the hub's machine (docs/server/mesh.md "Command line"): the same peers API as the admin page,
// under /api/v1/admin/cli/, over loopback with the master token (peerCLIRoutes).

// PairPeer prints a code another hub pairs with this one by (`personad hub pair --peer`): the code alone on stdout, what
// to do with it on stderr.
func PairPeer() error {
	cfg, err := runningHubConfig()
	if err != nil {
		return err
	}
	var code peerCode
	if err := cfg.adminCall(context.Background(), http.MethodPost, "/api/v1/admin/cli/peers/code", nil, &code); err != nil {
		return err
	}
	fmt.Println(code.Code)
	fmt.Fprintf(os.Stderr, "\n这是这台 hub 的互联码，%d 分钟内有效，只能用一次。\n"+
		"在另一台 hub 上：管理界面「互联 › 输入互联码」里粘贴；或者在那台机器上运行 personad hub peer add '<互联码>'。\n",
		max(1, int(time.Until(code.ExpiresAt).Round(time.Minute).Minutes())))
	return nil
}

// PeerCommand is `personad hub peer add <code>|-`, `personad hub peer list`, `personad hub peer remove <hub_id>`,
// `personad hub peer center [<hub_id>] [--confirm]` (set as center; this hub when no id is given).
func PeerCommand(args []string, stdin io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：personad hub peer add <互联码>|- ｜ list ｜ remove <hub_id> ｜ center [<hub_id>] [--confirm]")
	}
	cfg, err := runningHubConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch args[0] {
	case "add":
		if len(args) != 2 {
			return errors.New("用法：personad hub peer add <互联码>（- 从标准输入读）")
		}
		code := args[1]
		if code == "-" {
			line, err := bufio.NewReader(stdin).ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("读不到互联码：%w", err)
			}
			code = strings.TrimSpace(line)
		}
		var p peerItem
		if err := cfg.adminCall(ctx, http.MethodPost, "/api/v1/admin/cli/peers", map[string]string{"code": code}, &p); err != nil {
			return err
		}
		fmt.Fprintf(out, "已和「%s」（%s）互联：%s\n", p.Name, p.HubID, peerStateText(p))
		return nil
	case "list":
		var res struct {
			Peers      []peerItem      `json:"peers"`
			Discovered []discoveredHub `json:"discovered"`
		}
		if err := cfg.adminCall(ctx, http.MethodGet, "/api/v1/admin/cli/peers", nil, &res); err != nil {
			return err
		}
		var mesh struct {
			Role   string `json:"role"`
			Term   int64  `json:"term"`
			Center struct {
				HubID  string `json:"hub_id"`
				Name   string `json:"name"`
				Online bool   `json:"online"`
			} `json:"center"`
		}
		if err := cfg.adminCall(ctx, http.MethodGet, "/api/v1/admin/cli/mesh", nil, &mesh); err == nil {
			if mesh.Role == "center" {
				fmt.Fprintf(out, "这台是中心（任期 %d）。\n", mesh.Term)
			} else {
				state := "在线"
				if !mesh.Center.Online {
					state = "连不上"
				}
				fmt.Fprintf(out, "这台是成员，中心是「%s」（%s，%s，任期 %d）。\n", mesh.Center.Name, mesh.Center.HubID, state, mesh.Term)
			}
		}
		if len(res.Peers) == 0 {
			fmt.Fprintln(out, "还没有互联的 hub。")
		}
		for _, p := range res.Peers {
			role := map[string]string{"center": "中心", "member": "成员"}[p.Role]
			fmt.Fprintf(out, "%s  %s  %s  %s  %d 台电脑\n", p.HubID, p.Name, role, peerStateText(p), p.Machines)
		}
		for _, d := range res.Discovered {
			fmt.Fprintf(out, "局域网里发现：%s  %s  %s\n", d.HubID, d.Name, strings.Join(d.Addresses, " "))
		}
		return nil
	case "remove":
		if len(args) != 2 {
			return errors.New("用法：personad hub peer remove <hub_id>")
		}
		if err := cfg.adminCall(ctx, http.MethodDelete, "/api/v1/admin/cli/peers/"+args[1], nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "已断开并撤销。")
		return nil
	case "center":
		target, confirm := "", false
		for _, a := range args[1:] {
			if a == "--confirm" {
				confirm = true
			} else {
				target = a
			}
		}
		if target == "" {
			var health struct {
				HubID string `json:"hub_id"`
			}
			if err := cfg.adminCall(ctx, http.MethodGet, "/api/v1/health", nil, &health); err != nil {
				return err
			}
			target = health.HubID
		}
		var res struct {
			Term   int64 `json:"term"`
			Center struct {
				Name string `json:"name"`
			} `json:"center"`
		}
		err := cfg.adminCall(ctx, http.MethodPost, "/api/v1/admin/cli/peers/"+target+"/center", map[string]bool{"confirm": confirm}, &res)
		if err != nil {
			if strings.Contains(err.Error(), "连不上：确认") {
				return fmt.Errorf("%w（确认就加 --confirm）", err)
			}
			return err
		}
		fmt.Fprintf(out, "中心现在是「%s」（任期 %d）。\n", res.Center.Name, res.Term)
		return nil
	}
	return fmt.Errorf("不认识的子命令 %q", args[0])
}

func peerStateText(p peerItem) string {
	s := map[string]string{"connected": "已连接", "connecting": "连接中", "offline": "连不上", "refused": "对方不认这台的凭据"}[p.State]
	if s == "" {
		s = p.State
	}
	if p.RTTMS != nil {
		s += fmt.Sprintf("（%d ms）", *p.RTTMS)
	}
	if p.Error != "" {
		s += "：" + p.Error
	}
	return s
}

// adminCall calls this machine's hub over loopback with the master token.
func (c *Config) adminCall(ctx context.Context, method, path string, body, out any) error {
	_, port, err := net.SplitHostPort(c.Listen)
	if n, perr := strconv.Atoi(port); err != nil || perr != nil || n <= 0 || n > 65535 {
		return errors.New("hub.json 的 listen 没有有效端口")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+net.JoinHostPort("127.0.0.1", port)+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json") // the admin API takes JSON writes only
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("无法连接本机 hub：hub 可能没在运行，或没监听回环地址")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		why := apierr.Describe(b) // with the cause the hub moved to detail (#5728)
		if why == "" {
			why = resp.Status
		}
		return fmt.Errorf("hub 回了 %d：%s", resp.StatusCode, why)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
