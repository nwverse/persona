package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ---- the app's interface (relayed by the hub, assistantwire.Routes) ----

func (s *Service) channelRoutes() {
	handle := func(pattern string, fn http.HandlerFunc) {
		s.handle(pattern, func(w http.ResponseWriter, r *http.Request) { fn(w, withRequestLang(r)) })
	}
	handle("GET /api/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.channelList(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusBadGateway, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"channels": list})
	})
	handle("GET /api/v1/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, ok, err := s.channelDetail(r.Context(), r.PathValue("id"))
		switch {
		case err != nil:
			apierr.Write(w, http.StatusBadGateway, err)
		case !ok:
			apierr.WriteCode(w, http.StatusNotFound, "not_found", errors.New("no such channel"))
		default:
			apierr.WriteJSON(w, d)
		}
	})
	full := func(f func(w http.ResponseWriter, r *http.Request, v channel.View)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, errors.New("this device can only read and answer"))
				return
			}
			v, ok, err := s.channelView(r.Context(), r.PathValue("id"))
			switch {
			case err != nil:
				apierr.Write(w, http.StatusBadGateway, err)
				return
			case !ok:
				apierr.WriteCode(w, http.StatusNotFound, "not_found", errors.New("no such channel"))
				return
			case v.ReadOnly || v.Source == channel.SourceMarket:
				apierr.WriteCode(w, http.StatusConflict, "read_only", errors.New(i18n.T(s.botLang(r.Context()), "assistant-channels.not_set_here", i18n.Args{"name": v.Name})))
				return
			}
			f(w, r, v)
		}
	}
	detail := func(w http.ResponseWriter, r *http.Request, id string) {
		s.forgetChannelList()
		d, _, err := s.channelDetail(r.Context(), id)
		if err != nil {
			apierr.Write(w, http.StatusBadGateway, err)
			return
		}
		apierr.WriteJSON(w, d)
	}
	handle("PUT /api/v1/channels/{id}", full(func(w http.ResponseWriter, r *http.Request, v channel.View) {
		var in struct {
			Enabled  *bool           `json:"enabled"`
			Target   *channel.Target `json:"target"`
			Features map[string]bool `json:"features"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if err := s.updateChannel(r.Context(), v, in.Enabled, in.Target, in.Features); err != nil {
			var re *rpc.Error
			if errors.As(err, &re) && re.Code == assistantwire.CodeNeedsTriageModel {
				apierr.WriteCode(w, http.StatusConflict, re.Code, errors.New(re.Message))
				return
			}
			apierr.WriteCode(w, http.StatusBadRequest, "invalid", err)
			return
		}
		detail(w, r, v.ID)
	}))
	handle("DELETE /api/v1/channels/{id}/binding", full(func(w http.ResponseWriter, r *http.Request, v channel.View) {
		if err := s.appCall(r.Context(), AppChannels, ChannelsUnbind, map[string]any{"channel": v.ID}, nil); err != nil {
			apierr.Write(w, http.StatusBadGateway, err)
			return
		}
		s.forgetChannelTurns(v.ID)
		if v.ID == channel.Feishu {
			s.feishuUnpaired()
		}
		detail(w, r, v.ID)
	}))
	handle("POST /api/v1/channels/{id}/test", full(func(w http.ResponseWriter, r *http.Request, v channel.View) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.testChannel(ctx, v); err != nil {
			apierr.Write(w, http.StatusBadGateway, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	}))
}

// updateChannel applies the app's changes: only what is given.
func (s *Service) updateChannel(ctx context.Context, v channel.View, enabled *bool, target *channel.Target, features map[string]bool) error {
	l := s.botLang(ctx)
	if target != nil {
		if err := target.Check(); err != nil {
			return err
		}
		if target.Kind == channel.TargetConversation && !s.isAssistantConversation(ctx, target.MachineID, target.SessionID) {
			return errors.New(i18n.T(l, "assistant-channels.assistant_chats_only"))
		}
	}
	known := map[string]bool{}
	for _, f := range v.Features {
		known[f.ID] = true
	}
	for id := range features {
		if !known[id] {
			return errors.New(i18n.T(l, "assistant-channels.no_feature", i18n.Args{"name": v.Name, "feature": id}))
		}
	}
	if v.ID == channel.Feishu {
		return s.updateFeishuChannel(ctx, enabled, target, features)
	}
	if v.Source != channel.SourcePlugin {
		return errors.New(i18n.T(l, "assistant-channels.not_set_here", i18n.Args{"name": v.Name}))
	}
	if enabled != nil {
		if err := s.appCall(ctx, AppChannels, ChannelsEnable, map[string]any{"channel": v.ID, "on": *enabled}, nil); err != nil {
			return err
		}
	}
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	s.loadChannelsLocked(ctx)
	p := s.channelPrefLocked(v.ID)
	if target != nil {
		t := *target
		t.Title = ""
		p.Target = &t
	}
	for id, on := range features {
		p.Features[id] = on
		if id == channel.FeatureAllAsks && on {
			p.Features[channel.FeatureAsks] = true
		}
	}
	s.saveChannelsLocked()
	return nil
}

// testChannel sends the owner (only them) a line, to see the channel reaches them.
func (s *Service) testChannel(ctx context.Context, v channel.View) error {
	if v.State != channel.StateConnected {
		return errors.New(i18n.T(s.botLang(ctx), "assistant-channels.not_connected", i18n.Args{"name": v.Name}))
	}
	if v.ID == channel.Feishu {
		return s.appCall(ctx, AppFeishu, FeishuTest, map[string]any{}, nil)
	}
	return s.channelTell(ctx, v.ID, "", i18n.T(s.botLang(ctx), "assistant-channels.test_message"), channel.OutNote)
}
