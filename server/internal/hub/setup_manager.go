package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/managerconf"
)

// The built-in manager's own service in `personad hub setup` (docs/assistant/managers.md "The built-in manager as its
// own service"). A new install's hub.json says assistant.mode service, so the hub never starts the built-in manager;
// an existing hub's stays as it is unless --manager-service. Setup then makes the one-time code (as `persona-hub
// manager-code` does) and runs `<root>/bin/personad manager install --builtin --join <the hub on loopback>` with the
// code in its environment: the manager's program keeps a binding that works or trades the code, and writes its
// manager.json and its own unit (persona-manager-assistant.service, or the launchd agent com.persona.manager.assistant)
// itself; the hub writes neither. Last, setup waits until the hub has it on the bus.

// canManagerService: setup installs services here, and so the built-in manager's own service too.
func (e *setupEnv) canManagerService() bool {
	return !e.o.noService && (e.goos == "linux" || e.goos == "darwin") && !e.isInsideContainer()
}

// noManagerServiceWhy says why canManagerService is false.
func (e *setupEnv) noManagerServiceWhy() string {
	switch {
	case e.o.noService:
		return "--no-service"
	case e.isInsideContainer():
		return "在容器里"
	}
	return e.goos + " 上没有常驻服务"
}

// freshManagerMode is a new install's assistant.mode: service when setup installs the built-in manager's service
// (service true), else the default (child). A mode already in the file stays, except service where nothing would run
// the built-in manager (--no-service after a run that could not install the services: no hub has read this file yet).
// assistant.external is left alone: the built-in manager runs elsewhere then.
func freshManagerMode(o *jsonObject, service bool) error {
	a, err := o.object("assistant")
	if err != nil {
		return err
	}
	if a == nil {
		a = &jsonObject{}
	}
	var external bool
	if raw, ok := a.get("external"); ok && json.Unmarshal(raw, &external) == nil && external {
		return nil
	}
	var mode string
	if raw, ok := a.get("mode"); ok {
		_ = json.Unmarshal(raw, &mode)
	}
	switch {
	case service && mode == "":
		if err := a.set("mode", AssistantModeService); err != nil {
			return err
		}
	case !service && mode == AssistantModeService:
		a.del("mode")
	default:
		return nil
	}
	o.setObject("assistant", a)
	return nil
}

// toManagerService sets an existing hub's assistant.mode to service (--manager-service); changed false when it was.
func (e *setupEnv) toManagerService() (changed bool, err error) {
	err = editHubJSON(e.root, func(o *jsonObject) error {
		a, err := o.object("assistant")
		if err != nil {
			return err
		}
		if a == nil {
			a = &jsonObject{}
		}
		var external bool
		if raw, ok := a.get("external"); ok && json.Unmarshal(raw, &external) == nil && external {
			return errManagerExternal
		}
		var mode string
		if raw, ok := a.get("mode"); ok {
			_ = json.Unmarshal(raw, &mode)
		}
		if mode == AssistantModeService {
			return nil
		}
		changed = true
		if err := a.set("mode", AssistantModeService); err != nil {
			return err
		}
		o.setObject("assistant", a)
		return nil
	})
	switch {
	case errors.Is(err, errManagerExternal):
		return false, e.fail("config", "hub.json 的 assistant.external 是 true：内置管理者在别处运行，--manager-service 不动它",
			"要在这台装它的常驻服务，先去掉 "+e.hubJSON()+" 里的 assistant.external，再重跑")
	case err != nil:
		return false, e.fail("config", "写不了 "+e.hubJSON()+"："+err.Error(), "检查这个目录的权限")
	}
	return changed, nil
}

var errManagerExternal = errors.New("assistant.external")

// managerLog is where the built-in manager's service logs, for the lines setup prints.
func (e *setupEnv) managerLog() string {
	if e.goos == "darwin" {
		return "看 " + filepath.Join(managerconf.DefaultDir(e.root), "manager.log") + " 里内置管理者的日志"
	}
	return "运行 journalctl --user -u " + managerconf.BuiltinService + " -n 50 看内置管理者的日志"
}

