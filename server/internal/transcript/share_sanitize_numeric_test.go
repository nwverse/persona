package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShareSanitizerNumericCredentialsAndExplicitSettings(t *testing.T) {
	for _, input := range []string{"password=123456", "DB_PASSWORD=8675309", "PGPASSWORD=987654321", "token: 123456789012345678901234", "api_key=123456789"} {
		out, err := SanitizeShareText(input)
		if err != nil || !strings.Contains(out, "[REDACTED]") {
			t.Errorf("numeric credential retained: %q -> %q (%v)", input, out, err)
		}
	}
	for _, input := range []string{"max_tokens=4096", "max_output_tokens=8192", "use_token=true", "use_tokens=false"} {
		out, err := SanitizeShareText(input)
		if err != nil || out != input {
			t.Errorf("explicit setting changed: %q -> %q (%v)", input, out, err)
		}
	}
	out, err := SanitizeShareArguments(json.RawMessage(`{"password":123456,"env":{"DB_PASSWORD":8675309},"token":123456789012345678901234,"max_tokens":4096,"use_token":true,"password_enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["password"] != "[REDACTED]" || got["token"] != "[REDACTED]" || got["password_enabled"] != "[REDACTED]" || got["env"].(map[string]any)["DB_PASSWORD"] != "[REDACTED]" {
		t.Errorf("numeric/boolean credential field retained: %s", out)
	}
	if got["max_tokens"] != float64(4096) || got["use_token"] != true {
		t.Errorf("explicit setting removed: %s", out)
	}
}
