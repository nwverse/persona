package assistant

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A new keyed card whose source budget could not be saved has not reached Lark. Keep the run and its original
// card key so the normal channel tick can retry after storage recovers, instead of declaring the reply finished.
func TestFeishuReadAnswerRetriesBudgetPersistence(t *testing.T) {
	e := readEnv(t)
	e.read(readEvent("om_read_budget", "/read inspect alerts", ""))
	run := e.readLive("fb_om_read_budget")
	var keys []string
	delivered := 0
	e.f.answerApp(AppFeishu, FeishuPost, func(p json.RawMessage) (any, error) {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		keys = append(keys, m["uuid"].(string))
		if len(keys) == 1 {
			return nil, &rpc.Error{Code: assistantwire.CodeFeishuReplyBudgetUnavailable, Message: "feishu: reply budget could not be saved"}
		}
		delivered++
		return map[string]any{"message_id": "om_budget_answer"}, nil
	})
	e.s.finishChannelRun(run, &channel.Result{Answer: "The alerts share one cause."})
	if r, _ := e.s.channelRunByID(run.ID); r == nil {
		t.Fatal("run ended after budget persistence failed")
	}
	if delivered != 0 {
		t.Fatal("failed reservation delivered a card")
	}
	e.s.finishChannelRun(run, &channel.Result{Answer: "The alerts share one cause."})
	if r, _ := e.s.channelRunByID(run.ID); r != nil {
		t.Fatal("recovered run did not finish")
	}
	if delivered != 1 || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("delivered=%d keys=%v", delivered, keys)
	}
}
