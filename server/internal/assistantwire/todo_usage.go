package assistantwire

// The to-dos' token usage and estimated cost (assistant/todo_usage.go, #12936), relayed to the manager. The literal
// paths win over GET /api/v1/todos/{id}.
func init() {
	Routes = append(Routes,
		"GET /api/v1/todos/usage",
		"GET /api/v1/todos/usage/prices",
		"PUT /api/v1/todos/usage/prices",
		"GET /api/v1/todos/{id}/usage",
	)
}
