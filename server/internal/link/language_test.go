package link

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestCandidateSummaryCanonicalBytesAndPresentation(t *testing.T) {
	err := errors.Join(
		&attemptError{ep: Endpoint{URL: "https://192.0.2.10:18662", Kind: "lan"}, err: newTextError("link-errors.timeout", nil)},
		&attemptError{ep: Endpoint{URL: "https://hub.example.com:18662", Kind: "name"}, err: errors.New("peer 原文：keep")},
	)
	canonical := "内网 192.0.2.10:18662：超时；域名 hub.example.com:18662：peer 原文：keep"
	if got := summary(err); got != canonical {
		t.Fatalf("summary = %q, want %q", got, canonical)
	}
	for _, language := range []i18n.Lang{i18n.En, i18n.Ja, i18n.Lang("fr")} {
		t.Setenv("PERSONA_LANG", string(language))
		if got := summary(err); got != canonical {
			t.Fatalf("language %s changed canonical summary: %q", language, got)
		}
	}
	if got := summaryText(i18n.En, err); got != "LAN 192.0.2.10:18662: Timed out; Domain hub.example.com:18662: peer 原文：keep" {
		t.Fatalf("English summary = %q", got)
	}
	joined := ErrorText(i18n.En, err)
	if strings.Count(joined, "\n") != 1 || strings.Contains(joined, "；") {
		t.Fatalf("errors.Join presentation changed its newline separator: %q", joined)
	}
}

func TestStatusErrorTextRecognizesOnlyKnownShapes(t *testing.T) {
	for _, test := range []struct {
		name, canonical, english string
	}{
		{"candidate", "内网 192.0.2.10:18662：超时", "LAN 192.0.2.10:18662: Timed out"},
		{"listener", ":18663：端口被占用了", ":18663: The port is already in use"},
		{"node down", "本机的常驻 node 没在运行（/tmp/node.sock 连不上）：persona-link node install（systemd 用户服务）", "This computer's resident node isn't running (cannot connect to /tmp/node.sock): run persona-link node install (systemd user service)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := StatusErrorText(i18n.En, test.canonical); got != test.english {
				t.Fatalf("got %q, want %q", got, test.english)
			}
			if got := StatusErrorText(i18n.ZhHans, test.canonical); got != test.canonical {
				t.Fatalf("canonical Chinese changed: %q", got)
			}
		})
	}
	for _, text := range []string{
		"域名 b：peer 原文：keep",
		"peer：detail",
		"/tmp/link.json: peer：detail",
		"not-an-address：端口被占用了",
		":18663：an unrecognized operating-system error",
	} {
		if got := StatusErrorText(i18n.En, text); got != text {
			t.Errorf("unknown text %q changed to %q", text, got)
		}
	}
}

func TestNodeMissingCanonicalAndLocalizedViews(t *testing.T) {
	err := nodeMissing("linux", "/tmp/node.sock")
	canonical := "本机的常驻 node 没在运行（/tmp/node.sock 连不上）：persona-link node install（systemd 用户服务）"
	if err.Error() != canonical {
		t.Fatalf("Error = %q", err)
	}
	if got := ErrorText(i18n.En, err); got != "This computer's resident node isn't running (cannot connect to /tmp/node.sock): run persona-link node install (systemd user service)" {
		t.Fatalf("English = %q", got)
	}
	if got := ErrorText(i18n.Ja, err); got == canonical || !strings.Contains(got, "/tmp/node.sock") {
		t.Fatalf("Japanese = %q", got)
	}
	if !strings.Contains(canonical, NodeDownSelf+NodeDownMarker) {
		t.Fatal("the hub and manager's canonical node-down markers changed")
	}
}

func TestMaintainerLogAndTerminalErrorViews(t *testing.T) {
	err := newTextError("link-errors.heartbeat_timeout", errors.New("boom"))
	if got, want := err.Error(), "hub 没有回应心跳：boom"; got != want {
		t.Fatalf("canonical error = %q, want %q", got, want)
	}
	if got := ErrorText(i18n.En, err); got != "The hub didn't answer the heartbeat: boom" {
		t.Fatalf("maintainer log view = %q", got)
	}
	if got := ErrorText(i18n.Ja, err); got == err.Error() || !strings.HasSuffix(got, "boom") {
		t.Fatalf("terminal view = %q", got)
	}
}

func TestPassivePeerTextDoesNotFollowTerminalLanguage(t *testing.T) {
	t.Setenv("PERSONA_LANG", "ja")
	if got := defaultText("link-peer.wrong_credential"); got != "凭据不对：这台电脑只认配对它的那台 hub" {
		t.Fatalf("wrongCredential = %q", got)
	}
	if got := defaultText("link-peer.pair_json_required"); got != "要一个 JSON：{code, token, machine, hub_id, hub_name}" {
		t.Fatalf("pair JSON requirement = %q", got)
	}
}

func TestMalformedConfigKeepsCanonicalParserDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "link.json")
	if err := os.WriteFile(path, []byte(`{"machine":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil {
		t.Fatal("malformed JSON loaded")
	}
	canonical := err.Error()
	if !strings.HasPrefix(canonical, path+": ") {
		t.Fatalf("canonical error = %q", canonical)
	}
	if got := ErrorText(i18n.ZhHans, err); got != canonical {
		t.Fatalf("legacy Chinese presentation changed: %q", got)
	}
	detail := strings.TrimPrefix(canonical, path+": ")
	if got := ErrorText(i18n.En, err); got != "Cannot read "+path+": "+detail {
		t.Fatalf("English error = %q", got)
	}
	if got := ErrorText(i18n.Ja, err); !strings.HasSuffix(got, detail) || got == canonical {
		t.Fatalf("Japanese error = %q", got)
	}
}
