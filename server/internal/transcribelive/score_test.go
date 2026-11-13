package main

import (
	"encoding/json"
	"math"
	"testing"
)

func TestCER(t *testing.T) {
	for _, c := range []struct {
		ref, hyp string
		want     float64
	}{
		{"那 export cap 这次还是保持在 4 MB 吗？", "那export cap这次还是保持在4MB吗", 0},
		{"Hello, world.", "hello world", 0},
		{"好的", "好", 0.5},
		{"", "", 0},
		{"", "多余", 1},
		{"Bo owns it.", "", 1},
	} {
		if got := cer(c.ref, c.hyp); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("cer(%q, %q) = %v, want %v", c.ref, c.hyp, got, c.want)
		}
	}
}

func TestLatinRecallSeesTranslation(t *testing.T) {
	refs := []string{"周三有点紧，regression test 至少要留两天。", "Let's keep the landing page on the web."}
	if got := latinRecall(refs, "周三有点紧，regression test 至少要留两天。Let's keep the landing page on the web."); got != 1 {
		t.Errorf("as said: %v, want 1", got)
	}
	// "regression test" translated into Chinese loses those words.
	if got := latinRecall(refs, "周三有点紧，回归测试至少要留两天。Let's keep the landing page on the web."); got >= 1 {
		t.Errorf("translated: %v, want below 1", got)
	}
}

func TestHanShare(t *testing.T) {
	if got := hanShare("决定：Bo 负责 dashboard"); got <= 0.1 || got >= 0.9 {
		t.Errorf("mixed: %v", got)
	}
	if hanShare("Decisions: Bo owns the dashboard.") != 0 || hanShare("决定保持不变") != 1 || hanShare("— 12 —") != 0 {
		t.Error("pure texts")
	}
}

func TestSummary(t *testing.T) {
	s := summary([]float64{3, 1, 2, 10})
	if s["n"] != 4 || s["min"] != 1 || s["max"] != 10 || s["p50"] != 3 || s["p90"] != 10 {
		t.Errorf("summary = %v", s)
	}
	if summary(nil) != nil {
		t.Error("summary of nothing")
	}
}

// The embedded meeting is a third Chinese, a third English and a third both in one sentence, with one line marked as
// Persona's own answer (its echo).
func TestEmbeddedMeeting(t *testing.T) {
	var m meeting
	if err := json.Unmarshal(meetingJSON, &m); err != nil {
		t.Fatal(err)
	}
	langs, echo := map[string]int{}, 0
	for _, l := range m.Lines {
		langs[l.Lang]++
		if l.Echo {
			echo++
		}
		if l.Text == "" || l.Voice == "" {
			t.Errorf("line without text or voice: %+v", l)
		}
	}
	if langs["zh"] < 5 || langs["en"] < 5 || langs["mixed"] < 5 || echo != 1 || m.Title == "" || len(m.Glossary) == 0 {
		t.Errorf("meeting: %v languages, %d echo lines, title %q, glossary %v", langs, echo, m.Title, m.Glossary)
	}
}
