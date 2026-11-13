package persona.core.data.sync

import kotlinx.serialization.json.*
import persona.core.contracts.JsonCodec

// The hub of a chat sync scenario (spec/fixtures/sync/README.md "The hub's state"): its state as the case's `hub` steps
// change it, and the reads a device makes answered from it. Wire JSON throughout, as the hub sends it.

internal class SyncHub(chat: JsonObject, start: JsonObject) {
    val machine: String = chat.str("machine_id") ?: "m1"
    val session: String = chat.str("session_id") ?: "s1"
    private val assistant = chat.bool("assistant") == true
    var history: String? = start.str("history")
        private set
    var lines: List<JsonObject> = start.objects("lines")
        private set
    var toSeq: Int = start.int("to_seq") ?: lines.lastOrNull()?.int("seq") ?: 0
        private set
    /** The pending cards, as wire `Interaction`s. */
    var pending: List<JsonObject> = start.objects("interactions")
        private set
    private var state: String? = start.obj("session")?.str("state")
    private var queued: JsonElement? = start.obj("session")?.get("queued")
    var stream: String = start.str("event_stream") ?: "ev"
        private set
    var eventSeq: Long = start.long("event_seq") ?: 0
        private set
    private val pageTurns: Int? = start.int("page_turns")
    /** The list moves with the transcript, as the hub's does (`rev` is the transcript's size, `updated_at` its last line). */
    private var rev = 100L
    private var updated = "2026-01-02T03:04:05Z"
    private var restarts = 0
    /** Cards the hub raised during the case: the "no revived cards" rule lets them be open again. */
    val raised = mutableSetOf<String>()

    /** A `hub` step, its keys in the README's order. */
    fun apply(change: JsonObject) {
        change.obj("restart")?.let {
            if (it.bool("resumable") != true) {
                restarts++
                stream = "$stream-restart$restarts"
                eventSeq = 0
            }
        }
        change.obj("rewrite")?.let {
            history = it.str("history")
            lines = it.objects("lines")
            toSeq = it.int("to_seq") ?: lines.lastOrNull()?.int("seq") ?: 0
        }
        change["append"]?.let { appended ->
            val more = appended.jsonArray.map { it.jsonObject }
            lines = lines + more
            more.lastOrNull()?.let { last ->
                toSeq = maxOf(toSeq, last.int("seq") ?: toSeq)
                last.str("ts")?.let { if (it > updated) updated = it }
            }
            if (more.isNotEmpty()) rev += 10
        }
        change.int("to_seq")?.let { toSeq = it }
        change.obj("end")?.let { end ->
            pending = pending.filterNot { it.str("id") == end.str("id") }
            eventSeq++
        }
        change.obj("raise")?.let { card ->
            pending = pending + card
            card.str("id")?.let(raised::add)
            eventSeq++
        }
        change.obj("session")?.let { fields ->
            fields.str("state")?.let { state = it }
            if ("queued" in fields) queued = fields["queued"]
        }
    }

    /** The chat's row in the session list, as the hub lists it. */
    fun row(): JsonObject = buildJsonObject {
        put("machine_id", machine); put("session_id", session)
        put("title", "Weekend trip"); put("cwd", "/home/user/project")
        val listed = state ?: "idle"
        put("status", STATUS[listed] ?: "idle"); put("state", listed)
        put("assistant", assistant)
        put("updated_at", updated); put("rev", rev)
        queued?.takeUnless { it is JsonNull }?.let { put("queued", it) }
    }

    /** What the session list's hint (`sessions_list`) names: a checkpoint that moves with the list. */
    fun listRevision(): String = "list-$rev-$state-${queued?.toString().orEmpty().hashCode()}"

    /** The latest page: the last `page_turns` turns (a turn starts at a user's text line), else the whole transcript. */
    fun latest(): JsonObject {
        var from = 0
        pageTurns?.let { turns ->
            val starts = lines.indices.filter { lines[it].str("role") == "user" && lines[it].str("kind") == "text" }
            if (starts.size > turns) from = starts[starts.size - turns]
        }
        return page(lines.drop(from)) { put("to_seq", toSeq); put("has_more", from > 0) }
    }

    fun after(seq: Int): JsonObject = page(lines.filter { (it.int("seq") ?: 0) > seq }) {
        put("from_seq", seq); put("to_seq", toSeq); put("more_after", false)
    }

    fun before(seq: Int): JsonObject = page(lines.filter { (it.int("seq") ?: 0) < seq }) { put("has_more", false) }

    fun range(from: Int, through: Int): JsonObject =
        page(lines.filter { (it.int("seq") ?: 0) in from..through }) { put("more", false) }

    private fun page(items: List<JsonObject>, more: JsonObjectBuilder.() -> Unit) = buildJsonObject {
        put("items", JsonArray(items))
        history?.let { put("history", it) }
        more()
    }

    companion object {
        /** The hub's `status` for each unified state (the web's fake hub does the same). */
        private val STATUS = mapOf("working" to "working", "needs_you" to "blocked", "unread" to "done", "idle" to "idle")
    }
}

internal fun JsonObject.str(key: String): String? = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.content
internal fun JsonObject.int(key: String): Int? = (this[key] as? JsonPrimitive)?.intOrNull
internal fun JsonObject.long(key: String): Long? = (this[key] as? JsonPrimitive)?.longOrNull
internal fun JsonObject.bool(key: String): Boolean? = (this[key] as? JsonPrimitive)?.booleanOrNull
internal fun JsonObject.obj(key: String): JsonObject? = this[key] as? JsonObject
internal fun JsonObject.objects(key: String): List<JsonObject> = (this[key] as? JsonArray)?.map { it.jsonObject }.orEmpty()

/** Wire JSON as the app's type. */
internal inline fun <reified T> JsonElement.wire(): T = JsonCodec.decodeFromJsonElement(this)
