package node

// A dev server preview (docs/preview.md): preview.open makes a tab of the headless browser (preview_browser.go) show
// http://localhost:<port><path> and ties it to a stream (streams.Mux, like a forwarded port's) on which each message
// between the web and the node is one preview.Frame. The node sends pictures of the page (Page.startScreencast) and
// what it is showing; the web sends what the user does in it. The page's top frame stays on this computer's loopback
// addresses: a link to anywhere else is not followed in the preview, the web is told to open it in a new tab.
//
// The tab's goroutine handles everything in order (CDP events and the web's messages) and never waits on a CDP
// answer that may depend on an event it has yet to handle (a navigation waits on Fetch.requestPaused): such calls run
// on goroutines of their own and post their result back.

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/preview"
	"github.com/nwverse/persona/server/internal/rpc"
)

//go:embed preview_pick.js
var previewPickJS string

// previewTitleBinding is the function the page's title is reported through (Runtime.addBinding): the browser does not
// say when a page sets its title.
const previewTitleBinding = "__personaPreviewTitle"

// previewTitleJS runs in each new document of the top frame and reports its title whenever it changes.
const previewTitleJS = `(() => {
  if (window.top !== window) return;
  let last = null;
  const check = () => {
    const t = document.title;
    if (t === last) return;
    last = t;
    try { window.` + previewTitleBinding + `(t); } catch (_) {}
  };
  new MutationObserver(check).observe(document, { subtree: true, childList: true, characterData: true });
  document.addEventListener("DOMContentLoaded", check);
  check();
})()`

// Viewport bounds (CSS pixels) and the picture's quality.
const (
	previewMinSide   = 200
	previewMaxWidth  = 3840
	previewMaxHeight = 2400
	previewMaxDPR    = 2
	previewQuality   = 70
	// previewInFlight: pictures sent and not yet drawn by the web. The browser makes the next one when one is
	// acknowledged, so a slow link gets fewer pictures, not a backlog.
	previewInFlight = 2
	// previewShotMaxHeight: a full-page screenshot stops here (CSS pixels).
	previewShotMaxHeight = 12000
)

// Binary messages to the web: a picture of the page or a screenshot.
const (
	previewFrameMsg byte = 'f' // seq (4), CSS width (2), CSS height (2), JPEG
	previewShotMsg  byte = 's' // id (4), PNG
)

// previewIn is a message from the web (docs/preview.md "Messages").
type previewIn struct {
	T string `json:"t"`
	// mouse: down | up | move; key: down | up
	Type    string  `json:"type,omitempty"`
	X       float64 `json:"x,omitempty"`
	Y       float64 `json:"y,omitempty"`
	Button  string  `json:"button,omitempty"`
	Buttons int     `json:"buttons,omitempty"`
	Clicks  int     `json:"clicks,omitempty"`
	// Mods: Alt 1, Ctrl 2, Meta 4, Shift 8 (CDP's)
	Mods     int     `json:"mods,omitempty"`
	DX       float64 `json:"dx,omitempty"`
	DY       float64 `json:"dy,omitempty"`
	Key      string  `json:"key,omitempty"`
	Code     string  `json:"code,omitempty"`
	KeyCode  int     `json:"key_code,omitempty"`
	Location int     `json:"location,omitempty"`
	Repeat   bool    `json:"repeat,omitempty"`
	Text     string  `json:"text,omitempty"`
	URL      string  `json:"url,omitempty"`
	W        int     `json:"w,omitempty"`
	H        int     `json:"h,omitempty"`
	DPR      float64 `json:"dpr,omitempty"`
	Mobile   bool    `json:"mobile,omitempty"`
	Seq      uint32  `json:"seq,omitempty"`
	ID       uint32  `json:"id,omitempty"`
	Full     bool    `json:"full,omitempty"`
	On       bool    `json:"on,omitempty"`
	Accept   bool    `json:"accept,omitempty"`
	Name     string  `json:"name,omitempty"`
}

type previewViewport struct {
	w, h   int
	dpr    float64
	mobile bool
}

func (v previewViewport) clamp() previewViewport {
	v.w = min(max(v.w, previewMinSide), previewMaxWidth)
	v.h = min(max(v.h, previewMinSide), previewMaxHeight)
	if v.dpr <= 0 || math.IsNaN(v.dpr) {
		v.dpr = 1
	}
	v.dpr = math.Min(math.Max(v.dpr, 1), previewMaxDPR)
	return v
}

