package prompts

// WatchRun is one watch card's scheduled check (docs/assistant/watch-cards.md): Lang is the owner's language ("" keeps
// Chinese), Display one of number / status / table / trend, Unit the number's unit or "".
type WatchRun struct {
	Lang, ID, Title, Question, Display, Unit string
}

// RenderWatchRun is the prompt a watch card's scheduled task runs with: check the question, record it with watch_report.
func RenderWatchRun(w WatchRun) string { return render("assistant/watch_run", w) }
