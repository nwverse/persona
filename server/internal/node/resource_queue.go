package node

import (
	"context"
	"encoding/json"
	"time"

	rq "github.com/nwverse/persona/server/internal/resourcequeue"
)

func (n *Node) resourceRead(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var in struct {
		Mirror  rq.Mirror `json:"mirror"`
		Project string    `json:"project"`
		Commit  string    `json:"commit"`
		Groups  []string  `json:"groups"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if method == "resource.queue" {
		snap, _ := rq.ReadQueue(ctx, in.Mirror)
		return snap, nil
	}
	return rq.ReadResult(ctx, in.Mirror, in.Project, in.Commit, in.Groups)
}
