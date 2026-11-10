package persona.core.domain.tasks

import persona.core.contracts.Item
import persona.core.domain.sourceMetadata
import persona.core.domain.sourceOf
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.WorkItem

/** Display-only partition: the repository and timeline keep every original record and fold range. */
data class AssistantConversation(val foreground: List<ChatLine>, val background: List<ChatLine>) {
    companion object {
        fun project(lines: List<ChatLine>, items: List<Item>): AssistantConversation {
            val quiet = mutableSetOf<String>()
            val hints = mutableSetOf<String>()
            var background = false
            items.sortedBy { it.seq }.forEach { item ->
                if (item.kind == "text" && item.role == "user") {
                    val injected = item.injected
                    val metadata = sourceMetadata(item.text.orEmpty())
                    // Only structured provenance can move a whole exchange out of the conversation.
                    val source = if (item.source != null || injected != null || metadata.found) sourceOf(item) else null
                    val systemHint = injected?.kind == "system" && injected.state != "supervise"
                    if (systemHint) hints += item.id
                    else background = source?.kind in setOf("supervision", "patrol", "routine", "schedule") ||
                        (source?.kind == "followup" && !source.conversationId.isNullOrBlank() && injected?.state != "wake") ||
                        (injected?.kind == "followup" && injected.state in setOf("once", "daily", "interval")) ||
                        injected?.kind in setOf("supervision", "routine", "patrol", "schedule")
                } else if (item.kind == "image" && item.role == "user" && item.toolUseId == null) background = false
                if (background) quiet += item.id
                if (item.role == "system" || item.kind == "notice" || item.kind == "recap") hints += item.id
            }
            val quietGroups = lines.filterIsInstance<ChatLine.Work>().filter { line ->
                line.id.removePrefix("w-") in quiet || line.steps.any { it.originalId() in quiet }
            }.mapTo(mutableSetOf()) { it.id }
            if (background) quietGroups += persona.core.domain.lines.LiveWorkId
            val record = mutableListOf<ChatLine>()
            val visible = lines.mapNotNull { line ->
                val hidden = when (line) {
                    is ChatLine.Row -> line.id in quiet || line.id in hints
                    is ChatLine.UserImages -> line.images.all { it.id in quiet }
                    is ChatLine.Work -> line.id in quietGroups
                    is ChatLine.Step -> line.groupId in quietGroups || line.step.originalId() in hints
                    is ChatLine.End -> line.groupId in quietGroups
                    is ChatLine.ApiError -> line.id in quiet
                    is ChatLine.Recaps, is ChatLine.Wakes -> true
                    is ChatLine.Extra -> (line.payload as? AssistantExtra.Feed)?.item?.let(AssistantTaskCards::isBackgroundEvent) == true
                    else -> false
                }
                if (hidden) { record += line; null }
                else if (line is ChatLine.Work) {
                    val clean = line.steps.filterNot { it.originalId() in hints }
                    if (clean != line.steps) record += line
                    if (clean.isEmpty() && !line.live) null else line.copy(steps = clean)
                } else line
            }
            return AssistantConversation(visible, record)
        }
    }
}

private fun WorkItem.originalId(): String = when (this) {
    is WorkItem.Tool -> call.use.id
    is WorkItem.Thought -> item.id
    is WorkItem.Folded -> item.id
    is WorkItem.Narration -> item.id
}
