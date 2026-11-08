package prompts

// RenderQuestionCancelled reports a closed question without inventing an answer.
func RenderQuestionCancelled() string { return render("agents/question_cancelled", nil) }

// RenderQuestionUserSpoke tells ask_user's caller why a waiting question was withdrawn: the user wrote in the conversation.
func RenderQuestionUserSpoke() string { return render("agents/question_user_spoke", nil) }

// QuestionMovedToTodo is what ask_user returns when nobody answered in time and the question became a to-do (#1617).
type QuestionMovedToTodo struct {
	TodoID string
	Title  string
}

func RenderQuestionMovedToTodo(q QuestionMovedToTodo) string {
	return render("agents/question_moved_to_todo", q)
}
