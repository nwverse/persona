// Hub Status is an optional, read-only provider for real-data watch acceptance.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

//go:embed persona-extension.json
var manifest []byte

//go:embed locales/*.json
var localeFiles embed.FS

const widgetID = "watch-hub-status-counts"
const pageID = "watch-hub-status-page"
const bodyLimit = 4 * 1024 * 1024

func main() { ext.Main(program()) }

func program() ext.Program {
	a := &app{client: &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
	return ext.Program{
		ID: "watch-hub-status", Version: "0.1.0", Manifest: manifest, Init: a.init,
		UI: map[string]ext.Page{widgetID: {Render: a.render}, pageID: {Render: a.render}},
	}
}

type app struct {
	client      *http.Client
	base, token string
}

func (a *app) init(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	// The Hub issues this process a scoped credential; no user credential or URL is packaged.
	u, err := url.Parse(in.Hub.URL)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		(u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" &&
		in.Hub.Token != "" && slices.Contains(in.Hub.Scopes, "agents.read") && slices.Contains(in.Hub.Scopes, "machines.read") {
		a.base, a.token = strings.TrimRight(in.Hub.URL, "/"), in.Hub.Token
	}
	return nil, nil
}

type counts struct{ Computers, Sessions, Pending int }

func (a *app) count(ctx context.Context, path, key string) (int, error) {
	if a.base == "" || a.token == "" {
		return 0, errors.New("hub_access_unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return 0, errors.New("invalid_hub_address")
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return 0, errors.New("hub_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, errors.New("hub_read_failed")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, bodyLimit+1))
	if err != nil || len(body) > bodyLimit {
		return 0, errors.New("invalid_hub_response")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return 0, errors.New("invalid_hub_response")
	}
	value := bytes.TrimSpace(object[key])
	// Older Go handlers may encode a present nil slice as null. A missing key is still an error.
	if bytes.Equal(value, []byte("null")) {
		return 0, nil
	}
	if len(value) == 0 || value[0] != '[' {
		return 0, errors.New("invalid_hub_response")
	}
	var rows []json.RawMessage
	if json.Unmarshal(value, &rows) != nil {
		return 0, errors.New("invalid_hub_response")
	}
	for _, row := range rows {
		trimmed := bytes.TrimSpace(row)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return 0, errors.New("invalid_hub_response")
		}
	}
	return len(rows), nil
}

func (a *app) read(ctx context.Context) (counts, error) {
	// Only these three fixed GET routes are reachable. Any failed read invalidates the whole view.
	var out counts
	for _, item := range []struct {
		path, key string
		value     *int
	}{
		{"/api/v1/machines", "machines", &out.Computers},
		{"/api/v1/sessions", "sessions", &out.Sessions},
		{"/api/v1/interactions", "interactions", &out.Pending},
	} {
		value, err := a.count(ctx, item.path, item.key)
		if err != nil {
			return counts{}, err
		}
		*item.value = value
	}
	return out, nil
}

type words struct{ Title, Computers, Sessions, Pending, Unavailable string }

func localized(locale string) words {
	name := "en"
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		name = "zh-Hans"
	}
	if strings.HasPrefix(strings.ToLower(locale), "ja") {
		name = "ja"
	}
	data, err := localeFiles.ReadFile("locales/" + name + ".json")
	if err != nil {
		panic("missing bundled locale")
	}
	var out words
	if json.Unmarshal(data, &out) != nil {
		panic("invalid bundled locale")
	}
	return out
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	w := localized(r.Locale)
	values, err := a.read(ctx)
	view := &wire.View{Title: w.Title}
	if err != nil {
		// Never show fabricated zeroes, private server errors, credentials or cached data on failure.
		view.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentText, Text: w.Unavailable}}}}
		return view, nil
	}
	metric := func(label string, value int) wire.Component {
		return wire.Component{Type: wire.ComponentMetric, Label: label, Value: fmt.Sprint(value)}
	}
	var items []wire.Component
	switch r.Size {
	case "accessory_circular":
		items = []wire.Component{metric(w.Pending, values.Pending)}
	case "accessory_rectangular":
		items = []wire.Component{{Type: wire.ComponentText, Text: fmt.Sprintf("%s %d · %s %d · %s %d", w.Computers, values.Computers, w.Sessions, values.Sessions, w.Pending, values.Pending)}}
	default:
		items = []wire.Component{metric(w.Computers, values.Computers), metric(w.Sessions, values.Sessions), metric(w.Pending, values.Pending)}
	}
	view.Sections = []wire.Section{{Items: items}}
	return view, nil
}
