package voice

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// trCaptionJob is a live job with one open window and its caption session, to give caption frames to caption() as
// the session's reader does. Nothing reaches a manager: the job has not sent ready.
func trCaptionJob() (*trJob, *trSession) {
	j := &trJob{windows: []*trWin{{n: 0, from: 0, to: -1}}, nextWin: 1}
	sl := &trSlot{kind: trCaption}
	s := &trSession{j: j, kind: trCaption, slot: sl, last: -1, passed: map[int]time.Time{}}
	sl.cur = s
	j.slots = []*trSlot{sl}
	return j, s
}

// trAt is recording second at in samples.
func trAt(at float64) int64 { return int64(math.Round(at * trRate)) }

// trFrameAt is one caption frame and where the session's audio was when it came, in recording seconds.
type trFrameAt struct {
	at    float64
	item  string
	text  string
	final bool
}

// feedCaptions gives frames to caption session s of j in order and returns its captions' texts.
func feedCaptions(j *trJob, s *trSession, frames []trFrameAt) []string {
	for _, f := range frames {
		j.caption(s, tevent{kind: tevCaption, item: f.item, text: f.text, final: f.final}, trAt(f.at), trAt(f.at))
	}
	var out []string
	for _, it := range s.items {
		out = append(out, it.text)
	}
	return out
}

// The measured caption session's frames (one item, never final): the service revised a caption's first words at the
// moment it began ("好人", then "how 人" at once), and the next sentence's text replaced the last one's. The revised
// hypothesis is part of its caption, not a caption of its own: the window's captions are what was said, once.
func TestCaptionRevisedAsMeasured(t *testing.T) {
	const item = "029f54e8"
	frames := []trFrameAt{
		{2.50, item, "好人", false},
		{2.50, item, "how 人", false},
		{2.66, item, "how 人都", false},
		{5.37, item, "how 人都到齐了我们开始今天的发布评审", false},
		{9.60, item, "先说 ios 这边离线编辑基本做完了", false},
		{14.09, item, "the back end merge a", false},
		{17.49, item, "the back end merge a p an now returns the conflicting paragraphs so the client can", false},
		{21.18, item, "help 这词还是", false},
		{59.37, item, "andrea 那边", false},
		{61.89, item, "android 那边的 built 昨天挂了是 graded cache 的", false},
		{65.46, item, "i already filed a fix it should land before", false},
	}
	want := []string{
		"how 人都到齐了我们开始今天的发布评审",
		"先说 ios 这边离线编辑基本做完了",
		"the back end merge a p an now returns the conflicting paragraphs so the client can",
		"help 这词还是",
		"android 那边的 built 昨天挂了是 graded cache 的",
		"i already filed a fix it should land before",
	}
	j, s := trCaptionJob()
	if got := feedCaptions(j, s, frames); !reflect.DeepEqual(got, want) {
		t.Fatalf("captions:\n%q\nwant:\n%q", got, want)
	}
	if got := j.captionText(s, j.windows[0]); got != strings.Join(want, "") {
		t.Errorf("the window's captions %q", got)
	}
	// The revised caption keeps the start its first frame gave it.
	if s.items[0].start != trAt(2.50-trCaptionLag.Seconds()) {
		t.Errorf("the first caption starts at %.2f s", trSeconds(s.items[0].start))
	}
}
