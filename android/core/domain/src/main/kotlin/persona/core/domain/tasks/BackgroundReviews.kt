package persona.core.domain.tasks

import persona.core.contracts.FeedItem
import persona.core.contracts.Todo
import persona.core.contracts.api.AssistantConfig
import java.time.Instant

/**
 * The background supervisor's reviews in the main conversation (#5084, docs/assistant/unified-entry.md "Background
 * supervisor"). It reviews the main conversation's tasks without a main-conversation turn, so nothing in the transcript
 * says a task was accepted or sent back: its `review` feed entry is drawn in the conversation as one line that opens the
 * task, instead of staying behind the trays as the reviews the main conversation writes itself do.
 *
 * The hub does not say who wrote a review. What is drawn is every review of the conversation [main] written from
 * [since] on, when the hub's background supervisor started, while it is in effect, unless the task's ledger names another
 * supervisor (a leader's review). A review the main conversation made itself in that time is drawn too.
 */
data class BackgroundReviews(val main: String, val since: Instant) {
    /** The ids of the entries of [feed], [conversation]'s, that are drawn in the conversation. */
    fun shown(conversation: String, feed: List<FeedItem>, todos: List<Todo>): Set<String> {
        if (conversation != main) return emptySet()
        val supervisors = todos.associate { it.id to it.supervision?.supervisor.orEmpty() }
        return feed.filter { item ->
            item.kind == "review" && !item.ref.isNullOrBlank() &&
                AssistantLines.time(item.at)?.isBefore(since) == false &&
                supervisors[item.ref].orEmpty().let { it.isEmpty() || it == main }
        }.mapTo(HashSet()) { it.id }
    }

    companion object {
        /**
         * From `GET /api/v1/assistant`: null while the main conversation supervises its own tasks, before the hub has made
         * a background supervisor, without its start time or a main conversation, and from a hub without any of it.
         */
        fun of(config: AssistantConfig): BackgroundReviews? {
            if (config.mainSupervisesOwnTasks != false || config.backgroundSupervisor.isNullOrBlank()) return null
            val main = config.main?.takeIf(String::isNotBlank) ?: return null
            return BackgroundReviews(main, AssistantLines.time(config.backgroundSupervisorAt) ?: return null)
        }
    }
}
