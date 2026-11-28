package proto

// FeatureConversationDeletePreview announces the read-only dashboard count before conversation deletion.
const FeatureConversationDeletePreview = "assistant_conversation_delete_preview"

func init() { Features = append(Features, FeatureConversationDeletePreview) }
