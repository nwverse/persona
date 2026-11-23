package assistantwire

// The watch cards' routes (assistant/watch_tools.go, docs/assistant/watch-cards.md): the hub relays them to the manager.
var watchRoutes = []string{
	"GET /api/v1/assistant/watches",
	"POST /api/v1/assistant/watches/{id}/confirm",
	"PUT /api/v1/assistant/watches/order",
	"PUT /api/v1/assistant/watches/{id}",
	"DELETE /api/v1/assistant/watches/{id}",
}

func init() { Routes = append(Routes, watchRoutes...) }
