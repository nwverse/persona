package proto

import (
	"strings"
	"testing"
)

func TestValidModelID(t *testing.T) {
	for _, ok := range []string{
		"opus", "glm-5", "vendor/model-x", "opus[1m]", "claude-opus-4-6[1m]", "qwen3:32b", "claude-x@20250101",
		"gpt-5.1-codex", "meta-llama/Llama-3.1-8B-Instruct", "a_b", "7b",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-x-v1:0",
		strings.Repeat("a", MaxModelIDLen),
	} {
		if !ValidModelID(ok) {
			t.Errorf("ValidModelID(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"", " ", "opus ", " opus", "a b", "a\tb", "a\nb", "a\rb", "a\x00b", "a\x7fb",
		"-x", "--dangerously-skip-permissions", ".hidden", "/abs", "[1m]", "_x",
		"$(id)", "a$b", "a`id`", "a'b", `a"b`, `a\b`, "a;b", "a|b", "a&b", "a>b", "a<b", "a*", "a?", "~root",
		"a,b", "a=b", "a+b", "a#b", "a(b)", "a{b}", "a!b", "a%b", "模型", "é",
		strings.Repeat("a", MaxModelIDLen+1),
	} {
		if ValidModelID(bad) {
			t.Errorf("ValidModelID(%q) = true, want false", bad)
		}
	}
}
