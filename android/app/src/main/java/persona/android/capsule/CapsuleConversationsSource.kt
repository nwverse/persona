package persona.android.capsule

import persona.android.AppText
import persona.android.R

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeout
import persona.platform.capsule.*

/** Coordinates the shared unread queue without taking ownership of either repository's receipts. */
internal class CapsuleConversationsSource(
    private val agents: CapsuleContentSource,
    private val inbox: CapsuleContentSource,
    scope: CoroutineScope,
    /** Where passed conversations are kept across restarts and updates (null in tests: memory only). */
    private val store: android.content.SharedPreferences? = null,
    private val expandInApp: () -> Unit,
) : CapsuleContentSource {
    /** [list]: the unread list is showing (grouped by machine and folder, Feishu in one group) instead of a conversation. */
    private data class Entrance(val section: String? = null, val inbox: Boolean = false, val list: Boolean = false)
    private data class Next(val busy: Boolean = false, val error: String? = null)
    private val entrance = MutableStateFlow(Entrance())
    private val next = MutableStateFlow(Next())
    /**
     * Conversations read or passed with 下一条 here, by how they looked then. The hub reports a Feishu read a while
     * later, and until then the queue still lists it: 下一条 brought read conversations back. One that changes since
     * (a new message, another count) is unread again.
     */
    private data class Passed(val version: String?, val summary: String)
    private val passed = MutableStateFlow(restorePassed())
    private fun restorePassed(): Map<String, Passed> = store?.all.orEmpty().mapNotNull { (id, value) ->
        val text = value as? String ?: return@mapNotNull null
        val version = text.substringBefore(PASSED_SEPARATOR, "").takeIf { it.isNotEmpty() }
        id to Passed(version, text.substringAfter(PASSED_SEPARATOR))
    }.toMap()
    private fun pass(entry: CapsuleEntry?) {
        entry ?: return
        passed.update { old -> (old + (entry.id to Passed(entry.version, entry.summary))).entries.toList().takeLast(MAX_PASSED).associate { it.key to it.value } }
        store?.edit()?.apply {
            clear(); passed.value.forEach { (id, p) -> putString(id, "${p.version.orEmpty()}$PASSED_SEPARATOR${p.summary}") }
        }?.apply()
    }
    /**
     * Still as it was passed: the same items (their versions change only with something new). Neither counts nor
     * summaries are a signal: a Feishu topic group's preview switches between its newest message and its name, and
     * its count between the hub's record and Feishu, which brought it back again and again.
     */
    private fun stillPassed(entry: CapsuleEntry, done: Map<String, Passed>): Boolean {
        val then = done[entry.id] ?: return false
        return if (entry.version != null && then.version != null) entry.version == then.version else then.summary == entry.summary
    }
    private val receiptLock = Mutex()
    private val receipts = mutableSetOf<Triple<CapsuleContentSource, String?, String>>()
    private var generation = 0L
    private var expanded = false
    private var fullScreen = false
    private val active get() = if (entrance.value.inbox) inbox else agents
    override val completions: Flow<CapsuleCompletion> = merge(agents.completions, inbox.completions)
    override val content = combine(agents.content, inbox.content, entrance, next, passed) { agent, source, place, advancing, done ->
        val lark = AppText.get(R.string.app_capsule_lark_group)
        val queue = (agent.unreadEntries + source.unreadEntries.map { it.copy(group = LarkGroup, groupTitle = lark) }).distinctBy { it.id }
            .filterNot { stillPassed(it, done) }
        val selected = if (place.inbox) source else agent
        // 收件箱 is no longer a section of its own: Feishu conversations are reached through 未读 like Agent sessions.
        val section = (place.section ?: selected.sectionId)?.takeIf { it == MainSection } ?: UnreadSection
        val listing = section == UnreadSection && place.list
        val entries = if (section == UnreadSection) queue + selected.entries.filter {
            it.id == selected.selectedId && queue.none { unread -> unread.id == it.id }
        } else selected.entries
        selected.copy(entries = entries, sectionId = section,
            selectedId = if (listing) null else selected.selectedId,
            messages = if (listing) emptyList() else selected.messages,
            readVersion = if (listing) null else selected.readVersion,
            busy = if (listing) agent.busy || source.busy else selected.busy,
            error = if (listing) agent.error ?: source.error else selected.error,
            sections = listOf(CapsuleSection(UnreadSection, AppText.get(R.string.app_capsule_unread), queue.size, list = true),
                CapsuleSection(MainSection, AppText.get(R.string.app_capsule_main_section), list = false)),
            unreadEntries = queue, badgeCount = queue.size,
            hasActivity = agent.hasActivity || source.hasActivity,
            headline = queue.firstOrNull() ?: agent.headline ?: source.headline,
            nextAvailable = selected.selectedId != null && (selected.selected?.unread == true || queue.any { it.id != selected.selectedId }),
            nextBusy = advancing.busy, nextError = advancing.error,
            pendingCompletion = agent.pendingCompletion ?: source.pendingCompletion,
            noticeTargetIds = agent.noticeTargetIds.orEmpty() + source.noticeTargetIds.orEmpty())
    }.stateIn(scope, SharingStarted.Eagerly, CapsuleContent())

    private fun invalidate() { generation++; next.value = Next() }
    private fun isInbox(id: String) = inbox.content.value.entries.any { it.id == id } ||
        inbox.content.value.unreadEntries.any { it.id == id }
    private fun useInbox(value: Boolean) {
        entrance.value = entrance.value.copy(inbox = value)
        agents.shown((expanded || fullScreen) && !value)
        inbox.shown((expanded || fullScreen) && value)
    }
    override fun shown(expanded: Boolean) {
        this.expanded = expanded
        if (!expanded && !fullScreen) invalidate()
        agents.shown((expanded || fullScreen) && !entrance.value.inbox)
        inbox.shown((expanded || fullScreen) && entrance.value.inbox)
    }
    fun fullScreen(value: Boolean) {
        fullScreen = value
        shown(expanded)
    }
    override fun selectSection(id: String) {
        invalidate()
        require(id in setOf(UnreadSection, MainSection, InboxSection))
        entrance.value = entrance.value.copy(section = id)
        when (id) {
            InboxSection -> { useInbox(true); inbox.select(null) }
            MainSection -> { useInbox(false); agents.selectSection(MainSection) }
            else -> { entrance.value = entrance.value.copy(list = true); if (!entrance.value.inbox) agents.selectSection(UnreadSection) }
        }
    }
    override fun select(id: String?) {
        invalidate()
        if (id == null) {
            // Back from a conversation reached through 未读: return to the grouped list.
            if ((entrance.value.section ?: content.value.sectionId) == UnreadSection) { entrance.value = entrance.value.copy(section = UnreadSection, list = true); return }
            if (entrance.value.section == InboxSection) inbox.select(null) else active.select(null); return
        }
        // Any conversation but the main chat belongs to 未读 now (the main chat is its own section).
        entrance.value = entrance.value.copy(list = false, section = if (id == MainEntry) MainSection else UnreadSection)
        if (isInbox(id)) { useInbox(true); inbox.select(id) }
        else { useInbox(false); agents.select(id) }
    }
    override fun updateDraft(id: String?, text: String) = active.updateDraft(id, text)
    override suspend fun send(id: String?, text: String) = active.send(id, text)
    override fun open(id: String?) {
        invalidate()
        if (id != null && content.value.entries.firstOrNull { it.id == id }?.detailOnly == true) inbox.open(id)
        else expandInApp()
    }
    override fun openCompletion(completion: CapsuleCompletion) {
        invalidate()
        if (inbox.content.value.noticeTargetIds?.contains(completion.targetId) == true) {
            entrance.value = Entrance(InboxSection, true)
            useInbox(true)
            inbox.openCompletion(completion)
        } else {
            entrance.value = Entrance(UnreadSection, false)
            useInbox(false)
            agents.openCompletion(completion)
        }
    }
    override fun noticeShown(id: String) { agents.noticeShown(id); inbox.noticeShown(id) }

    private suspend fun receipt(source: CapsuleContentSource, id: String?, message: String, version: String): Boolean =
        receiptLock.withLock {
            val key = Triple(source, id, version)
            if (key in receipts) true else source.viewed(id, message, version).also { if (it) receipts += key }
        }
    override suspend fun viewed(id: String?, messageId: String, version: String): Boolean {
        val source = active
        // Taken before the receipt, which may already drop it from the source's unread entries.
        val entry = content.value.unreadEntries.firstOrNull { it.id == id } ?: content.value.selected?.takeIf { it.id == id }
        return receipt(source, id, messageId, version).also { read -> if (read) pass(entry) }
    }
    override suspend fun perform(id: String?, actionId: String) {
        if (actionId.startsWith("read-group:")) {
            invalidate()
            val group = actionId.removePrefix("read-group:")
            val listed = content.value.unreadEntries.filter { it.group == group }
            if (group == LarkGroup) inbox.perform(null, "read-unread") else agents.perform(null, actionId)
            // 全部已读 stands for the whole group: one Feishu cannot clear here (a topic group's thread replies) stays read.
            listed.forEach(::pass)
            return
        }
        if (actionId != "next") { invalidate(); active.perform(id, actionId); return }
        if (next.value.busy) return
        val captured = content.value
        check(captured.selectedId == id && id != null) { AppText.get(R.string.app_capsule_changed) }
        val source = active
        val ticket = generation
        val lastReply = captured.messages.lastOrNull { !it.fromUser }
        next.value = Next(busy = true)
        try {
            captured.readVersion?.let { version ->
                check(lastReply != null && receipt(source, id, lastReply.id, version)) { AppText.get(R.string.app_capsule_read_retry) }
                // The repository publishes its successful receipt before the combined queue removes the row.
                withTimeout(5_000) { source.content.first { it.selectedId != id || it.readVersion != version } }
            } ?: run {
                // No reply to confirm against yet (it ends with your message, or a Feishu window is still loading):
                // 下一条 still means "seen, move on", so the conversation is marked read before moving.
                if (captured.selected?.unread == true) source.perform(id, "seen")
            }
            currentCoroutineContext().ensureActive()
            if (generation != ticket || active !== source || content.value.selectedId != id) return
            val current = source.content.value
            val latest = current.messages.lastOrNull { !it.fromUser }
            if (latest?.id != lastReply?.id || latest?.text != lastReply?.text || current.messages.any { it.unread }) {
                next.value = Next(error = AppText.get(R.string.app_capsule_new_message_stay))
                return
            }
            pass(captured.unreadEntries.firstOrNull { it.id == id } ?: captured.selected?.takeIf { it.id == id })
            val target = content.value.unreadEntries.firstOrNull { it.id != id }
            if (target != null) select(target.id)
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { if (generation == ticket) next.value = Next(error = error.message ?: AppText.get(R.string.app_capsule_read_retry)) }
        finally { if (generation == ticket) next.value = next.value.copy(busy = false) }
    }
}

/** Passed conversations remembered at most (the newest kept), and what separates version from summary in the store. */
private const val MAX_PASSED = 300
private const val PASSED_SEPARATOR = "\u0001"
