package prompts

// RenderQuestionCancelled reports a closed question without inventing an answer.
func RenderQuestionCancelled() string { return render("agents/question_cancelled", nil) }

// RenderQuestionUserSpoke tells ask_user's caller why a waiting question was withdrawn: the user wrote in the conversation.
func RenderQuestionUserSpoke() string { return render("agents/question_user_spoke", nil) }
