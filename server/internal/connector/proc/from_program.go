package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connector/serve"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Handle answers a linked program's calls (connector.secrets.*, without the namespace), as fromProgram.
func (c *Conn) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return c.fromProgram(ctx, method, params)
}

// Event takes a linked program's event (changed, without the namespace).
func (c *Conn) Event(event string, params json.RawMessage) { c.onEvent(event, params) }

// fromProgram answers the program's calls: its secret, and only its own (the hub bound Secrets to this connector;
// nothing the program sends picks another), and the knowledge base's (KB, bound the same way).
func (c *Conn) fromProgram(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case serve.MGhToken:
		return c.ghToken(ctx, params)
	case serve.MPostsPut, serve.MPostsComments, serve.MPostsPending, serve.MArchiveChats:
		return c.fromProgramKB(ctx, method, params)
	}
	if c.cfg.Secrets == nil {
		return nil, errors.New("no secrets store")
	}
	switch method {
	case serve.MSecretsLoad:
		data, err := c.cfg.Secrets.Load(ctx)
		return serve.SecretData{Data: data}, err
	case serve.MSecretsSave:
		var p serve.SecretData
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return struct{}{}, c.cfg.Secrets.Save(ctx, p.Data)
	case serve.MSecretsDelete:
		return struct{}{}, c.cfg.Secrets.Delete(ctx)
	}
	return nil, fmt.Errorf("unknown method %s", method)
}

func (c *Conn) fromProgramKB(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if !c.Manifest().Has(connector.CapArchive) {
		return nil, &rpc.Error{Code: serve.CodeNoCapability, Message: c.cfg.Name + " does not declare an archive"}
	}
	if c.cfg.KB == nil {
		return nil, errors.New("no knowledge base on this hub")
	}
	switch method {
	case serve.MPostsPut:
		var p serve.PostsPutParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		st, err := c.cfg.KB.PutPosts(ctx, p.Posts)
		return serve.PostsPutResult{Statuses: st}, err
	case serve.MPostsComments:
		var p serve.PostsCommentsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return struct{}{}, c.cfg.KB.PutComments(ctx, p.ID, p.CommentCount, p.Comments)
	case serve.MPostsPending:
		var p serve.PostsPendingParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		ps, err := c.cfg.KB.PendingPosts(ctx, p.Limit)
		return serve.PostsPendingResult{Posts: ps}, err
	default:
		var p serve.ArchiveChatsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		cs, err := c.cfg.KB.ArchivedChats(ctx, p.Source)
		return serve.ArchiveChatsResult{Chats: cs}, err
	}
}

func (c *Conn) onEvent(event string, params json.RawMessage) {
	if event != serve.EChanged {
		return
	}
	var e serve.ChangedEvent
	if json.Unmarshal(params, &e) != nil {
		return
	}
	scope := connector.Scope(e.Scope)
	if !scope.Known() {
		c.cfg.Logf("connector %s: unknown scope %q changed", c.cfg.ID, e.Scope)
		return
	}
	c.mu.Lock()
	c.pending[scope] = true
	c.mu.Unlock()
	select {
	case c.events <- struct{}{}:
	default: // Watch is woken already and takes this scope too
	}
}

// Watch passes the program's changes on until ctx ends; the hub restarts it like any connector's.
func (c *Conn) Watch(ctx context.Context, changed func(connector.Scope)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.events:
			c.mu.Lock()
			pending := c.pending
			c.pending = map[connector.Scope]bool{}
			c.mu.Unlock()
			if len(pending) == 0 { // the scopes went with the wake before, as onEvent's signal came after
				continue
			}
			c.kick(ctx) // what changed may be its status too (signed out elsewhere)
			for _, scope := range []connector.Scope{connector.ScopeChats, connector.ScopeItems, connector.ScopeInbox} {
				if pending[scope] {
					changed(scope)
				}
			}
		}
	}
}
