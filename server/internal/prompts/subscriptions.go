package prompts

// Drafting a source subscription's recipe with a model (subscriptions/*, #2040, docs/extensions/subscriptions.md
// "Drafting with a model"): the model picks a tool and its arguments from the tools alone, then maps fields from a
// cut sample of what it answered. Both are structured calls; the JSON Schema of the answer goes with each.

// SubscriptionPickInput is what the model picks a tool from: the source and its read-only tools, never a result
// (subscriptions/pick_input).
type SubscriptionPickInput struct {
	App     string // the connector's id
	Source  string // the source's name
	Service string // the source's service hint (mail:example.com), "" when it has none
	Tools   []SubscriptionTool
}

// SubscriptionTool is one read-only tool, its description and input schema (compact JSON) clipped by the caller.
type SubscriptionTool struct {
	Name, Title, Description, Schema string
}

// RenderSubscriptionPick is the system prompt for picking a tool and its arguments.
func RenderSubscriptionPick() string { return render("subscriptions/pick", nil) }

func RenderSubscriptionPickInput(in SubscriptionPickInput) string {
	return render("subscriptions/pick_input", in)
}

// SubscriptionMap is the system prompt for mapping fields (subscriptions/map): Language is the language the note
// for the user is written in, by its own name.
type SubscriptionMap struct {
	Language string
}

func RenderSubscriptionMap(m SubscriptionMap) string { return render("subscriptions/map", m) }

// SubscriptionMapInput is the picked tool and one sample of its answer (subscriptions/map_input): Sample is the
// answer cut by the caller (JSON, or the start of a text answer when Text).
type SubscriptionMapInput struct {
	App, Source, Tool, Description string
	Args                           string // the arguments as picked, compact JSON
	Text                           bool
	Sample                         string
}

func RenderSubscriptionMapInput(in SubscriptionMapInput) string {
	return render("subscriptions/map_input", in)
}

// SubscriptionRetry asks once more for a draft that did not pass the checks (subscriptions/retry): the check's code
// and its sentence, which never carry source content.
type SubscriptionRetry struct {
	Code, Message string
}

func RenderSubscriptionRetry(r SubscriptionRetry) string { return render("subscriptions/retry", r) }
