package persona.feature.settings

import persona.core.contracts.api.ExtensionView
import persona.core.contracts.api.ExtensionViewBadge
import persona.core.contracts.api.ExtensionViewItem
import persona.core.contracts.generated.ExtensionViewAction
import persona.core.contracts.generated.ExtensionViewActionStatus
import persona.core.domain.views.AssistantCard

// How an assistant's card reads in each state (docs/assistant/assistant-views.md "Actions and multiple pages" "Persistent submits"): what can be tapped,
// what was chosen, what was sent. The renderer stays the extensions' (ExtensionViewContent.kt); only the page it is given
// changes.

/** The few words the presentation writes into a card, in the shown language. */
internal data class AssistantCardWords(val chosen: String, val on: String, val off: String, val more: String)

/** How an answered card's answer is getting on. */
internal enum class AssistantCardStatus { Sending, Sent, Failed }

internal object AssistantCardPresentation {
    /**
     * [page] as the card shows it. Open, push and pop always work. While the card can still be answered ([locked]
     * false) its replies and fields are as declared; once it is answered, or where it cannot answer (a reply still
     * coming in, a block the hub has not registered, a read-only device), the replies cannot be tapped: the chosen one
     * says "Selected", a field reads as a line with what was sent. A section's "Load more" is a reply like any other, so it
     * is drawn as its button (tapped, never sent by scrolling to it); the search box and filter are drawn by the card
     * above the page while it can answer ([searchable]).
     */
    fun page(page: ExtensionView, card: AssistantCard, answer: ExtensionViewAction?, locked: Boolean, words: AssistantCardWords): ExtensionView {
        val chosenBadge = ExtensionViewBadge(words.chosen, "positive")
        val chosen = answer?.action
        val sent = answer?.values.orEmpty()
        fun replies(id: String?) = id != null && card.actions[id]?.answers == true
        fun mark(item: ExtensionViewItem) = if (item.action?.id == chosen) item.copy(badge = chosenBadge) else item
        val sections = page.sections.map { section ->
            val more = section.more?.takeIf { it.action.id.isNotEmpty() }?.let { m ->
                ExtensionViewItem("button", title = m.title?.takeIf { it.isNotBlank() } ?: words.more, action = m.action)
            }
            section.copy(more = null, items = (section.items + listOfNotNull(more)).mapNotNull { item ->
                if (!locked) return@mapNotNull item
                when (item.type) {
                    "row" -> if (replies(item.action?.id)) mark(item).copy(action = null) else item
                    "button" -> when {
                        !replies(item.action?.id) -> item
                        item.action?.id == chosen -> ExtensionViewItem("row", title = item.title, badge = chosenBadge)
                        else -> null
                    }
                    "segmented" -> if (!replies(item.action?.id)) item else {
                        val value = sent[item.name.orEmpty()]?.takeIf { item.action?.id == chosen } ?: item.value
                        ExtensionViewItem("row", title = item.label ?: item.name.orEmpty(),
                            value = item.choices.orEmpty().firstOrNull { it.value == value }?.label ?: value,
                            badge = if (item.action?.id == chosen) chosenBadge else null)
                    }
                    "field" -> {
                        val name = item.name.orEmpty()
                        val value = sent[name] ?: if (item.kind == "secret") null else item.value
                        val shown = when (item.kind) {
                            "toggle" -> if (value == "true") words.on else words.off
                            "choice" -> item.choices.orEmpty().firstOrNull { it.value == value }?.label ?: value
                            else -> value
                        }?.takeIf { it.isNotEmpty() } ?: "—"
                        ExtensionViewItem("row", title = item.label ?: name, value = shown)
                    }
                    "empty" -> if (replies(item.button?.action?.id)) item.copy(button = null) else item
                    else -> item
                }
            })
        }
        return page.copy(search = if (locked) null else page.search, filter = if (locked) null else page.filter,
            refreshAfter = null, state = null, sections = sections)
    }

    /** Whether the card draws a search box or filter above [page] (one shown by [page]). */
    fun searchable(page: ExtensionView): Boolean = page.search != null || page.filter?.let { it.choices.size >= 2 && it.name.isNotEmpty() } == true

    /** The line under an answered card: how its answer is getting on. */
    fun status(answer: ExtensionViewAction?): AssistantCardStatus? = when (answer?.status) {
        null -> null
        ExtensionViewActionStatus.SENT -> AssistantCardStatus.Sent
        ExtensionViewActionStatus.FAILED -> AssistantCardStatus.Failed
        else -> AssistantCardStatus.Sending
    }
}
