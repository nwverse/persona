package assistantwire

// templateRoutes apply the settings part of a template (#16772, docs/protocol/protocol.md "Templates"): the
// default manager checks and writes each persona_settings key with its own setter (templates.go there); the hub relays
// it like Routes.
var templateRoutes = []string{
	"POST /api/v1/templates/settings",
}

func init() { Routes = append(Routes, templateRoutes...) }