// builtinManager is setup's manager step.
func (e *setupEnv) builtinManager(ctx context.Context, cfg *setupConfig) error {
	a := cfg.Assistant
	bin := filepath.Join(e.root, "bin", "personad")
	switch {
	case a.External:
		e.report("manager", true, "hub.json 的 assistant.external：内置管理者在别处运行，没动它", "")
		return nil
	case a.StartsBuiltin():
		next := ""
		if e.canManagerService() {
			next = "要把它改成自己的常驻服务（hub 重启、升级时它不跟着重启）：personad hub setup --manager-service"
		}
		e.report("manager", true, "hub 自己拉起内置管理者（assistant.mode child）", next)
		return nil
	case !a.ServiceBound():
		e.report("manager", false, "hub.json 的 assistant.mode 是 "+a.Mode+"：hub 不认识，内置管理者没有运行",
			"把 "+e.hubJSON()+" 里的 assistant.mode 改成 service（或者删掉，让 hub 自己拉起它），再重跑")
		return nil
	case !e.canManagerService():
		e.report("manager", true, "内置管理者是自己的服务（assistant.mode service），这次没装它（"+e.noManagerServiceWhy()+"）",
			"自己让它一直运行：personad hub manager-code 拿一个码，personad manager join --builtin "+hubLocalURL(cfg.Listen)+
				" <码> 绑定，再一直运行 PERSONA_ASSISTANT_DEPLOY=service "+bin+" assistant")
		return nil
	}
	code, err := e.builtinCode(ctx, cfg.Config)
	if err != nil {
		return e.fail("manager", "没拿到绑定内置管理者的码："+err.Error(), "重跑 personad hub setup")
	}
	// The code goes in the environment, never on the command line: any user here can read that while it runs, and
	// the hub trades this code from its own machine without another credential.
	out, err := e.run(ctx, []string{"PERSONA_HOME=" + e.root, managerconf.BuiltinCodeEnv + "=" + code},
		bin, "manager", "install", "--builtin", "--join", hubLocalURL(cfg.Listen))
	if err != nil {
		why := lastLine(string(out))
		if why == "" {
			why = err.Error()
		}
		return e.fail("manager", "内置管理者的常驻服务没装好："+why, "按这句处理后重跑 personad hub setup")
	}
	unit := managerconf.BuiltinService
	if e.goos == "darwin" {
		unit = managerconf.BuiltinLabel
	}
	wait := e.managerWait
	deadline := time.Now().Add(wait)
	for {
		if e.builtinOnline(ctx, cfg.Config) {
			e.report("manager", true, "内置管理者是自己的常驻服务（"+unit+"），已绑定这个 hub、连上了", "")
			return nil
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			break
		}
		e.sleep(time.Second)
	}
	// The hub runs and the phone can pair: say what is wrong and go on.
	e.report("manager", false, fmt.Sprintf("内置管理者的常驻服务（%s）装好了，但 %d 秒内没连上 hub", unit, int(wait.Seconds())), e.managerLog())
	return nil
}

// builtinOnline: the hub has the built-in manager on its bus as a service of its own (GET /api/v1/managers on loopback).
func (e *setupEnv) builtinOnline(ctx context.Context, cfg *Config) bool {
	client, done := loopbackClient()
	defer done()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", portOf(cfg.Listen))+"/api/v1/managers", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Managers []managerInfo `json:"managers"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil {
		return false
	}
	for _, m := range body.Managers {
		if m.ID == assistantwire.AssistantID { // its hello's deploy is "service" then (PERSONA_ASSISTANT_DEPLOY)
			return m.Online && m.Deploy == AssistantModeService
		}
	}
	return false
}

// builtinCode makes a one-time code (10 minutes) that binds the built-in manager's own service to the hub of cfg, in
// its database: `persona-hub manager-code` and setup.
func builtinCode(ctx context.Context, cfg *Config) (string, time.Time, error) {
	if cfg.DatabaseURL == "" {
		return "", time.Time{}, errors.New("hub.json has no database_url")
	}
	database, err := db.Open(ctx, cfg.DatabaseURL, db.HubSet)
	if err != nil {
		return "", time.Time{}, err
	}
	defer database.Close()
	h := &Hub{cfg: cfg, store: database, logger: log.New(io.Discard, "", 0)}
	return h.newInstallCode(ctx, assistantwire.AssistantID, "助手") // i18n-ignore: its identity's name, as assistant-token gives it
}

// lastLine is the last line of s with text.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
