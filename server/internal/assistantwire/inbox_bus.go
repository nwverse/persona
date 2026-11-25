package assistantwire

// The params and answers of the Inbox's bus methods (MethodInbox*, inbox.go) that had no type of their own: the
// manager decodes and encodes them through these, so their JSON is the shape each method's comment gives. InboxRound,
// InboxRepoMutes, InboxTriagePreview and InboxItemResult are the others.

// InboxAck is the answer {} of MethodInboxCollected and MethodInboxUnmute.
type InboxAck struct{}

// InboxItemParams are MethodInboxItem's params: the item's id.
type InboxItemParams struct {
	ID string `json:"id"`
}

// InboxForgetParams are MethodInboxForget's params: the source whose data the user deleted.
type InboxForgetParams struct {
	Source string `json:"source"`
}

// InboxForgetResult is MethodInboxForget's answer: how many items went.
type InboxForgetResult struct {
	Deleted int `json:"deleted"`
}

// InboxRepoMutesResult is MethodInboxRepoMutes's answer: the source's muted repositories.
type InboxRepoMutesResult struct {
	Repos []string `json:"repos"`
}

// InboxMutesParams are MethodInboxMutes's params: one source's mutes when Source is set, else every mute.
type InboxMutesParams struct {
	Source string `json:"source"`
}

// InboxMutesResult is MethodInboxMutes's answer.
type InboxMutesResult struct {
	Mutes []InboxMute `json:"mutes"`
}

// InboxUnmuteParams are MethodInboxUnmute's params: the mute's id.
type InboxUnmuteParams struct {
	ID string `json:"id"`
}

// InboxPostParams are MethodInboxPost's params: with On, the switch is set first.
type InboxPostParams struct {
	On *bool `json:"on"`
}

// InboxPostResult is MethodInboxPost's answer: whether the assistant may put its conversations into the Inbox.
type InboxPostResult struct {
	On bool `json:"on"`
}

// InboxTriagePreviewResult is MethodInboxTriagePreview's answer: one verdict per dry-run item.
type InboxTriagePreviewResult struct {
	Verdicts []InboxTriageVerdict `json:"verdicts"`
}
