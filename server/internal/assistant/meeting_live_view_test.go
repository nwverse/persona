package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// The live view (#3862 phase 2, docs/assistant/meeting-minutes.md "Phase 2: live view"): lines and topics on
// GET …/live, the room's meeting_topics_set, lines and topics in meeting_live. All names and texts are placeholders.

// topicsCall is the room session's meeting_topics_set with args, and its text.
func (r *roomFixture) topicsCall(session, args string) (proto.ToolResult, string) {
	r.t.Helper()
	d := newFleetDevs() // the nodes' requests are the fleet's; only for the call, so the feeder keeps the fake hub's
	d.add("server", "Server")
	r.s.devs = d
	defer func() { r.s.devs = nil }()
	out, ok, err := r.s.NodeRequest(context.Background(), "server", "mcp.call",
		json.RawMessage(`{"name":"meeting_topics_set","args":`+args+`,"session_id":"`+session+`","assistant":true,"guest":true}`))
	if !ok || err != nil {
		r.t.Fatalf("call: %v %v", ok, err)
	}
	res := out.(proto.ToolResult)
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	return res, b.String()
}

// GET …/live?from=: the live log's positions from n (a placeholder's text "" included), at most 500 with more when
// there are more; none without from; a from that is not a whole number of 0 or more is 400, after 404. The lines'
// revision is the live log's length; ?parts=lines gives the lines only beside from. POST …/live's 200 has neither
// lines nor topics.
func TestMeetingLiveLines(t *testing.T) {
	r := newRoomFixture(t)
	r.do("GET", livePath(meetingRec)+"?from=x", "", nil, http.StatusNotFound)
	r.roomUp(meetingRec, "listener")
	segs := make([]map[string]any, 0, 200)
	for i := range 200 {
		text := fmt.Sprintf("line %d", i)
		if i == 1 {
			text = ""
		}
		segs = append(segs, map[string]any{"start": i, "end": i + 1, "text": text})
	}
	for from := 0; from < 600; from += 200 {
		for i := range segs {
			segs[i]["text"] = fmt.Sprintf("line %d", from+i)
			if from+i == 1 {
				segs[i]["text"] = ""
			}
		}
		out := r.do("POST", livePath(meetingRec), "", map[string]any{"from": from, "segments": segs}, http.StatusOK)
		if _, has := out["lines"]; has || out["topics"] != nil {
			t.Fatalf("POST …/live: %v", out)
		}
	}

	view := r.do("GET", livePath(meetingRec), "", nil, http.StatusOK)
	if _, has := view["lines"]; has || view["revs"].(map[string]any)["lines"] != float64(600) {
		t.Fatalf("without from: %v", view)
	}
	page := r.do("GET", livePath(meetingRec)+"?from=0", "", nil, http.StatusOK)
	lines, _ := page["lines"].([]any)
	if len(lines) != maxLiveLines || page["more"] != true {
		t.Fatalf("first page: %d lines, more %v", len(lines), page["more"])
	}
	first, second := lines[0].(map[string]any), lines[1].(map[string]any)
	if first["n"] != float64(0) || first["text"] != "line 0" || first["start"] != float64(0) || first["end"] != float64(1) ||
		second["n"] != float64(1) || second["text"] != "" {
		t.Fatalf("lines %v %v", first, second)
	}
	last := lines[len(lines)-1].(map[string]any)["n"].(float64)
	rest := r.do("GET", livePath(meetingRec)+fmt.Sprintf("?from=%d", int(last)+1), "", nil, http.StatusOK)
	if lines, _ := rest["lines"].([]any); len(lines) != 100 || rest["more"] != nil || lines[0].(map[string]any)["n"] != float64(500) {
		t.Fatalf("second page: %v", rest["more"])
	}
	if end := r.do("GET", livePath(meetingRec)+"?from=900", "", nil, http.StatusOK); len(end["lines"].([]any)) != 0 || end["more"] != nil {
		t.Fatalf("past the end: %v", end)
	}
	for _, bad := range []string{"-1", "1.5", "abc", "", "+3", "99999999999999999999"} {
		wantCode(t, r.do("GET", livePath(meetingRec)+"?from="+bad, "", nil, http.StatusBadRequest), "bad_request")
	}

	only := r.do("GET", livePath(meetingRec)+"?parts=lines", "", nil, http.StatusOK)
	if raw, _ := json.Marshal(only); string(raw) != `{"revs":{"lines":600}}` {
		t.Fatalf("parts=lines without from: %s", raw)
	}
	only = r.do("GET", livePath(meetingRec)+"?parts=lines&from=599", "", nil, http.StatusOK)
	if raw, _ := json.Marshal(only); string(raw) != `{"lines":[{"end":200,"n":599,"start":199,"text":"line 599"}],"revs":{"lines":600}}` {
		t.Fatalf("parts=lines from 599: %s", raw)
	}
	waitFor(t, "a meeting_live event for the lines", func() bool {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		for _, ev := range r.f.liveEvents {
			changed, _ := ev["changed"].([]any)
			if slices.Contains(changed, any("lines")) && ev["revs"].(map[string]any)["lines"] == float64(600) {
				return true
			}
		}
		return false
	})
	d, err := r.engine().Get(kb.KindRecording, meetingRec, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(d.Meta), "line 1") || strings.Contains(string(d.Meta), "topics") || strings.Contains(string(d.Meta), "lines") {
		t.Fatalf("detail.meta %s", d.Meta)
	}
}

