package kb

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The echo rule: a sentence at least half inside the speaking spans, widened by half a second on each side, is
// dropped; the spans count together; a sentence of no length by its moment.
func TestEchoOf(t *testing.T) {
	spans := EchoSpans(Timeline{Speaking: [][2]float64{{10, 20}, {30, 31}, {31.5, 32}}})
	if len(spans) != 3 || spans[0] != (EchoSpan{From: 10, To: 20}) {
		t.Fatalf("spans %+v", spans)
	}
	for _, c := range []struct {
		name       string
		start, end float64
		want       EchoVerdict
	}{
		{"inside", 12, 18, EchoDrop},
		{"exactly half inside the widened span", 15.5, 25.5, EchoDrop},
		{"just under half", 15.6, 25.6, EchoKeep},
		{"before, touching the margin", 5, 9.5, EchoKeep},
		{"within the margin before", 9.6, 10.2, EchoDrop},
		{"after, in the margin", 20.1, 20.4, EchoDrop},
		{"after the margin", 20.6, 25, EchoKeep},
		{"two spans together", 29.4, 32.6, EchoDrop},
		{"a moment inside", 15, 15, EchoDrop},
		{"a moment outside", 25, 25, EchoKeep},
		{"backwards, inside", 18, 12, EchoDrop},
	} {
		if got := EchoOf(EchoSentence{Start: c.start, End: c.end, Text: "好的 OK"}, spans); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	if got := EchoOf(EchoSentence{Start: 12, End: 18}, nil); got != EchoKeep {
		t.Errorf("no spans: %s", got)
	}
	// Held back: a sentence that ends after an open span began, less the margin.
	for _, c := range []struct {
		end  float64
		want bool
	}{{99.4, false}, {99.5, false}, {99.6, true}, {120, true}} {
		if got := EchoHeld(EchoSentence{Start: 90, End: c.end}, 100); got != c.want {
			t.Errorf("held ending at %v: %v", c.end, got)
		}
	}
}

// The echo rule stays one function (EchoOf, EchoHeld in echo.go): no other file of the knowledge base or the assistant
// widens spans by the margin or reads the timeline's speaking spans itself, so a refinement of the rule changes echo.go
// and no caller.
func TestEchoRuleIsOneFunction(t *testing.T) {
	allowed := map[string]bool{"echo.go": true, "transcription.go": true} // transcription.go keeps the timeline's union
	speaking := regexp.MustCompile(`\.Speaking\b|\bEchoMargin\b`)
	for _, dir := range []string{".", filepath.Join("..", "assistant")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files in %s", dir)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") || dir == "." && allowed[filepath.Base(f)] {
				continue
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if code, _, _ := strings.Cut(line, "//"); speaking.MatchString(code) {
					t.Errorf("%s:%d judges sentences against the speaking spans itself (use kb.EchoOf, kb.EchoHeld, kb.EchoSpans): %s",
						f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
