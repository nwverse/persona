package assistant

import (
	"encoding/json"
	"testing"
)

// topic_policy is fresh_each_run under a track rhythm's name (#13501): new starts a conversation each run, continue
// keeps one; absent keeps the choice, anything else is refused.
func TestReadTopicPolicy(t *testing.T) {
	for _, c := range []struct {
		body       string
		start, out bool
		bad        bool
	}{
		{body: `{}`, start: true, out: true},
		{body: `{"topic_policy":"new"}`, out: true},
		{body: `{"topic_policy":"continue"}`, start: true, out: false},
		{body: `{"fresh_each_run":false,"topic_policy":"new"}`, out: true},
		{body: `{"topic_policy":"sometimes"}`, start: true, out: true, bad: true},
		{body: `{"topic_policy":1}`, bad: true},
	} {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.body), &raw); err != nil {
			t.Fatal(err)
		}
		task := Task{Repeat: repeatDaily, FreshEachRun: c.start}
		err := readTopicPolicy(raw, &task)
		if (err != nil) != c.bad || task.FreshEachRun != c.out {
			t.Errorf("%s: fresh %v, err %v", c.body, task.FreshEachRun, err)
		}
	}
}
