package persona.platform.capsule

import androidx.annotation.StringRes
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow

/** Content providers own routing and business actions; the host owns only the Android window. */
interface CapsuleContentSource {
    val content: StateFlow<CapsuleContent>
    val completions: Flow<CapsuleCompletion>
    fun openCompletion(completion: CapsuleCompletion)
    fun noticeShown(id: String)
    fun shown(expanded: Boolean) {}
    fun selectSection(id: String)
    fun select(id: String?)
    suspend fun viewed(id: String?, messageId: String, version: String): Boolean
    suspend fun send(id: String?, text: String)
    suspend fun perform(id: String?, actionId: String)
    fun updateDraft(id: String?, text: String) {}
    fun open(id: String?)
}

enum class CapsuleNoticeKind { Completed, NewMessage }
data class CapsuleCompletion(val id: String, val targetId: String, val title: String,
    val kind: CapsuleNoticeKind = CapsuleNoticeKind.Completed)

enum class CapsuleState { NeedsYou, Working, Done, Idle }
data class CapsuleSection(val id: String, val title: String, val count: Int = 0, val list: Boolean = true)
data class CapsuleAction(val id: String, val label: String)
data class CapsuleMessage(val id: String, val text: String, val fromUser: Boolean,
    val unread: Boolean = false, val sender: String? = null)
data class CapsuleReplyTarget(val id: String, val title: String)
data class CapsuleEntry(
    val id: String,
    val title: String,
    val summary: String,
    val state: CapsuleState,
    val actions: List<CapsuleAction> = emptyList(),
    val unread: Boolean = false,
    val unreadCount: Int? = null,
    val group: String? = null,
    val detailOnly: Boolean = false,
    /** A named group in the unread list (a machine and folder, or 飞书消息); null keeps the inbox's fixed groups. */
    val groupTitle: String? = null,
    /**
     * What changes only when the conversation gets something new (the hub's item versions), for telling a read
     * conversation from one that moved on. Null: the provider has none and the summary stands in.
     */
    val version: String? = null,
)
data class CapsuleContent(
    val entries: List<CapsuleEntry> = emptyList(),
    val selectedId: String? = null,
    val messages: List<CapsuleMessage> = emptyList(),
    val canSend: Boolean = true,
    val busy: Boolean = false,
    val error: String? = null,
    val available: Boolean = false,
    val sections: List<CapsuleSection> = emptyList(),
    val sectionId: String? = null,
    val badgeCount: Int = 0,
    val hasActivity: Boolean = false,
    val headline: CapsuleEntry? = null,
    val pendingCompletion: CapsuleCompletion? = null,
    val noticeTargetIds: Set<String>? = null,
    val readVersion: String? = null,
    val unreadEntries: List<CapsuleEntry> = emptyList(),
    val nextAvailable: Boolean = false,
    val nextBusy: Boolean = false,
    val nextError: String? = null,
    val replyTargets: List<CapsuleReplyTarget> = emptyList(),
    val replyTargetId: String? = null,
    val recipient: String? = null,
    val confirmSend: Boolean = false,
    val context: String? = null,
    val canLoadOlder: Boolean = false,
    val draftText: String? = null,
) {
    val selected: CapsuleEntry? get() = entries.firstOrNull { it.id == selectedId }
    val section: CapsuleSection? get() = sections.firstOrNull { it.id == sectionId }
    val listing: Boolean get() = sections.isNotEmpty() && section?.list == true && selectedId == null
    val urgent: CapsuleEntry? get() = headline ?: entries.minByOrNull { it.state.ordinal }
    val activeCount: Int get() = entries.count { it.state != CapsuleState.Idle }
    val urgentCount: Int get() = urgent?.state?.takeIf { it != CapsuleState.Idle }?.let { state -> entries.count { it.state == state } } ?: 0
    val active: Boolean get() = if (sections.isEmpty()) activeCount > 0 else hasActivity
}

enum class CapsuleMode(val key: String, @StringRes val label: Int) {
    Off("off", R.string.capsule_mode_off), Active("active", R.string.capsule_mode_active), Always("always", R.string.capsule_mode_always);
    companion object { fun fromKey(key: String?) = entries.firstOrNull { it.key == key } ?: Off }
}

object CapsuleVisibility {
    fun visible(mode: CapsuleMode, foreground: Boolean, permitted: Boolean, active: Boolean, expanded: Boolean = false,
        unread: Boolean = false): Boolean = permitted && !foreground && when (mode) {
            CapsuleMode.Off -> false; CapsuleMode.Active -> active || expanded || unread; CapsuleMode.Always -> true
        }
}

/** Coordinates are screen pixels; the vertical fraction survives rotation and window resizing. */
data class CapsuleDock(val right: Boolean = true, val fraction: Float = .15f)
data class CapsuleArea(val left: Int, val top: Int, val right: Int, val bottom: Int) {
    fun position(dock: CapsuleDock, width: Int, height: Int): Pair<Int, Int> {
        val x = if (dock.right) (right - width).coerceAtLeast(left) else left
        val travel = (bottom - top - height).coerceAtLeast(0)
        return x to top + (travel * dock.fraction.coerceIn(0f, 1f)).toInt()
    }
    fun dock(x: Int, y: Int, width: Int, height: Int): CapsuleDock {
        val travel = (bottom - top - height).coerceAtLeast(0)
        return CapsuleDock(x + width / 2 >= (left + right) / 2,
            if (travel == 0) 0f else ((y - top).toFloat() / travel).coerceIn(0f, 1f))
    }
}

/** Full-display insets and a view's clipped insets use different coordinate spaces. */
object CapsuleIme {
    fun top(screenBottom: Int, fullInset: Int, windowTop: Int, windowHeight: Int,
        localInset: Int, visible: Boolean, previous: Int?): Int? = when {
        !visible -> null
        fullInset > 0 -> screenBottom - fullInset
        localInset > 0 && windowHeight > 0 -> windowTop + windowHeight - localInset
        else -> previous
    }
}
