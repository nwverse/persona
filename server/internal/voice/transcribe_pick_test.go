package voice

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/voice/voicetest"
)

func TestNormDistance(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want float64
	}{
		{"", "", 0},
		{"", "abc", 1},
		{"abcd", "abed", 0.25},
		{"Ｈｅｌｌｏ，世界！", "hello 世界", 0},
		{"我们先把 export cap 保持在 4 MB。", "我们先把export cap保持在4MB", 0},
		{"Bo owns the vendor follow-up.", "bo owns the vendor followup", 0},
		{"我们先把 export cap 保持在 4 MB。", "我们先把出口上限保持在四兆。", 0.63},
	} {
		if got := normDistance(tt.a, tt.b); math.Abs(got-tt.want) > 0.01 {
			t.Errorf("%q %q: %.3f, want %.2f", tt.a, tt.b, got, tt.want)
		}
	}
	// What a rogue session writes is far from what was said, and two rogue sessions do not agree.
	for i, r := range voicetest.InventedReplies {
		if d := normDistance(r, "Bo owns the vendor follow-up."); d <= trDropOver {
			t.Errorf("invented reply %d is %.2f from a sentence", i, d)
		}
		for k, o := range voicetest.InventedReplies[:i] {
			if d := normDistance(r, o); d <= trDropOver {
				t.Errorf("invented replies %d and %d are %.2f apart", i, k, d)
			}
		}
	}
}

func TestMedoid(t *testing.T) {
	texts := []string{"abcdefghij", "abcdefghix", "zzzzzzzzzz", "abcdefghiy"}
	if got := medoid(texts, []int{0, 1, 2, 3}, nil); got != 0 {
		t.Errorf("medoid %d", got)
	}
	// Of two, the one closer to the anchor.
	anchor := "abcdefghiy"
	if got := medoid(texts, []int{1, 3}, &anchor); got != 3 {
		t.Errorf("of two %d", got)
	}
	if got := medoid(texts, []int{1, 3}, nil); got != 1 {
		t.Errorf("of two without an anchor %d", got)
	}
}

func TestPickLive(t *testing.T) {
	said := "我们这周先把 export cap 保持在 4 MB。"
	caption := "我们这周先把 export 开 保持在 4 MB"
	rogue := voicetest.InventedReplies[0]
	for _, tt := range []struct {
		name    string
		caption string
		texts   []string
		pick    int
		dropped []bool
	}{
		{"one rogue", caption, []string{rogue, said}, 1, []bool{true, false}},
		{"healthy pair", caption, []string{said, said + "嗯"}, 0, []bool{false, false}},
		{"none near the captions", caption, []string{rogue, voicetest.InventedReplies[1]}, -1, []bool{true, true}},
		{"no captions, three", "", []string{said, rogue, said}, 0, []bool{false, true, false}},
		{"no captions, two apart", "", []string{said, rogue}, -1, []bool{false, false}},
		{"no captions, alone", "", []string{said}, 0, []bool{false}},
		{"no captions, none agree", "", []string{said, rogue, voicetest.InventedReplies[1]}, -1, []bool{true, true, true}},
		{"nothing", caption, nil, -1, []bool{}},
	} {
		pick, dropped := pickLive(tt.caption, tt.texts)
		if pick != tt.pick || !reflect.DeepEqual(dropped, tt.dropped) {
			t.Errorf("%s: %d %v, want %d %v", tt.name, pick, dropped, tt.pick, tt.dropped)
		}
	}
}

func TestPickReplay(t *testing.T) {
	said := "下周三之前 Alice 要把 pricing 的 proposal 发出来。"
	near := "下周三之前 Alice 要把 pricing 的 proposal 发出"
	rogue := voicetest.InventedReplies[0]
	ref := "下周三之前 alice 要把 pricing 的 proposol 发出来。"
	for _, tt := range []struct {
		name    string
		ref     *string
		texts   []string
		stopped []bool
		pick    int
		kept    bool
	}{
		{"with a reference", &ref, []string{rogue, said}, []bool{false, false}, 1, false},
		{"of two, the closer to the reference", &ref, []string{near, said}, []bool{false, false}, 1, false},
		{"none near: the reference stays", &ref, []string{rogue, voicetest.InventedReplies[1]}, []bool{false, false}, -1, true},
		{"stopped early", &ref, []string{said, near}, []bool{true, false}, 1, false},
		{"without: two of three", nil, []string{said, rogue, near}, []bool{false, false, false}, 0, false},
		{"without: none agree", nil, []string{said, rogue, voicetest.InventedReplies[1]}, []bool{false, false, false}, -1, true},
		{"without: one left", nil, []string{said, near, rogue}, []bool{true, false, false}, -1, true},
		{"an empty reference is none", new(""), []string{said, rogue, near}, []bool{false, false, false}, 0, false},
	} {
		pick, kept := pickReplay(tt.ref, tt.texts, tt.stopped)
		if pick != tt.pick || kept != tt.kept {
			t.Errorf("%s: %d %v, want %d %v", tt.name, pick, kept, tt.pick, tt.kept)
		}
	}
}

func TestSplitSentences(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"我们先这样。Then we ship it.", []string{"我们先这样。", "Then we ship it."}},
		{"The cap is 4.5 MB. OK", []string{"The cap is 4.5 MB.", "OK"}},
		{"他说：“好的。”然后走了", []string{"他说：“好的。”", "然后走了"}},
		{"Really?! Yes", []string{"Really?!", "Yes"}},
		{"  下周三之前 Alice 要把 pricing 的 proposal 发出来。  ", []string{"下周三之前 Alice 要把 pricing 的 proposal 发出来。"}},
		{"。", []string{"。"}},
		{"", nil},
	} {
		if got := splitSentences(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSpreadSentences(t *testing.T) {
	speech := [][2]float64{{6.4, 9.6}, {10.6, 12.4}, {13.2, 16.0}}
	pieces := []string{"下周三之前 Alice 要把 pricing 的 proposal 发出来。", "Let's revisit pricing next week.",
		"这个 release 先只发 iOS，Android 下个 sprint 再跟。"}
	got := spreadSentences(pieces, speech, 6.0, 16.5)
	want := [][2]float64{{6.4, 9.6}, {10.6, 12.4}, {13.2, 16.0}}
	for i, s := range got {
		if math.Abs(s.start-want[i][0]) > 0.01 || math.Abs(s.end-want[i][1]) > 0.01 || s.text != pieces[i] {
			t.Errorf("sentence %d: %+v, want %v", i, s, want[i])
		}
	}
	// Two sentences in one stretch of speech share it by length; none: the window.
	got = spreadSentences([]string{"短。", "This sentence is a good deal longer than the first one."}, [][2]float64{{1, 5}}, 0, 6)
	if got[0].start != 1 || got[0].end >= 2 || got[1].start != got[0].end || got[1].end != 5 {
		t.Errorf("one stretch: %+v", got)
	}
	got = spreadSentences([]string{"Hello there."}, nil, 2, 4)
	if len(got) != 1 || got[0].start != 2 || got[0].end != 4 {
		t.Errorf("no speech: %+v", got)
	}
	if spreadSentences(nil, speech, 0, 1) != nil {
		t.Error("no sentences")
	}
	if w := trWeight("Bo owns it"); math.Abs(w-(1.0+1.5+1.0)) > 0.01 || trWeight(strings.Repeat("字", 4)) != 4 {
		t.Errorf("weights %.2f", w)
	}
}
