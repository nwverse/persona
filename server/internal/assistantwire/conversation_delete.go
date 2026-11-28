package assistantwire

const ConversationDeletePreviewRoute = "GET /api/v1/assistant/conversations/{id}/delete-preview"

func init() { Routes = append(Routes, ConversationDeletePreviewRoute) }
