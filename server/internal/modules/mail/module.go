package mail

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/modapi"
)

// The mail module (docs/architecture/core-and-modules.md "Mail"). It reaches the core through the extension manager's
// port (internal/extmgr/mailport), not the kernel: the composition root provides Open there, and the manager serves
// its routes and tools while the kernel does not serve hub_routes and mcp_tools yet.

//go:embed module.json
var manifest []byte

// Info is the embedded manifest (module.json, checked against schema/module.schema.json).
func Info() modapi.Info {
	var info modapi.Info
	if err := json.Unmarshal(manifest, &info); err != nil {
		panic("mail: module.json: " + err.Error()) // embedded and tested: never at run time
	}
	return info
}
