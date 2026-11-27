package assistantwire

// todoSortRoutes sort the open to-dos into knowledge-base projects in one go (#13026, docs/assistant/todos.md "Sorting
// into projects"): suggestions, one confirmed batch, the recent batches and an undo. The manager keeps them
// (todo_sort.go, todo_sort_batches.go there); the hub relays them like Routes.
var todoSortRoutes = []string{
	"POST /api/v1/todo-sort/suggest",
	"POST /api/v1/todo-sort/apply",
	"GET /api/v1/todo-sort/batches",
	"POST /api/v1/todo-sort/batches/{id}/undo",
}

func init() { Routes = append(Routes, todoSortRoutes...) }
