package hub

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestSettingsTextsInEachLanguage(t *testing.T) {
	st := FleetState{NeedsYou: 1, Working: 2, Unread: 3}
	for l, want := range map[i18n.Lang]string{
		i18n.ZhHans: "1 个需要你 · 2 个在工作 · 3 个做完了",
		i18n.En:     "1 needs you · 2 working · 3 done",
		i18n.Ja:     "1件が要対応 · 2件が作業中 · 3件が完了",
	} {
		if got := fleetSentence(l, st); got != want {
			t.Errorf("%s fleet: %q", l, got)
		}
	}
	if got := fleetSentence(i18n.En, FleetState{NeedsYou: 2}); got != "2 need you" {
		t.Errorf("en plural: %q", got)
	}
	// The live activity's start alert, built without a language, is the Chinese it was.
	aps := startPayload(st, time.Now(), false)["aps"].(map[string]any)
	if body := aps["alert"].(map[string]any)["body"]; body != "1 个需要你 · 2 个在工作 · 3 个做完了" {
		t.Errorf("default alert: %q", body)
	}

	if got := unknownDelivery(i18n.Ja, "hub-send.lost_during").Error(); got != "送信中にコンピューターとの接続が切れました：すでにコンピューターに届いている可能性があります。チャットを確認してから再送するか決めてください" {
		t.Errorf("send ja: %q", got)
	}
	// (The mail account's and the computer's cards, and a change's before → after line, are the manager's: extmgr's
	// TestMailAccountRequestInEnglish.)
}

func TestSelfUpdateWhyNotInEachLanguage(t *testing.T) {
	h := newTestHub(t)
	u := &updater{h: h}
	if err := u.whyNot(); err.Error() != "这个 hub 不能自己更新：开发版（"+h.version+"）" {
		t.Errorf("zh: %q", err)
	}
	if got := u.whyNotReason(i18n.En); got != "Development build ("+h.version+")" {
		t.Errorf("en: %q", got)
	}
	if got := i18n.Localize(u.whyNot(), i18n.Ja); got != "このハブは自動で更新できません：開発版（"+h.version+"）" {
		t.Errorf("ja: %q", got)
	}
}