// tabItem is one thing for the tab's goroutine: a CDP event, a message from the web, a result to apply, or the end.
type tabItem struct {
	method string
	params json.RawMessage
	in     *previewIn
	fn     func()
	gone   string // why the tab ends: the web left, the browser or the tab went away
}

type previewTab struct {
	n       *Node
	b       *previewBrowser
	target  string
	session string
	frame   string // the main frame's id
	conn    io.ReadWriteCloser
	ua      string // the browser's own user agent (a phone-sized preview changes it)

	wmu sync.Mutex // writes to conn

	qmu  sync.Mutex
	q    []tabItem
	wake chan struct{}

	vp       previewViewport
	seq      uint32
	unacked  int
	heldAck  []int // screencast frames not acknowledged to the browser yet
	url      string
	title    string
	loading  bool
	picking  bool
	lastPage string
}

func (t *previewTab) post(it tabItem) {
	t.qmu.Lock()
	t.q = append(t.q, it)
	t.qmu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *previewTab) take() []tabItem {
	t.qmu.Lock()
	q := t.q
	t.q = nil
	t.qmu.Unlock()
	return q
}

func (t *previewTab) write(kind byte, p []byte) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_ = preview.WriteFrame(t.conn, kind, p)
}

func (t *previewTab) send(v any) {
	b, err := json.Marshal(v)
	if err == nil {
		t.write(preview.Text, b)
	}
}

// cdp sends a command to the tab and does not wait for the answer (input, acknowledgements: in order, nothing to
// read back).
func (t *previewTab) cdp(method string, params any) {
	_ = t.b.cdp.sendNoReply(t.session, method, params)
}

// async runs a command whose answer matters on a goroutine of its own and hands the answer to then, on the tab's
// goroutine.
func (t *previewTab) async(method string, params any, then func(json.RawMessage, error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var out json.RawMessage
		err := t.b.cdp.call(ctx, t.session, method, params, &out)
		if then != nil {
			t.post(tabItem{fn: func() { then(out, err) }})
		}
	}()
}

