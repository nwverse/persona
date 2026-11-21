package larkcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBotOpenIDUsesBoundBotIdentity(t *testing.T) {
	log := filepath.Join(t.TempDir(), "cli.log")
	t.Setenv("LARKCLI_FAKE_LOG", log)
	id, err := New(fakeConfig(t, "success")).BotOpenID(context.Background())
	if err != nil || id != "ou_example_persona_bot" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "api GET /open-apis/bot/v3/info --as bot") {
		t.Fatalf("bot identity not explicit: %s", raw)
	}
}

func TestBotOpenIDResponseShapes(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"flattened CLI", `{"ok":true,"data":{"open_id":"ou_example_persona_bot"}}`, "ou_example_persona_bot"},
		{"nested CLI", `{"ok":true,"data":{"bot":{"open_id":"ou_example_persona_bot"}}}`, "ou_example_persona_bot"},
		{"raw bot object", `{"code":0,"msg":"ok","bot":{"open_id":"ou_example_persona_bot"}}`, "ou_example_persona_bot"},
		{"bot identity takes precedence", `{"data":{"open_id":"ou_example_other","bot":{"open_id":"ou_example_persona_bot"}}}`, "ou_example_persona_bot"},
		{"missing identity", `{"data":{"bot":{}}}`, ""},
		{"empty nested identity ignores unrelated flat identity", `{"data":{"open_id":"ou_example_other","bot":{}}}`, ""},
		{"invalid nested identity ignores unrelated flat identity", `{"data":{"open_id":"ou_example_other","bot":{"open_id":"cli_example_invalid"}}}`, ""},
		{"invalid flat identity", `{"data":{"open_id":"cli_example_invalid"}}`, ""},
		{"empty flat identity", `{"data":{"open_id":""}}`, ""},
		{"empty flat suffix", `{"data":{"open_id":"ou_"}}`, ""},
		{"empty nested suffix", `{"data":{"bot":{"open_id":"ou_"}}}`, ""},
		{"empty raw bot suffix", `{"code":0,"bot":{"open_id":"ou_"}}`, ""},
		{"nested empty suffix ignores flat identity", `{"data":{"open_id":"ou_example_other","bot":{"open_id":"ou_"}}}`, ""},
		{"leading whitespace", `{"data":{"open_id":" ou_example_bot"}}`, ""},
		{"trailing whitespace", `{"data":{"open_id":"ou_example_bot "}}`, ""},
		{"embedded whitespace", `{"data":{"open_id":"ou_example bot"}}`, ""},
		{"newline suffix", `{"data":{"open_id":"ou_example_bot\n"}}`, ""},
		{"control character", `{"data":{"open_id":"ou_example\u0000bot"}}`, ""},
		{"invalid separator", `{"data":{"open_id":"ou_example/bot"}}`, ""},
		{"opaque ID characters", `{"data":{"open_id":"ou_Example-bot_123"}}`, "ou_Example-bot_123"},
		{"invalid bot shape", `{"data":{"open_id":"ou_example_other","bot":"invalid"}}`, ""},
		{"malformed JSON", `{"data":`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(fakeConfig(t, "success"))
			t.Setenv("LARKCLI_FAKE_BOT_INFO", tc.raw)
			id, err := r.BotOpenID(context.Background())
			if id != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("id=%q err=%v, want %q", id, err, tc.want)
			}
		})
	}
}
