package persona.core.data

import android.os.Handler
import android.os.Looper
import android.view.Choreographer
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.*
import persona.core.contracts.Item
import persona.core.contracts.JsonCodec
import persona.core.domain.ContinuityReducer.Gap
import persona.core.domain.ContinuitySync.Action
import persona.core.rustcore.CoreHost
import persona.core.rustcore.RustCore
import persona.core.rustcore.wire.*
import persona.core.rustcore.wire.Action as CoreAction
import persona.core.rustcore.wire.ChatSnapshot as CoreSnapshot

/** A callback represents the next frame, not a timer or a wait for a worker. */
interface ContinuityFrameScheduler {
    fun schedule(frame: () -> Unit)
    /** Creation must start after the frame's traversal, not inside its animation callback. */
    fun afterFirstFrame(start: () -> Unit) = schedule(start)
    /** JVM tests may finish worker work and run their manual frame here. The app never waits. */
    suspend fun submitted(core: RustCore) {}
    val synchronous: Boolean get() = false
}

class AndroidContinuityFrameScheduler : ContinuityFrameScheduler {
    override fun afterFirstFrame(start: () -> Unit) = schedule {
        Handler(Looper.getMainLooper()).post { start() }
    }
    override fun schedule(frame: () -> Unit) {
        Handler(Looper.getMainLooper()).post {
            Choreographer.getInstance().postFrameCallback { frame() }
        }
    }
}

/** Selection is test-only in PR A. Merely creating a Kotlin adapter never initializes JNI. */
internal object ContinuityAdapters {
    fun create(chat: String, applied: (ContinuityAdapter.Applied) -> Unit): ContinuityAdapter =
        if (System.getProperty("persona.continuity", "kotlin") == "rust") {
            RustContinuityAdapter(chat, shared, applied)
        } else KotlinContinuityAdapter(chat)

    private val shared by lazy {
        val scheduler = System.getProperty("persona.continuity.frameScheduler")?.let {
            Class.forName(it).getDeclaredConstructor().newInstance() as ContinuityFrameScheduler
        } ?: AndroidContinuityFrameScheduler()
        RustContinuityDriver(scheduler)
    }
}

/** One lazy process-wide core; tests can own a separate driver and manual frames. */
class RustContinuityDriver(
    private val scheduler: ContinuityFrameScheduler,
    private val create: (CoreHost) -> RustCore = { RustCore(Config(), it) },
) : AutoCloseable {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val owners = linkedMapOf<String, (ChatPatch, List<Request>) -> Unit>()
    private val requests = mutableMapOf<String, MutableList<Request>>()
    private val dirty = linkedSetOf<String>()
    private var scheduled = false
    private var closed = false
    @Volatile private var liveCore: RustCore? = null
    private val coreDelegate = lazy {
        CompletableDeferred<RustCore>().also { ready ->
            // Even first use during launch waits for a frame; loading/creation happens on a worker, not that frame.
            scheduler.afterFirstFrame {
                if (scheduler.synchronous) {
                    try { ready.complete(createCore()) }
                    catch (failure: Throwable) { ready.completeExceptionally(failure) }
                } else scope.launch {
                    try { ready.complete(createCore()) }
                    catch (failure: Throwable) { ready.completeExceptionally(failure) }
                }
            }
        }
    }
    private fun createCore(): RustCore {
        val created = create(host)
        val accepted = synchronized(owners) {
            if (closed) false else { liveCore = created; true }
        }
        if (!accepted) {
            created.close()
            throw CancellationException("Continuity driver was closed")
        }
        return created
    }
    private val core by coreDelegate
    internal val synchronous get() = scheduler.synchronous

    private val host = object : CoreHost {
        override fun changed(version: Long) = this@RustContinuityDriver.changed(version)
        override fun run(effect: Effect) {
            when (effect) {
                is Effect.Fetch -> {
                    synchronized(owners) { requests.getOrPut(effect.chat) { mutableListOf() }.add(effect.request) }
                    changedChat(effect.chat, 0)
                }
                is Effect.Rejected -> error("Continuity action rejected")
            }
        }
    }

    internal fun register(chat: String, apply: (ChatPatch, List<Request>) -> Unit) = synchronized(owners) {
        check(chat !in owners)
        owners[chat] = apply
    }

    /** Compatibility fallback: a global wake must not guess which chat changed. */
    fun changed(version: Long) {
        synchronized(owners) { dirty.addAll(owners.keys) }
        scheduleDrain()
    }

    /** Ready to be called by CoreHost.changedChat when the targeted callback lands. */
    fun changedChat(chat: String, version: Long) {
        synchronized(owners) { if (chat in owners) dirty.add(chat) }
        scheduleDrain()
    }

    private fun scheduleDrain() {
        val start = synchronized(owners) {
            if (scheduled || dirty.isEmpty()) false else { scheduled = true; true }
        }
        if (start) scheduler.schedule(::drainFrame)
    }

    private fun drainFrame() {
        val chats = synchronized(owners) {
            scheduled = false
            dirty.toList().also { dirty.clear() }
        }
        val live = liveCore ?: return
        for (chat in chats) {
            val apply = synchronized(owners) { owners[chat] } ?: continue
            val fetches = synchronized(owners) { requests.remove(chat)?.toList().orEmpty() }
            apply(live.drain(chat), fetches)
        }
    }

    internal suspend fun dispatch(action: CoreAction) {
        val live = core.await()
        val chat = when (action) {
            is CoreAction.OpenChat -> action.chat
            is CoreAction.CloseChat -> action.chat
            is CoreAction.Chat -> action.chat
            is CoreAction.ContinueFill -> action.chat
        }
        synchronized(owners) {
            if (closed || chat !in owners) throw CancellationException("Continuity owner was released")
            check(live.dispatch(action)) { "Continuity action was not queued" }
        }
        if (scheduler.synchronous) changedChat(chat, 0)
        scheduler.submitted(live)
    }

    internal fun remove(chat: String) {
        synchronized(owners) { owners.remove(chat); requests.remove(chat); dirty.remove(chat) }
        synchronized(owners) { if (!closed) liveCore?.dispatch(CoreAction.CloseChat(chat)) }
    }

    override fun close() {
        val live = synchronized(owners) {
            if (closed) return
            closed = true
            owners.clear(); requests.clear(); dirty.clear()
            liveCore.also { liveCore = null }
        }
        scope.cancel()
        live?.close()
    }
}

