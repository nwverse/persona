package assistant

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// The built-in model catalog (#2455): what is known of the models gateways commonly serve to Claude Code — the
// context window and the thinking levels. A model a gateway lists (POST /api/v1/launchers/discover) is filled in from
// it. The data is cindy's model registry, the Claude series and the models of the common Chinese gateways
// (THIRD_PARTY_NOTICES.md); only what that registry states, nothing guessed.

//go:embed models_catalog.json
var modelCatalogJSON []byte

var modelCatalog = sync.OnceValue(func() map[string]proto.ProviderModel {
	var doc struct {
		Models []proto.ProviderModel `json:"models"`
	}
	if err := json.Unmarshal(modelCatalogJSON, &doc); err != nil {
		panic("models_catalog.json: " + err.Error())
	}
	out := make(map[string]proto.ProviderModel, len(doc.Models))
	for _, m := range doc.Models {
		out[strings.ToLower(m.ID)] = m
	}
	return out
})

var modelDateSuffix = lazyre.New(`-\d{8}$`)

// catalogModel looks a model id up: as given, then without a vendor prefix ("anthropic/…"), the "[1m]" marker and a
// date suffix ("-20250929"), case-insensitively.
func catalogModel(id string) (proto.ProviderModel, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	key = strings.TrimSuffix(key, "[1m]")
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	cat := modelCatalog()
	if m, ok := cat[key]; ok {
		return m, true
	}
	m, ok := cat[modelDateSuffix.ReplaceAllString(key, "")]
	return m, ok
}

// enrichModel fills what the gateway did not say from the catalog; what it said wins. A "[1m]" id is a 1M window.
func enrichModel(m proto.ProviderModel) proto.ProviderModel {
	if c, ok := catalogModel(m.ID); ok {
		if m.Name == "" {
			m.Name = c.Name
		}
		if m.ContextWindow == 0 {
			m.ContextWindow = c.ContextWindow
		}
		if len(m.Efforts) == 0 {
			m.Efforts = c.Efforts
		}
		if m.DefaultEffort == "" {
			m.DefaultEffort = c.DefaultEffort
		}
	}
	if m.ContextWindow == 0 && strings.HasSuffix(strings.ToLower(m.ID), "[1m]") {
		m.ContextWindow = 1000000
	}
	return m
}
