package hub

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// A full client's client_hello.locale becomes the user's language and this connection's; an answer-only one (the
// watch) only its connection's; an invalid or missing one changes nothing.
func TestClientHelloLocale(t *testing.T) {
	h := newTestHub(t)
	full := &client{out: make(chan []byte, 16), role: roleFull, hub: h}
	if h.userLang() != i18n.ZhHans || full.lang() != i18n.ZhHans {
		t.Fatal("nothing said yet should be zh-Hans")
	}
	h.onClientHello(full, []byte(`{"locale":"en"}`))
	if full.lang() != i18n.En || h.userLang() != i18n.En {
		t.Fatalf("full client: conn %s, user %s", full.lang(), h.userLang())
	}
	h.onClientHello(full, []byte(`{"platform":"web"}`))
	if full.lang() != i18n.En {
		t.Fatal("a hello without locale must keep the connection's")
	}
	h.onClientHello(full, []byte(`{"locale":"fr"}`))
	if h.userLang() != i18n.En {
		t.Fatal("an unsupported code must not change the user's language")
	}
	watch := &client{out: make(chan []byte, 16), role: roleAnswer, hub: h}
	h.onClientHello(watch, []byte(`{"locale":"ja"}`))
	if watch.lang() != i18n.Ja || h.userLang() != i18n.En {
		t.Fatalf("watch: conn %s, user %s", watch.lang(), h.userLang())
	}
	quiet := &client{out: make(chan []byte, 16), role: roleFull, hub: h, acceptLang: i18n.Ja}
	if quiet.lang() != i18n.Ja {
		t.Fatal("the upgrade request's Accept-Language is the connection's until a hello says one")
	}
}

// Each device's push reads in its language; one without falls back to the user's.
func TestPushLanguagePerDevice(t *testing.T) {
	p := &Pusher{userLang: func() i18n.Lang { return i18n.Ja }}
	if p.langOf(Device{Locale: "en"}) != i18n.En || p.langOf(Device{}) != i18n.Ja {
		t.Fatal("langOf")
	}
	ix := proto.Interaction{Kind: proto.IxPermission, Agent: "codex"}
	title := func(l i18n.Lang) any {
		return interactionPayloadIn(l, ix, "box")["aps"].(map[string]any)["alert"].(map[string]any)["title"]
	}
	if title(i18n.ZhHans) != "Codex 需要你授权" || title(i18n.En) != "Codex needs your permission" {
		t.Fatalf("titles %v / %v", title(i18n.ZhHans), title(i18n.En))
	}
	if interactionPayload(ix, "box")["aps"].(map[string]any)["alert"].(map[string]any)["title"] != "Codex 需要你授权" {
		t.Fatal("the default payload must stay Chinese")
	}
	done := donePayloadIn(i18n.En, "m", "box", "s", "title", "")
	if done["aps"].(map[string]any)["alert"].(map[string]any)["body"] != "Done" {
		t.Fatal("done body")
	}
	if done["aps"].(map[string]any)["category"] != "SESSION_DONE" {
		t.Fatal("a done push carries the category iOS replies from")
	}
}

// An i18n.Message error reads in the request's language; other errors are as they were.
func TestErrorInRequestLanguage(t *testing.T) {
	h := newTestHub(t)
	handler := logRequests(h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plain" {
			writeErr(w, http.StatusBadRequest, errors.New("device_id is required"))
			return
		}
		writeErr(w, http.StatusConflict, i18n.Err("push.done_body"))
	}))
	call := func(path, lang string) string {
		r := httptest.NewRequest("GET", path, nil)
		if lang != "" {
			r.Header.Set("Accept-Language", lang)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Body.String()
	}
	if got := call("/x", "ja"); !strings.Contains(got, "完了しました") {
		t.Fatalf("ja: %s", got)
	}
	if got := call("/x", "en-US,en;q=0.9"); !strings.Contains(got, `"Done"`) {
		t.Fatalf("browser header: %s", got)
	}
	if got := call("/x", ""); !strings.Contains(got, "已完成") {
		t.Fatalf("no header is the user's language: %s", got)
	}
	if got := call("/plain", "ja"); !strings.Contains(got, "device_id is required") {
		t.Fatalf("plain error: %s", got)
	}
}

// An injected message's line reads in the user's language; the message the model reads stays as it is.
func TestInjectedLineLanguage(t *testing.T) {
	in := &proto.Injected{Kind: proto.InjectedTaskNotice, State: "done", Title: "写周报", Line: "任务「写周报」做完了"}
	if injectedLine(i18n.ZhHans, in) != in.Line {
		t.Fatal("zh-Hans must stay what ParseInjected wrote")
	}
	if got := injectedLine(i18n.En, in); got != "Task “写周报” is done" {
		t.Fatalf("en: %q", got)
	}
	lost := &proto.Injected{Kind: proto.InjectedSystem, State: "lost_tasks", Line: "node 重启过，3 个后台任务停了"}
	if got := injectedLine(i18n.Ja, lost); got != "ノードが再起動し、3件のバックグラウンドタスクが停止しました" {
		t.Fatalf("ja: %q", got)
	}
	one := &proto.Injected{Kind: proto.InjectedSystem, State: "lost_tasks", Line: "node 重启过，1 个后台任务停了"}
	if got := injectedLine(i18n.En, one); got != "The node restarted and 1 background task stopped" {
		t.Fatalf("en one: %q", got)
	}
}