/** Applies keyed patches to the native list. The repository still owns HTTP and WebSocket transport. */
class RustContinuityAdapter(
    chat: String,
    private val driver: RustContinuityDriver,
    private val applied: (ContinuityAdapter.Applied) -> Unit = {},
) : ContinuityAdapter {
    // Hub pairings and evicted/reopened entries can share a public path; core ownership must not.
    private val owner = "$chat:${ids.incrementAndGet()}"
    private val publicChat = chat
    @Volatile override var state = ContinuityAdapter.State(); private set
    @Volatile private var pendingHubChange = false
    override val behind: Boolean get() = pendingHubChange
    override val asynchronous get() = !driver.synchronous
    private var opened = false
    private var version = 0UL
    private val metadata = Any()
    private var gap = Gap.None
    private var uncertainThrough = 0
    private var needsProgress = false
    private var logical = state
    private var immediate: ContinuityAdapter.Applied? = null

    private fun record(result: ContinuityAdapter.Applied) {
        val previous = immediate
        immediate = result.copy(gap = if (previous != null && previous.gap.ordinal > result.gap.ordinal) previous.gap else result.gap,
            requests = previous?.requests.orEmpty() + result.requests)
    }

    init { driver.register(owner, ::apply) }

    override suspend fun accept(input: ContinuityAdapter.Input): ContinuityAdapter.Applied? {
        immediate = null
        if (!opened && input !is ContinuityAdapter.Input.Restore) {
            opened = true
            driver.dispatch(CoreAction.OpenChat(owner, CoreSnapshot("[]", null, 0, null, false)))
        }
        val action = when (input) {
            is ContinuityAdapter.Input.Restore -> {
                if (opened) driver.dispatch(CoreAction.CloseChat(owner))
                opened = true
                synchronized(metadata) { logical = input.state }
                CoreAction.OpenChat(owner, CoreSnapshot(json(input.state.items), input.state.history,
                    input.state.covered.toLong(), input.state.hasMore, input.state.loadedOnce))
            }
            is ContinuityAdapter.Input.Push -> event(ChatEvent.Pushed(batch(input.batch.also(::observe))))
            is ContinuityAdapter.Input.Latest -> event(ChatEvent.Latest(batch(input.batch.copy(backlog = true).also(::observe))))
            is ContinuityAdapter.Input.After -> event(ChatEvent.After(batch(input.batch.copy(backlog = false, hasMore = null).also(::observe))))
            is ContinuityAdapter.Input.Reload -> {
                synchronized(metadata) { logical = logical.copy(history = input.history ?: logical.history,
                    covered = input.toSeq ?: input.items.maxOfOrNull { it.seq } ?: 0) }
                event(ChatEvent.Reloaded(json(input.items), input.history, input.toSeq?.toLong(), input.hasMore))
            }
            is ContinuityAdapter.Input.Older -> event(ChatEvent.Older(json(input.items), input.hasMore))
            is ContinuityAdapter.Input.OpenFold -> event(ChatEvent.OpenFold(input.id))
            is ContinuityAdapter.Input.Range -> event(ChatEvent.Range(json(input.items), input.more))
            ContinuityAdapter.Input.HubChanged -> { pendingHubChange = true; CoreAction.ContinueFill(owner) }
            ContinuityAdapter.Input.ContinueFill -> CoreAction.ContinueFill(owner)
            ContinuityAdapter.Input.Reconnect -> event(ChatEvent.Reconnected)
            ContinuityAdapter.Input.LoadOlder -> event(ChatEvent.LoadOlder)
            ContinuityAdapter.Input.FillFailed -> event(ChatEvent.FillFailed)
            ContinuityAdapter.Input.OlderFailed -> event(ChatEvent.OlderFailed)
            ContinuityAdapter.Input.RangeFailed -> event(ChatEvent.RangeFailed)
        }
        driver.dispatch(action)
        // ContinueFill has no hub-change signal. FIFO makes this conservative retry follow completion, never poll.
        if (pendingHubChange && (input is ContinuityAdapter.Input.After || input is ContinuityAdapter.Input.Reload)) {
            pendingHubChange = false
            driver.dispatch(CoreAction.ContinueFill(owner))
        }
        return if (driver.synchronous) immediate ?: ContinuityAdapter.Applied(publicChat, state) else null
    }

    private fun event(event: ChatEvent) = CoreAction.Chat(owner, event)

    /** The boundary omits Step.gap. Derive only its scalar baseline rules; never run a second item reducer. */
    private fun observe(b: ContinuityAdapter.Batch) = synchronized(metadata) {
        var covered = logical.covered
        var loaded = logical.loadedOnce
        if (b.history != null && logical.history != null && b.history != logical.history) { covered = 0; loaded = false }
        val reset = b.history != null && logical.history != null && b.history != logical.history
        val nextGap = when {
            reset && b.fromSeq != null && b.fromSeq > 0 -> Gap.Reload
            b.toSeq == null -> if (b.moreAfter) Gap.Reload else Gap.None
            b.fromSeq != null && covered == 0 && b.fromSeq > 0 -> if (loaded || b.backlog) Gap.Reload else Gap.None
            b.fromSeq != null && (b.fromSeq > covered || b.moreAfter) -> Gap.Fill
            else -> Gap.None
        }
        if (nextGap.ordinal > gap.ordinal) gap = nextGap
        if (nextGap != Gap.None) {
            uncertainThrough = maxOf(uncertainThrough, logical.covered, b.toSeq ?: b.items.maxOfOrNull { it.seq } ?: covered)
            if (b.moreAfter || nextGap == Gap.Reload) needsProgress = true
        }
        if (!(reset && b.fromSeq != null && b.fromSeq > 0)) {
            if (b.toSeq == null) covered = maxOf(covered, b.items.maxOfOrNull { it.seq } ?: 0)
            else if (b.fromSeq == null) covered = if (covered > 0 && b.items.firstOrNull()?.seq?.toLong()?.let { it > covered.toLong() + 1 } == true) b.toSeq else maxOf(covered, b.toSeq)
            else if (b.fromSeq <= covered) covered = maxOf(covered, b.toSeq)
            loaded = loaded || b.backlog
        }
        logical = logical.copy(history = b.history ?: logical.history, covered = covered, loadedOnce = loaded)
    }

    private fun apply(patch: ChatPatch, fetches: List<Request>) {
        val changed = patch.version != version
        if (changed && patch.version != 0UL) {
            version = patch.version
            val list = state.items.toMutableList()
            for (op in patch.ops) when (op) {
                is ListOp.Remove -> list.removeAt(op.index.toInt())
                is ListOp.Insert -> list.add(op.index.toInt(), item(op.id, op.json))
                is ListOp.Update -> {
                    check(list[op.index.toInt()].id == op.id)
                    list[op.index.toInt()] = item(op.id, op.json)
                }
            }
            state = ContinuityAdapter.State(list, patch.history, patch.covered.toInt(), patch.has_more, patch.loaded_once)
        }
        val requests = fetches.map { request -> when (request) {
            is Request.FetchAfter -> Action.FetchAfter(request.after_seq.toInt())
            is Request.FetchLatest -> Action.FetchLatest(request.fold_work)
            is Request.Subscribe -> Action.Subscribe(request.since_seq?.toInt(), request.fold_work)
            is Request.FetchOlder -> Action.FetchOlder(request.before_seq.toInt(), request.fold_work)
            is Request.FetchRange -> Action.FetchRange(request.from_seq.toInt(), request.through_seq.toInt())
        } }
        if (!changed && requests.isEmpty() && !driver.synchronous) return
        val result = synchronized(metadata) {
            val current = gap
            // No input acknowledgement exists: an earlier frame cannot clear later input uncertainty.
            if (driver.synchronous || changed && state.covered >= uncertainThrough &&
                (!needsProgress || state.covered > uncertainThrough) &&
                state.items.none { it.seq > state.covered } && requests.none { it is Action.FetchAfter || it is Action.FetchLatest }) {
                gap = Gap.None
                uncertainThrough = 0
                needsProgress = false
            }
            ContinuityAdapter.Applied(publicChat, state, if (driver.synchronous) current else gap, requests)
        }
        if (driver.synchronous) record(result) else applied(result)
    }

    override fun close() = driver.remove(owner)

    private fun item(id: String, json: String) = JsonCodec.decodeFromString(Item.serializer(), json).also { check(it.id == id) }
    private fun json(items: List<Item>) = items.joinToString(",", "[", "]") { JsonCodec.encodeToString(Item.serializer(), it) }
    private fun batch(b: ContinuityAdapter.Batch) = WireBatch(json(b.items), b.history, b.fromSeq?.toLong(), b.toSeq?.toLong(), b.backlog, b.hasMore, b.moreAfter)

    private companion object { val ids = AtomicLong() }
}