// previewOpenRPC is preview.open: a tab showing a port of this computer, on the stream id.
func (n *Node) previewOpenRPC(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID     string  `json:"id"`
		Port   int     `json:"port"`
		Path   string  `json:"path"`
		W      int     `json:"w"`
		H      int     `json:"h"`
		DPR    float64 `json:"dpr"`
		Mobile bool    `json:"mobile"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Port <= 0 || p.Port > 65535 {
		return nil, errors.New("preview.open: need id and port")
	}
	if p.Path == "" || p.Path[0] != '/' {
		p.Path = "/" + p.Path
	}
	start, err := url.Parse("http://localhost:" + strconv.Itoa(p.Port) + p.Path)
	if err != nil || start.Host != "localhost:"+strconv.Itoa(p.Port) {
		return nil, fmt.Errorf("preview.open: bad path %q", p.Path)
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	c, err := dialPort(dctx, p.Port, "")
	cancel()
	if err != nil {
		return nil, &rpc.Error{Code: "port_gone", Message: fmt.Sprintf("nothing is listening on port %d", p.Port)}
	}
	c.Close()
	host := n.previewHost()
	b, err := host.acquire(ctx)
	if err != nil {
		if errors.Is(err, errNoBrowser) {
			return nil, &rpc.Error{Code: "no_browser", Message: err.Error()}
		}
		return nil, &rpc.Error{Code: "browser_failed", Message: err.Error()}
	}
	vp := previewViewport{w: p.W, h: p.H, dpr: p.DPR, mobile: p.Mobile}.clamp()
	t, err := b.newTab(ctx, n, vp)
	if err != nil {
		host.release()
		return nil, &rpc.Error{Code: "browser_failed", Message: err.Error()}
	}
	mine, theirs := net.Pipe()
	if err := n.streams.Open(p.ID); err != nil {
		mine.Close()
		theirs.Close()
		t.close()
		host.release()
		return nil, err
	}
	n.streams.Start(p.ID, theirs)
	t.conn = mine
	go func() {
		defer host.release()
		t.run(start.String())
	}()
	return map[string]any{"browser": b.name}, nil
}

// newTab opens a page in the browser and sets it up; it shows nothing until run navigates it.
func (b *previewBrowser) newTab(ctx context.Context, n *Node, vp previewViewport) (*previewTab, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := b.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		return nil, fmt.Errorf("could not open a tab: %w", err)
	}
	t := &previewTab{n: n, b: b, target: created.TargetID, wake: make(chan struct{}, 1), vp: vp}
	b.mu.Lock()
	b.targets[t.target] = t
	b.mu.Unlock()
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.cdp.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.target, "flatten": true}, &attached); err != nil {
		t.close()
		return nil, fmt.Errorf("could not attach to the tab: %w", err)
	}
	t.session = attached.SessionID
	b.mu.Lock()
	b.pages[t.session] = t
	b.mu.Unlock()
	var version struct {
		UserAgent string `json:"userAgent"`
	}
	_ = b.cdp.call(ctx, "", "Browser.getVersion", nil, &version)
	t.ua = strings.Replace(version.UserAgent, "HeadlessChrome", "Chrome", 1)
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	steps := []struct {
		method string
		params any
		out    any
	}{
		{"Page.enable", nil, nil},
		{"Inspector.enable", nil, nil},
		{"Page.getFrameTree", nil, &tree},
		{"Runtime.enable", nil, nil},
		{"Runtime.addBinding", map[string]any{"name": previewTitleBinding}, nil},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": previewTitleJS}, nil},
		{"Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}, nil},
		// The top frame's navigations are checked (top-level only; the page's own requests go as they would).
		{"Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}}}, nil},
	}
	for _, s := range steps {
		if err := b.cdp.call(ctx, t.session, s.method, s.params, s.out); err != nil {
			t.close()
			return nil, fmt.Errorf("could not set up the tab (%s): %w", s.method, err)
		}
	}
	t.frame = tree.FrameTree.Frame.ID
	t.applyViewport(false)
	return t, nil
}

// applyViewport sizes the page and (re)starts its pictures at that size.
func (t *previewTab) applyViewport(restart bool) {
	v := t.vp
	t.cdp("Emulation.setDeviceMetricsOverride", map[string]any{
		"width": v.w, "height": v.h, "deviceScaleFactor": v.dpr, "mobile": v.mobile, "screenWidth": v.w, "screenHeight": v.h,
	})
	t.cdp("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": v.mobile, "maxTouchPoints": 5})
	t.cdp("Emulation.setEmitTouchEventsForMouse", map[string]any{"enabled": v.mobile, "configuration": "mobile"})
	ua := t.ua
	if v.mobile && ua != "" {
		ua = mobileUA(ua)
	}
	if ua != "" {
		t.cdp("Emulation.setUserAgentOverride", map[string]any{"userAgent": ua})
	}
	if restart {
		t.cdp("Page.stopScreencast", nil)
		t.unacked, t.heldAck = 0, nil
	}
	t.cdp("Page.startScreencast", map[string]any{
		"format": "jpeg", "quality": previewQuality, "maxWidth": int(float64(v.w) * v.dpr), "maxHeight": int(float64(v.h) * v.dpr), "everyNthFrame": 1,
	})
}

// mobileUA is a desktop Chrome user agent as a phone's (what a dev server's "is this a phone" check looks for).
func mobileUA(ua string) string {
	i, j := strings.Index(ua, "("), strings.Index(ua, ")")
	if i < 0 || j < i {
		return ua
	}
	ua = ua[:i] + "(Linux; Android 10; K)" + ua[j+1:]
	if k := strings.Index(ua, " Safari/"); k >= 0 && !strings.Contains(ua, "Mobile") {
		ua = ua[:k] + " Mobile" + ua[k:]
	}
	return ua
}

// close closes the tab in the browser.
func (t *previewTab) close() {
	t.b.mu.Lock()
	delete(t.b.targets, t.target)
	delete(t.b.pages, t.session)
	t.b.mu.Unlock()
	if t.target != "" {
		_ = t.b.cdp.sendNoReply("", "Target.closeTarget", map[string]any{"targetId": t.target})
	}
}

// run serves the tab until the web leaves or the tab goes away.
func (t *previewTab) run(start string) {
	defer t.conn.Close()
	defer t.close()
	go t.readWeb()
	t.send(map[string]any{"t": "ready", "browser": t.b.name, "w": t.vp.w, "h": t.vp.h})
	t.navigate(start)
	for range t.wake {
		for _, it := range t.take() {
			switch {
			case it.gone != "":
				if it.gone != "web" {
					t.send(map[string]any{"t": "closed", "code": it.gone})
				}
				return
			case it.fn != nil:
				it.fn()
			case it.in != nil:
				t.fromWeb(it.in)
			default:
				if t.event(it.method, it.params) {
					return
				}
			}
		}
	}
}

func (t *previewTab) readWeb() {
	br := bufio.NewReader(t.conn)
	for {
		kind, p, err := preview.ReadFrame(br)
		if err != nil {
			t.post(tabItem{gone: "web"})
			return
		}
		if kind != preview.Text {
			continue
		}
		var in previewIn
		if json.Unmarshal(p, &in) == nil && in.T != "" {
			t.post(tabItem{in: &in})
		}
	}
}

// allowedTop says whether the top frame may go to u: this computer's loopback addresses over http(s), or nothing at
// all (about:blank).
func allowedTop(u string) bool {
	if u == "about:blank" {
		return true
	}
	pu, err := url.Parse(u)
	return err == nil && (pu.Scheme == "http" || pu.Scheme == "https") && loopbackHost(pu.Hostname())
}

// resolve is an address typed in the panel as a URL: a path on the page's server, a host:port without a scheme, or a
// whole URL.
func (t *previewTab) resolve(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "/") {
		if cur, err := url.Parse(t.url); err == nil && cur.Host != "" {
			return cur.Scheme + "://" + cur.Host + s
		}
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return s
}

func (t *previewTab) navigate(u string) {
	if !allowedTop(u) {
		t.send(map[string]any{"t": "external", "url": u})
		return
	}
	t.async("Page.navigate", map[string]any{"url": u}, func(raw json.RawMessage, err error) {
		var r struct {
			ErrorText string `json:"errorText"`
		}
		_ = json.Unmarshal(raw, &r)
		if err != nil {
			r.ErrorText = err.Error()
		}
		if r.ErrorText != "" && r.ErrorText != "net::ERR_ABORTED" {
			t.send(map[string]any{"t": "failed", "url": u, "error": r.ErrorText})
		}
	})
}

// fromWeb does what the user did in the panel.
func (t *previewTab) fromWeb(in *previewIn) {
	switch in.T {
	case "mouse":
		typ := map[string]string{"down": "mousePressed", "up": "mouseReleased", "move": "mouseMoved"}[in.Type]
		if typ == "" {
			return
		}
		button := in.Button
		if button == "" {
			button = "none"
		}
		t.cdp("Input.dispatchMouseEvent", map[string]any{
			"type": typ, "x": in.X, "y": in.Y, "button": button, "buttons": in.Buttons, "clickCount": in.Clicks, "modifiers": in.Mods,
		})
	case "wheel":
		t.cdp("Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": in.X, "y": in.Y, "deltaX": in.DX, "deltaY": in.DY, "modifiers": in.Mods})
	case "key":
		ev := map[string]any{
			"key": in.Key, "code": in.Code, "modifiers": in.Mods, "windowsVirtualKeyCode": in.KeyCode, "nativeVirtualKeyCode": in.KeyCode,
			"autoRepeat": in.Repeat, "location": in.Location, "isKeypad": in.Location == 3,
		}
		switch in.Type {
		case "down":
			ev["type"] = "rawKeyDown"
			if in.Text != "" {
				ev["type"], ev["text"], ev["unmodifiedText"] = "keyDown", in.Text, in.Text
			}
		case "up":
			ev["type"] = "keyUp"
		default:
			return
		}
		t.cdp("Input.dispatchKeyEvent", ev)
	case "text":
		if in.Text != "" {
			t.cdp("Input.insertText", map[string]any{"text": in.Text})
		}
	case "cmd":
		t.command(in.Name)
	case "ack":
		if t.unacked > 0 {
			t.unacked--
		}
		if len(t.heldAck) > 0 {
			t.cdp("Page.screencastFrameAck", map[string]any{"sessionId": t.heldAck[0]})
			t.heldAck = t.heldAck[1:]
		}
	case "nav":
		t.navigate(t.resolve(in.URL))
	case "reload":
		t.cdp("Page.reload", map[string]any{"ignoreCache": true})
	case "back", "forward":
		t.async("Page.getNavigationHistory", nil, func(raw json.RawMessage, err error) {
			var h previewHistory
			if err != nil || json.Unmarshal(raw, &h) != nil {
				return
			}
			i := h.CurrentIndex - 1
			if in.T == "forward" {
				i = h.CurrentIndex + 1
			}
			if i >= 0 && i < len(h.Entries) {
				t.cdp("Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID})
			}
		})
	case "size":
		vp := previewViewport{w: in.W, h: in.H, dpr: in.DPR, mobile: in.Mobile}.clamp()
		if vp != t.vp {
			ua := vp.mobile != t.vp.mobile
			t.vp = vp
			t.applyViewport(true)
			if ua { // the page asks who it is talking to as it loads
				t.cdp("Page.reload", nil)
			}
		}
	case "shot":
		t.screenshot(in.ID, in.Full)
	case "pick":
		t.pick(in.On)
	case "dialog":
		t.cdp("Page.handleJavaScriptDialog", map[string]any{"accept": in.Accept, "promptText": in.Text})
	}
}

// command is an editing command the panel's keyboard shortcut stands for (⌘A / Ctrl+A…): the page's own, since the
// browser here has no menu to take them from.
func (t *previewTab) command(name string) {
	switch name {
	case "selectAll", "undo", "redo", "delete":
		t.async("Runtime.evaluate", map[string]any{"expression": "document.execCommand(" + strconv.Quote(name) + ")"}, nil)
	case "copy", "cut":
		const sel = `(() => { const a = document.activeElement;
  if (a && (a.tagName === "INPUT" || a.tagName === "TEXTAREA") && a.selectionStart !== a.selectionEnd)
    return a.value.slice(a.selectionStart, a.selectionEnd);
  return String(getSelection()); })()`
		t.async("Runtime.evaluate", map[string]any{"expression": sel, "returnByValue": true}, func(raw json.RawMessage, err error) {
			var r struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			if err == nil && json.Unmarshal(raw, &r) == nil && r.Result.Value != "" {
				t.send(map[string]any{"t": "copied", "text": r.Result.Value})
				if name == "cut" {
					t.async("Runtime.evaluate", map[string]any{"expression": `document.execCommand("delete")`}, nil)
				}
			}
		})
	}
}

type previewHistory struct {
	CurrentIndex int `json:"currentIndex"`
	Entries      []struct {
		ID  int    `json:"id"`
		URL string `json:"url"`
	} `json:"entries"`
}

// screenshot sends a PNG of the page (what is in view, or the whole page up to a height) under id.
func (t *previewTab) screenshot(id uint32, full bool) {
	fail := func(err error) { t.send(map[string]any{"t": "shot_failed", "id": id, "error": err.Error()}) }
	capture := func(params map[string]any) {
		params["format"] = "png"
		t.async("Page.captureScreenshot", params, func(raw json.RawMessage, err error) {
			var r struct {
				Data string `json:"data"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &r)
			}
			var png []byte
			if err == nil {
				png, err = base64.StdEncoding.DecodeString(r.Data)
			}
			if err == nil && len(png)+5 >= preview.MaxFrame {
				err = errors.New("the screenshot is too big to send")
			}
			if err != nil {
				fail(err)
				return
			}
			msg := make([]byte, 5, 5+len(png))
			msg[0] = previewShotMsg
			binary.BigEndian.PutUint32(msg[1:], id)
			t.write(preview.Binary, append(msg, png...))
		})
	}
	if !full {
		capture(map[string]any{})
		return
	}
	t.async("Page.getLayoutMetrics", nil, func(raw json.RawMessage, err error) {
		var m struct {
			Content struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"cssContentSize"`
		}
		if err != nil || json.Unmarshal(raw, &m) != nil || m.Content.Height <= 0 {
			capture(map[string]any{})
			return
		}
		capture(map[string]any{"captureBeyondViewport": true, "clip": map[string]any{
			"x": 0, "y": 0, "width": math.Max(m.Content.Width, float64(t.vp.w)), "height": math.Min(m.Content.Height, previewShotMaxHeight), "scale": 1,
		}})
	})
}

// pick turns element picking on or off: while on, the element under the pointer is outlined and a click picks it
// (Overlay.inspectNodeRequested) instead of clicking it.
func (t *previewTab) pick(on bool) {
	if on == t.picking {
		return
	}
	t.picking = on
	if on {
		t.cdp("DOM.enable", nil)
		t.cdp("Overlay.enable", nil)
		t.cdp("Overlay.setInspectMode", map[string]any{"mode": "searchForNode", "highlightConfig": map[string]any{
			"showInfo": true, "showAccessibilityInfo": false,
			"contentColor": map[string]any{"r": 57, "g": 135, "b": 229, "a": 0.25},
			"borderColor":  map[string]any{"r": 57, "g": 135, "b": 229, "a": 0.9},
			"paddingColor": map[string]any{"r": 27, "g": 175, "b": 122, "a": 0.18},
			"marginColor":  map[string]any{"r": 235, "g": 104, "b": 52, "a": 0.15},
		}})
		return
	}
	t.cdp("Overlay.setInspectMode", map[string]any{"mode": "none", "highlightConfig": map[string]any{}})
	t.cdp("Overlay.hideHighlight", nil)
}

// picked describes the element the user picked and sends it to the web.
func (t *previewTab) picked(backendNode int) {
	t.pick(false)
	t.send(map[string]any{"t": "pick", "on": false})
	t.async("DOM.resolveNode", map[string]any{"backendNodeId": backendNode}, func(raw json.RawMessage, err error) {
		var r struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		if err != nil || json.Unmarshal(raw, &r) != nil || r.Object.ObjectID == "" {
			t.send(map[string]any{"t": "pick_failed"})
			return
		}
		t.async("Runtime.callFunctionOn", map[string]any{
			"objectId": r.Object.ObjectID, "functionDeclaration": previewPickJS, "returnByValue": true,
		}, func(raw json.RawMessage, err error) {
			var res struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
			}
			if err != nil || json.Unmarshal(raw, &res) != nil || len(res.Result.Value) == 0 || string(res.Result.Value) == "null" {
				t.send(map[string]any{"t": "pick_failed"})
				return
			}
			var el map[string]any
			if json.Unmarshal(res.Result.Value, &el) != nil {
				t.send(map[string]any{"t": "pick_failed"})
				return
			}
			el["t"], el["url"], el["title"] = "picked", t.url, t.title
			t.send(el)
		})
	})
}

// event handles a CDP event of the tab; true when the tab is gone.
func (t *previewTab) event(method string, params json.RawMessage) bool {
	switch method {
	case "Page.screencastFrame":
		var f struct {
			Data      string `json:"data"`
			SessionID int    `json:"sessionId"`
			Metadata  struct {
				DeviceWidth  float64 `json:"deviceWidth"`
				DeviceHeight float64 `json:"deviceHeight"`
			} `json:"metadata"`
		}
		if json.Unmarshal(params, &f) != nil {
			return false
		}
		jpeg, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			t.cdp("Page.screencastFrameAck", map[string]any{"sessionId": f.SessionID})
			return false
		}
		t.seq++
		msg := make([]byte, 9, 9+len(jpeg))
		msg[0] = previewFrameMsg
		binary.BigEndian.PutUint32(msg[1:], t.seq)
		binary.BigEndian.PutUint16(msg[5:], uint16(math.Round(f.Metadata.DeviceWidth)))
		binary.BigEndian.PutUint16(msg[7:], uint16(math.Round(f.Metadata.DeviceHeight)))
		t.write(preview.Binary, append(msg, jpeg...))
		t.unacked++
		if t.unacked < previewInFlight {
			t.cdp("Page.screencastFrameAck", map[string]any{"sessionId": f.SessionID})
		} else {
			t.heldAck = append(t.heldAck, f.SessionID)
		}
	case "Fetch.requestPaused":
		var r struct {
			RequestID    string `json:"requestId"`
			FrameID      string `json:"frameId"`
			ResourceType string `json:"resourceType"`
			Request      struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(params, &r) != nil {
			return false
		}
		if r.ResourceType == "Document" && (r.FrameID == t.frame || r.FrameID == "") && !allowedTop(r.Request.URL) {
			// A 204 answer leaves the page where it was (an error page would replace it); the web opens the link.
			t.cdp("Fetch.fulfillRequest", map[string]any{"requestId": r.RequestID, "responseCode": 204, "responseHeaders": []any{}})
			t.send(map[string]any{"t": "external", "url": r.Request.URL})
			return false
		}
		t.cdp("Fetch.continueRequest", map[string]any{"requestId": r.RequestID})
	case "Page.frameNavigated":
		var f struct {
			Frame struct {
				ID             string `json:"id"`
				ParentID       string `json:"parentId"`
				URL            string `json:"url"`
				URLFragment    string `json:"urlFragment"`
				UnreachableURL string `json:"unreachableUrl"`
			} `json:"frame"`
		}
		if json.Unmarshal(params, &f) == nil && f.Frame.ParentID == "" {
			t.frame = f.Frame.ID
			t.url = f.Frame.URL + f.Frame.URLFragment
			if f.Frame.UnreachableURL != "" {
				t.url = f.Frame.UnreachableURL
			}
			t.pushPage(true)
		}
	case "Page.navigatedWithinDocument":
		var f struct {
			FrameID string `json:"frameId"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(params, &f) == nil && f.FrameID == t.frame {
			t.url = f.URL
			t.pushPage(true)
		}
	case "Page.frameStartedLoading", "Page.frameStoppedLoading":
		var f struct {
			FrameID string `json:"frameId"`
		}
		if json.Unmarshal(params, &f) == nil && f.FrameID == t.frame {
			t.loading = method == "Page.frameStartedLoading"
			t.pushPage(!t.loading)
		}
	case "Runtime.bindingCalled":
		var c struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		if json.Unmarshal(params, &c) == nil && c.Name == previewTitleBinding && c.Payload != t.title {
			if r := []rune(c.Payload); len(r) > 300 { // the page can call the function with anything
				c.Payload = string(r[:300])
			}
			t.title = c.Payload
			t.pushPage(false)
		}
	case "Target.targetCreated": // a window the page opened: shown here when it is this computer's, else in a new tab
		var i struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				OpenerID string `json:"openerId"`
				Type     string `json:"type"`
			} `json:"targetInfo"`
		}
		if json.Unmarshal(params, &i) == nil && i.TargetInfo.OpenerID == t.target && i.TargetInfo.Type == "page" {
			_ = t.b.cdp.sendNoReply("", "Target.closeTarget", map[string]any{"targetId": i.TargetInfo.TargetID})
		}
	case "Page.windowOpen":
		var w struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(params, &w) == nil && w.URL != "" {
			t.navigate(w.URL)
		}
	case "Page.javascriptDialogOpening":
		var d struct {
			Message       string `json:"message"`
			Type          string `json:"type"`
			DefaultPrompt string `json:"defaultPrompt"`
		}
		if json.Unmarshal(params, &d) == nil {
			t.send(map[string]any{"t": "dialog", "kind": d.Type, "message": d.Message, "default": d.DefaultPrompt})
		}
	case "Page.javascriptDialogClosed":
		t.send(map[string]any{"t": "dialog_closed"})
	case "Page.fileChooserOpened":
		t.send(map[string]any{"t": "notice", "code": "file_chooser"})
	case "Overlay.inspectNodeRequested":
		var r struct {
			BackendNodeID int `json:"backendNodeId"`
		}
		if json.Unmarshal(params, &r) == nil && r.BackendNodeID != 0 {
			t.picked(r.BackendNodeID)
		}
	case "Inspector.targetCrashed":
		t.send(map[string]any{"t": "crashed"})
	case "Target.targetDestroyed", "Target.detachedFromTarget":
		t.send(map[string]any{"t": "closed", "code": "tab_closed"})
		return true
	}
	return false
}

// pushPage tells the web what the tab shows; with history, whether it can go back or forward too.
func (t *previewTab) pushPage(history bool) {
	send := func(back, forward bool) {
		msg := map[string]any{"t": "page", "url": t.url, "title": t.title, "loading": t.loading, "back": back, "forward": forward}
		b, _ := json.Marshal(msg)
		if string(b) == t.lastPage {
			return
		}
		t.lastPage = string(b)
		t.write(preview.Text, b)
	}
	var last struct {
		Back, Forward bool
	}
	_ = json.Unmarshal([]byte(t.lastPage), &last)
	if !history {
		send(last.Back, last.Forward)
		return
	}
	t.async("Page.getNavigationHistory", nil, func(raw json.RawMessage, err error) {
		var h previewHistory
		if err != nil || json.Unmarshal(raw, &h) != nil {
			// between two documents the page cannot say ("Not attached to an active page"): it is asked again
			// once the new one has loaded
			send(last.Back, last.Forward)
			return
		}
		// the about:blank the tab opened at is not somewhere to go back to
		first := 0
		if len(h.Entries) > 0 && h.Entries[0].URL == "about:blank" {
			first = 1
		}
		send(h.CurrentIndex > first, h.CurrentIndex < len(h.Entries)-1)
	})
}
