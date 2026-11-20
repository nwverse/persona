// Package hubclient shares the paired-device transport for terminal commands.
package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/clitext"
	"github.com/nwverse/persona/server/internal/i18n"
)

var (
	ErrTransport = errors.New("hub request failed")
	ErrResponse  = errors.New("invalid hub response")
)

type HTTPError struct {
	Status int
	Code   string
	Detail string
	Named  bool // the hub returned a structured error
}

func (e *HTTPError) Error() string { return e.Detail }

// ValidID checks a client-supplied identifier before it becomes an escaped path segment.
func ValidID(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsFunc(s, unicode.IsControl)
}

// BaseURL accepts a mounted hub without letting a token travel over public HTTP.
func BaseURL(raw string) (*url.URL, error) {
	addr, err := url.Parse(raw)
	if err != nil || addr.Hostname() == "" || addr.User != nil || addr.RawQuery != "" ||
		addr.ForceQuery || addr.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("invalid hub URL")
	}
	switch addr.Scheme {
	case "https":
		return addr, nil
	case "http":
		host := addr.Hostname()
		if strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback() {
			return addr, nil
		}
	}
	return nil, errors.New("invalid hub scheme or host")
}

// Token reads a bounded token from the explicit file, or from env when supplied.
func Token(path, env string) (string, error) {
	const maxTokenBytes = 4096
	var raw string
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxTokenBytes+1))
		if err != nil {
			return "", err
		}
		raw = string(data)
	} else if env != "" {
		raw = os.Getenv(env)
	}
	token := strings.TrimSpace(raw)
	if len(raw) > maxTokenBytes || token == "" ||
		strings.ContainsFunc(token, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return "", errors.New("invalid token")
	}
	return token, nil
}

type Client struct {
	base     *url.URL
	token    string
	lang     i18n.Lang
	http     *http.Client
	features string
}

func New(base *url.URL, token string, lang i18n.Lang, timeout time.Duration, features ...string) *Client {
	return &Client{base: base, token: token, lang: lang, features: strings.Join(features, ","), http: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// endpoint keeps any hub mount prefix and existing escapes.
func (c *Client) endpoint(path string, query url.Values) string {
	base := *c.base
	base.RawPath = strings.TrimRight(base.EscapedPath(), "/") + path
	base.Path, _ = url.PathUnescape(base.RawPath)
	base.RawQuery = query.Encode()
	return base.String()
}

func (c *Client) headers() http.Header {
	header := http.Header{
		"Authorization":   {"Bearer " + c.token},
		"Accept-Language": {string(c.lang)},
	}
	if c.features != "" {
		header.Set("X-Persona-Features", c.features)
	}
	return header
}

// JSON makes one request and decodes one bounded response. It reports the bytes read for callers with a total budget.
func (c *Client) JSON(ctx context.Context, method, path string, query url.Values, limit int64, out any) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query), nil)
	if err != nil {
		return 0, ErrTransport
	}
	req.Header = c.headers()
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, ErrTransport
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	size := int64(len(body))
	if err != nil || size > limit {
		return size, ErrResponse
	}
	if resp.StatusCode != http.StatusOK {
		return size, c.httpError(resp, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return size, ErrResponse
	}
	return size, nil
}

// Hello reads only the initial authenticated WebSocket hello. Public HTTP health
// responses intentionally omit the capabilities required to select a REST contract.
func (c *Client) Hello(ctx context.Context, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.http.Timeout)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, c.endpoint("/api/v1/ws", nil), &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: c.headers(),
	})
	if err != nil {
		if resp != nil {
			return c.httpError(resp, nil)
		}
		return ErrTransport
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	typ, body, err := conn.Read(ctx)
	if err != nil {
		return ErrTransport
	}
	if typ != websocket.MessageText || json.Unmarshal(body, out) != nil {
		return ErrResponse
	}
	return nil
}

func (c *Client) httpError(resp *http.Response, body []byte) error {
	var wire struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &wire)
	detail := apierr.Describe(body)
	named := detail != ""
	if !named {
		detail = resp.Status
	}
	return &HTTPError{
		Status: resp.StatusCode,
		Code:   wire.Code,
		Detail: clitext.Inert(strings.ReplaceAll(detail, c.token, "[redacted]")),
		Named:  named,
	}
}
