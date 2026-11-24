package proto

// FeatureTodoMatters is the main conversation's matters: every owner message filed under a concern with
// concern_file, the matter / concern / header_card fields on the to-dos GET, and the PUT/DELETE
// /api/v1/todos/{id}/confirm and PUT /api/v1/todos/{id}/words routes (docs/assistant/todos.md "Matters").
const FeatureTodoMatters = "todo_matters"

func init() { Features = append(Features, FeatureTodoMatters) }
