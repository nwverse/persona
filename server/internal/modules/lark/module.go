// Package lark is the Feishu / Lark module (docs/architecture/core-and-modules.md, #13404): the bot's client
// (feishubot), which the extension host drives through internal/botport. internal/modules/all provides it; the module
// adds nothing to the assistant yet, so Register adds nothing.
package lark

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/modapi"
)

//go:embed module.json
var manifest []byte

// Module is the Feishu / Lark module.
type Module struct{}

// Info is the embedded manifest (module.json, checked against schema/module.schema.json).
func (Module) Info() modapi.Info {
	var info modapi.Info
	if err := json.Unmarshal(manifest, &info); err != nil {
		panic("lark: module.json: " + err.Error()) // embedded and tested: never at run time
	}
	return info
}

// Register adds nothing: the extension host reaches the bot's client through internal/botport.
func (Module) Register(modapi.Host) error { return nil }
