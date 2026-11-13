package kb

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// meetingEchoFixtures are the cases the hub and the iOS app share (spec/fixtures/meeting-echo).
const meetingEchoFixtures = "../../../spec/fixtures/meeting-echo/cases.json"

// Every shared case: the hub's rule (isEcho) says what the phone's does (#5905).
func TestMeetingEchoFollowsTheSharedCases(t *testing.T) {
	raw, err := os.ReadFile(meetingEchoFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Version int `json:"version"`
		Cases   []struct {
			Name  string `json:"name"`
			Input struct {
				Line  RecordingSegment `json:"line"`
				Spans []SpokenSpan     `json:"spans"`
			} `json:"input"`
			Expected struct {
				Echo *bool `json:"echo"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if fixtures.Version != 1 || len(fixtures.Cases) < 20 {
		t.Fatalf("version %d, %d cases", fixtures.Version, len(fixtures.Cases))
	}
	for _, c := range fixtures.Cases {
		if c.Expected.Echo == nil || len(c.Input.Spans) == 0 {
			t.Fatalf("%s: no expected echo or no spans", c.Name)
		}
		l := c.Input.Line
		if got := isEcho(l.Start, l.End, l.Text, c.Input.Spans); got != *c.Expected.Echo {
			t.Errorf("%s: echo %v, want %v", c.Name, got, *c.Expected.Echo)
		}
		// The same through an upload: the phone marked nothing, the hub marks it by the spans.
		segs := []RecordingSegment{l}
		markEcho(segs, c.Input.Spans)
		if segs[0].Echo != *c.Expected.Echo {
			t.Errorf("%s: marked %v, want %v", c.Name, segs[0].Echo, *c.Expected.Echo)
		}
	}
}

// The normalization: NFKC, lower case, letters and decimal digits only.
func TestMeetingEchoNormal(t *testing.T) {
	for in, want := range map[string]string{
		"You kept the CAP — for this release!": "youkeptthecapforthisrelease",
		"这一版先保留４ＭＢ的上限。":                        "这一版先保留4mb的上限",
		"回帰ﾃｽﾄ、OK？":                            "回帰テストok",
		"Cafe\u0301\u0301 \u2460":              "café1", // composed, the extra mark goes; ① is 1 after NFKC
		"……！":                                  "",
	} {
		if got := string(echoNormal(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// spoken is read leniently: a malformed span or list is left out, never an error; text is cut at a character
// boundary.
func TestReadSpoken(t *testing.T) {
	long := strings.Repeat("答", maxSpokenText) // 3 bytes each
	for _, c := range []struct {
		name, raw string
		want      []SpokenSpan
	}{
		{"absent", ``, nil},
		{"null", `null`, nil},
		{"not a list", `"yes"`, nil},
		{"an object", `{"start":1,"end":2}`, nil},
		{"malformed spans left out", `[1, "x", {"start":"a","end":2}, {"start":1}, {"start":5,"end":4}, {"start":1,"end":2,"text":7},
			{"start":10,"end":17.5,"text":"You kept the cap."}, {"start":20,"end":21}]`,
			[]SpokenSpan{{Start: 10, End: 17.5, Text: "You kept the cap."}, {Start: 20, End: 21}}},
		{"text cut", `[{"start":0,"end":1,"text":"` + long + `"}]`, []SpokenSpan{{Start: 0, End: 1, Text: strings.Repeat("答", maxSpokenText/3)}}},
	} {
		got := readSpoken(json.RawMessage(c.raw))
		if len(got) != len(c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %+v, want %+v", c.name, got[i], c.want[i])
			}
		}
	}
	over := "[" + strings.TrimSuffix(strings.Repeat(`{"start":1,"end":2,"text":"a"},`, maxSpokenSpans+1), ",") + "]"
	if got := readSpoken(json.RawMessage(over)); got != nil {
		t.Errorf("a list over the limit: %d spans", len(got))
	}
}
