package prompts

// TodoRule is the system prompt that turns an Inbox item into a to-do according to one user rule.
type TodoRule struct {
	RulePrompt string
	Source     string
	Kind       string
	Sender     string
	Title      string
	Context    string
}

func RenderTodoRule(r TodoRule) string { return render("todos/rule", r) }
