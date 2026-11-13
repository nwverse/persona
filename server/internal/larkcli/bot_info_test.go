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
