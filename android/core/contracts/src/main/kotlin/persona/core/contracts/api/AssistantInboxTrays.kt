package persona.core.contracts.api

import kotlinx.coroutines.flow.Flow
import persona.core.contracts.InboxSuggestion

/** Read-only projections keep the chat feature independent of Inbox's page models. */
data class AssistantInboxDraft(
    val itemId: String,
    val title: String,
    val suggestion: InboxSuggestion,
    val manager: String? = null,
)

/** A source is bound to a concrete pairing generation; switching hubs cannot retarget a Send. */
interface AssistantInboxTrays {
    fun drafts(scope: String): Flow<List<AssistantInboxDraft>>
    suspend fun send(scope: String, draft: AssistantInboxDraft, clientId: String): Boolean
    suspend fun refresh(scope: String)
}
