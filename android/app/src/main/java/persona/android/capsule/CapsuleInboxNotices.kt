package persona.android.capsule

import persona.feature.inbox.InboxConversationKey
import persona.feature.inbox.InboxConversationRow
import persona.platform.capsule.CapsuleCompletion
import persona.platform.capsule.CapsuleNoticeKind

/** Initial synchronization and loading an existing message window are not new-message edges. */
internal class CapsuleInboxNotices {
    private data class Revision(val items: List<Pair<String, String?>>, val message: String?)
    private var previous: Map<InboxConversationKey, Revision>? = null
    fun update(rows: List<InboxConversationRow>, ready: Boolean,
        entryId: (InboxConversationKey) -> String): List<CapsuleCompletion> {
        if (!ready) return emptyList()
        val before = previous
        val current = rows.associate { row -> row.key to Revision(
            row.records.sortedBy { it.item.id }.map { it.item.id to it.item.version }, row.latestMessageId ?: before?.get(row.key)?.message) }
        previous = current
        if (before == null) return emptyList()
        return rows.filter { row ->
            val old = before[row.key]
            val now = current.getValue(row.key)
            row.unread && (old == null || old.items != now.items ||
                old.message != null && now.message != null && old.message != now.message)
        }.map { CapsuleCompletion("${entryId(it.key)}|${it.version}", entryId(it.key), it.title, CapsuleNoticeKind.NewMessage) }
    }
}
