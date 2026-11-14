package persona.core.data

import persona.core.contracts.Item
import persona.core.domain.ContinuityReducer
import persona.core.domain.ContinuityReducer.Gap
import persona.core.domain.ContinuitySync
import persona.core.domain.ContinuitySync.Action

/** Native transport owns requests. Each input and applied view belongs to one chat. */
interface ContinuityAdapter {
    data class State(
        val items: List<Item> = emptyList(),
        val history: String? = null,
        val covered: Int = 0,
        val hasMore: Boolean? = null,
        val loadedOnce: Boolean = false,
    ) {
        val firstSeq: Int? get() = items.minOfOrNull { it.seq }
    }

    data class Batch(
        val items: List<Item>, val history: String? = null, val fromSeq: Int? = null,
        val toSeq: Int? = null, val backlog: Boolean = false, val hasMore: Boolean? = null,
        val moreAfter: Boolean = false,
    )

    sealed interface Input {
        data class Restore(val state: State) : Input
        data class Push(val batch: Batch) : Input
        data class Latest(val batch: Batch) : Input
        data class After(val batch: Batch) : Input
        data class Reload(val items: List<Item>, val history: String?, val toSeq: Int?, val hasMore: Boolean) : Input
        data class Older(val items: List<Item>, val hasMore: Boolean) : Input
        data class OpenFold(val id: String) : Input
        data class Range(val items: List<Item>, val more: Boolean = false) : Input
        data object HubChanged : Input
        data object ContinueFill : Input
        data object Reconnect : Input
        data object LoadOlder : Input
        data object FillFailed : Input
        data object OlderFailed : Input
        data object RangeFailed : Input
    }

    data class Applied(val chat: String, val state: State, val gap: Gap = Gap.None, val requests: List<Action> = emptyList())

    val state: State
    val behind: Boolean
    val asynchronous: Boolean get() = false
    /** Null means queued: the frame delivers the applied view through the adapter's callback. */
    suspend fun accept(input: Input): Applied?
    fun close() {}
}

/** The existing reducer and sync, without changing their rules or request gates. */
class KotlinContinuityAdapter(private val chat: String) : ContinuityAdapter {
    private val reducer = ContinuityReducer()
    private val sync = ContinuitySync(reducer)
    override val state get() = ContinuityAdapter.State(reducer.items, reducer.history, reducer.covered, reducer.hasMore, reducer.loadedOnce)
    override val behind get() = sync.behind

    override suspend fun accept(input: ContinuityAdapter.Input): ContinuityAdapter.Applied {
        var gap = Gap.None
        val requests = when (input) {
            is ContinuityAdapter.Input.Restore -> {
                val s = input.state
                reducer.restore(s.items, s.history, s.covered, s.hasMore, s.loadedOnce)
                // Restore does not read the reducer's lazily ordered list; the native caller decides when to publish.
                return ContinuityAdapter.Applied(chat, s)
            }
            is ContinuityAdapter.Input.Push -> {
                val b = input.batch
                gap = reducer.apply(b.items, b.history, b.fromSeq, b.toSeq, b.backlog, b.hasMore, b.moreAfter)
                sync.settle(gap)
            }
            is ContinuityAdapter.Input.Latest -> {
                val b = input.batch
                reducer.latest(b.items, b.history, b.toSeq, b.hasMore)
                emptyList()
            }
            is ContinuityAdapter.Input.After -> {
                val b = input.batch
                val result = sync.after(b.items, b.history, b.fromSeq, b.toSeq, b.moreAfter)
                gap = result.gap
                result.actions
            }
            is ContinuityAdapter.Input.Reload -> {
                sync.reload(input.items, input.history, input.toSeq, input.hasMore)
                emptyList()
            }
            is ContinuityAdapter.Input.Older -> { sync.older(input.items, input.hasMore); emptyList() }
            is ContinuityAdapter.Input.OpenFold -> sync.openFold(input.id)
            is ContinuityAdapter.Input.Range -> sync.range(input.items, input.more)
            ContinuityAdapter.Input.HubChanged -> sync.settle(Gap.Fill)
            ContinuityAdapter.Input.ContinueFill -> sync.continueFill()
            ContinuityAdapter.Input.Reconnect -> sync.reconnect()
            ContinuityAdapter.Input.LoadOlder -> sync.loadOlder()
            ContinuityAdapter.Input.FillFailed -> { sync.finishFill(); emptyList() }
            ContinuityAdapter.Input.OlderFailed -> { sync.finishOlder(); emptyList() }
            ContinuityAdapter.Input.RangeFailed -> { sync.cancelFold(); emptyList() }
        }
        return ContinuityAdapter.Applied(chat, state, gap, requests)
    }
}
