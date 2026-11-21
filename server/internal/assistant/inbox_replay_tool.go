package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/lazyre"
)

var inboxReplayChatID = lazyre.New(`^oc_[A-Za-z0-9_-]+$`)
var inboxReplayMessageID = lazyre.New(`^om_[A-Za-z0-9_-]+$`)

type inboxReplayToolArgs struct {
	ChatID     string   `json:"chat_id"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	MessageIDs []string `json:"message_ids"`
	ReadOnly   bool     `json:"read_only,omitempty"`
}

func (s *Service) toolInboxReplay(ctx context.Context, args json.RawMessage) (string, error) {
	if err := s.inboxReady(ctx); err != nil {
		return "", err
	}
	if isOutwardReader(ctx) {
		return "", fmt.Errorf("Lark Inbox replay is unavailable in conversations that write to other people")
	}
	var in inboxReplayToolArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("arguments: expected one JSON object")
	}
	if err := validateInboxReplayArgs(in); err != nil {
		return "", err
	}
	if !in.ReadOnly {
		req := assistantwire.InboxLarkReplay{ChatID: in.ChatID, Start: in.Start, End: in.End, MessageIDs: in.MessageIDs}
		if err := s.appCall(ctx, assistantwire.AppInbox, assistantwire.InboxCollect,
			assistantwire.InboxCollectRequest{LarkReplay: &req}, nil); err != nil {
			return "", err
		}
	}
	return s.inboxReplayItems(in.ChatID, in.MessageIDs)
}

func validateInboxReplayArgs(in inboxReplayToolArgs) error {
	if !inboxReplayChatID.MatchString(in.ChatID) {
		return fmt.Errorf("chat_id must be a Lark chat ID beginning with oc_")
	}
	start, startErr := time.Parse(time.RFC3339, in.Start)
	end, endErr := time.Parse(time.RFC3339, in.End)
	if startErr != nil || endErr != nil || !end.After(start) || end.Sub(start) > time.Hour {
		return fmt.Errorf("start and end must be RFC3339 times spanning at most one hour")
	}
	if len(in.MessageIDs) < 1 || len(in.MessageIDs) > 10 {
		return fmt.Errorf("message_ids must contain 1-10 message IDs")
	}
	wanted := make(map[string]struct{}, len(in.MessageIDs))
	for _, id := range in.MessageIDs {
		if !inboxReplayMessageID.MatchString(id) {
			return fmt.Errorf("message_ids must be Lark message IDs beginning with om_")
		}
		if _, exists := wanted[id]; exists {
			return fmt.Errorf("message_ids must be distinct")
		}
		wanted[id] = struct{}{}
	}
	return nil
}

func (s *Service) inboxReplayItems(chatID string, messageIDs []string) (string, error) {
	base := "larkcli:chat:" + chatID
	wanted := make(map[string]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		wanted[id] = struct{}{}
	}
	type result struct {
		Items []InboxItem `json:"items"`
	}
	out := result{Items: []InboxItem{}}
	b := &s.inbox
	b.mu.Lock()
	for _, it := range b.items {
		if it.Source != "larkcli" || it.Connector == nil {
			continue
		}
		thread := it.Connector.Thread
		if thread != base && !strings.HasPrefix(thread, base+":thread:") && !strings.HasPrefix(thread, base+":handoff:") {
			continue
		}
		if _, ok := wanted[it.Last]; !ok {
			continue
		}
		listed := it.listed()
		listed.Context = it.Context
		out.Items = append(out.Items, listed)
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	raw, err := json.Marshal(out)
	b.mu.Unlock()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