// meeting_topics_set: the room replaces its meeting's topics whole; GET …/live has them ([] before the first) with
// topics_at and their revision, which goes up only when they change; a list over a limit is refused naming it, and
// changes nothing; meeting_live says topics changed; the next batch shows the room the topics so far, and the
// follow-up's seed lists them. A room started before the tool existed sets none and is shown none.
func TestMeetingTopics(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "participant")
	view := r.do("GET", livePath(meetingRec), "", nil, http.StatusOK)
	if topics, ok := view["topics"].([]any); !ok || len(topics) != 0 || view["topics_at"] != nil || view["revs"].(map[string]any)["topics"] != float64(0) {
		t.Fatalf("before the first: %v", view)
	}

	if res, text := r.topicsCall(sid, `{"topics":[{"title":" Export\ncap ","state":"done","at":5,"note":"Kept at 4 MB."},`+
		`{"title":"Pricing","state":"CURRENT","at":61},{"title":"Hiring","state":"open"}]}`); res.IsError || text != "Topics saved." {
		t.Fatalf("set: %s", text)
	}
	view = r.do("GET", livePath(meetingRec)+"?parts=topics", "", nil, http.StatusOK)
	raw, _ := json.Marshal(view["topics"]) // a map's keys in order
	if string(raw) != `[{"at":5,"note":"Kept at 4 MB.","state":"done","title":"Export cap"},{"at":61,"state":"current","title":"Pricing"},{"state":"open","title":"Hiring"}]` {
		t.Fatalf("topics %s", raw)
	}
	at, err := time.Parse(time.RFC3339, view["topics_at"].(string))
	if err != nil || time.Since(at) > time.Minute || view["revs"].(map[string]any)["topics"] != float64(1) || len(view) != 3 {
		t.Fatalf("topics_at and revs %v", view)
	}
	if res, text := r.topicsCall(sid, `{"topics":[{"title":"Export cap","state":"done","at":5,"note":"Kept at 4 MB."},`+
		`{"title":"Pricing","state":"current","at":61},{"title":"Hiring","state":"open"}]}`); res.IsError || !strings.Contains(text, "nothing changed") {
		t.Fatalf("the same again: %s", text)
	}
	if l, _, _ := r.engine().LiveRead(meetingRec); l.TopicsRev != 1 {
		t.Fatalf("the same list moved the revision: %d", l.TopicsRev)
	}

	many := make([]map[string]any, kb.MaxTopics+1)
	for i := range many {
		many[i] = map[string]any{"title": fmt.Sprintf("t%d", i), "state": "open"}
	}
	manyArgs, _ := json.Marshal(map[string]any{"topics": many})
	for name, c := range map[string]struct{ args, want string }{
		"too many":      {string(manyArgs), "over the 20"},
		"long title":    {`{"topics":[{"title":"` + strings.Repeat("字", kb.MaxTopicTitle+1) + `","state":"open"}]}`, "over the 80"},
		"long note":     {`{"topics":[{"title":"x","state":"open","note":"` + strings.Repeat("a", kb.MaxTopicNote+1) + `"}]}`, "over the 200"},
		"two current":   {`{"topics":[{"title":"a","state":"current"},{"title":"b","state":"current"}]}`, "at most one"},
		"bad state":     {`{"topics":[{"title":"a","state":"later"}]}`, "not done, current or open"},
		"no title":      {`{"topics":[{"title":"  ","state":"open"}]}`, "topic 1 has no title"},
		"negative at":   {`{"topics":[{"title":"a","state":"open","at":-1}]}`, "0 or more seconds"},
		"no topics":     {`{}`, "topics is required"},
		"not a list":    {`{"topics":"Pricing"}`, "topics is required"},
		"a text number": {`{"topics":[{"title":"a","state":"open","at":"5"}]}`, "topics is required"},
	} {
		if res, text := r.topicsCall(sid, c.args); !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("%s: %v %s", name, res.IsError, text)
		}
	}
	if l, _, _ := r.engine().LiveRead(meetingRec); len(l.Topics) != 3 || l.TopicsRev != 1 {
		t.Fatalf("a refused call changed the topics: %+v", l.Topics)
	}
	waitFor(t, "a meeting_live event for the topics", func() bool {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		for _, ev := range r.f.liveEvents {
			changed, _ := ev["changed"].([]any)
			if slices.Contains(changed, any("topics")) && ev["revs"].(map[string]any)["topics"] == float64(1) {
				return true
			}
		}
		return false
	})

	// The batches show the room the topics so far.
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":70,"end":74,"text":"Bo: on pricing, we keep the tiers."}]}`, http.StatusOK)
	before := len(r.sends())
	r.look(meetingRec)
	sends := r.sends()
	if len(sends) != before+1 {
		t.Fatalf("no batch: %v", sends[before:])
	}
	if text, _ := sends[len(sends)-1]["text"].(string); !strings.Contains(text, "meeting_topics_set") ||
		!strings.Contains(text, "- current: Pricing (from 1:01 into the meeting)") || !strings.Contains(text, "- done: Export cap (from 0:05 into the meeting): Kept at 4 MB.") {
		t.Fatalf("batch %s", text)
	}

	// The follow-up's seed lists them.
	rec, _ := r.engine().Store.Recording(meetingRec)
	m, _, _ := r.engine().MeetingRead(meetingRec)
	if seed := r.s.followupSeed(context.Background(), r.engine(), *rec, m, "Draft the email."); !strings.Contains(seed, "- open: Hiring") {
		t.Fatalf("seed %s", seed)
	}

	// A room started before the tool existed: not listed, refused, and its batches show no topics.
	if err := r.s.guestPut(context.Background(), sid, &guestConv{MachineID: "server", Tools: []string{"meeting_lines", "meeting_earlier", "meeting_photo_note"},
		Kind: guestKindMeeting, Recording: meetingRec}); err != nil {
		t.Fatal(err)
	}
	if res, text := r.topicsCall(sid, `{"topics":[]}`); !res.IsError || !strings.Contains(text, "unknown tool") {
		t.Fatalf("an older room set topics: %s", text)
	}
	if _, err := r.engine().LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error {
		l.FedAt = l.FedAt.Add(-2 * feedBatchEvery)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.do("POST", livePath(meetingRec), "", `{"from":1,"segments":[{"start":80,"end":84,"text":"Ada: next, hiring."}]}`, http.StatusOK)
	before = len(r.sends())
	r.look(meetingRec)
	sends = r.sends()
	if len(sends) != before+1 {
		t.Fatalf("no batch: %v", sends[before:])
	}
	if text, _ := sends[len(sends)-1]["text"].(string); strings.Contains(text, "topics") || !strings.Contains(text, "Write nothing in reply.") {
		t.Fatalf("an older room's batch %s", text)
	}
}
