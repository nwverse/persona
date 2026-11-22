// {{name}}: a Persona extension (docs/extensions/extensions.md "Writing extensions: SDK, templates and command line"). A page on the main navigation with a
// list you can search and open, and a card on the overview. Replace the sample items with your service's.
package main

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

func main() { ext.Main(program()) }

func program() ext.Program {
	var m struct{ ID, Name, Version string }
	_ = json.Unmarshal(manifest, &m)
	a := &app{name: m.Name, greeting: "你好"}
	return ext.Program{
		ID: m.ID, Version: m.Version, Manifest: manifest,
		Init: a.init,
		// Contribution ids are the manifest's: unique among every extension's on the hub.
		UI: map[string]ext.Page{"{{id}}": {Render: a.home, Action: a.homeAction}, "{{id}}-today": {Render: a.today}},
	}
}
