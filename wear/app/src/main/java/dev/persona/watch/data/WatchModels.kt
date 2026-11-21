package dev.persona.watch.data

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.time.OffsetDateTime

/** Only the watch's own answer-only credential is persisted after pairing. */
data class HubConfig(
    val baseUrl: String,
    val token: String,
    val fingerprint: String? = null,
    val deviceId: String? = null,
) {
    override fun toString() = "HubConfig(<redacted>)"

    fun validated(): HubConfig {
        val origin = normalizeBaseUrl(baseUrl)
        if (origin.startsWith("http:") && fingerprint != null) throw HubException("https_required")
        if (token.isBlank() || token.length > 4096 || token.any { it.code < 33 || it.code > 126 }) throw HubException("invalid_token")
        if (fingerprint != null && !fingerprint.matches(Regex("[A-Za-z0-9_-]{43}"))) throw HubException("invalid_fingerprint")
        // Existing credentials also identify drafts and caches. Validation must not migrate that identity.
        return copy(baseUrl = URI(baseUrl.trim()).toASCIIString().trimEnd('/'))
    }

    /** New enrollment candidates use a canonical origin; existing stored credentials keep their scope. */
    fun canonicalized(): HubConfig = validated().let { it.copy(baseUrl = normalizeBaseUrl(it.baseUrl)) }

    companion object {
        /** Validate before rebuilding so disallowed URL components cannot disappear during normalization. */
        fun normalizeBaseUrl(address: String): String {
            val uri = try { URI(address.trim()) } catch (_: Exception) { throw HubException("invalid_address") }
            val scheme = uri.scheme?.lowercase()
            if (scheme !in setOf("http", "https") || uri.host.isNullOrBlank() || uri.rawUserInfo != null ||
                uri.rawQuery != null || uri.rawFragment != null || uri.port !in -1..65535 ||
                uri.rawPath.orEmpty().split('/').any { it == "." || it == ".." } || uri.rawPath.orEmpty().contains('%')) {
                throw HubException("invalid_address")
            }
            if (scheme == "http" && !isLocalHost(uri.host)) throw HubException("https_required")
            val port = if (scheme == "http" && uri.port == 80 || scheme == "https" && uri.port == 443) -1 else uri.port
            return URI(scheme, null, uri.host.lowercase(), port, uri.path.orEmpty().trimEnd('/'), null, null).toASCIIString()
        }

        fun sameOrigin(first: String?, second: String): Boolean = first != null &&
            runCatching { normalizeBaseUrl(first) == normalizeBaseUrl(second) }.getOrDefault(false)

        private fun isLocalHost(host: String): Boolean {
            val h = host.lowercase().removePrefix("[").removeSuffix("]")
            if (h == "localhost" || h == "::1" || h.endsWith(".local")) return true
            if (h.contains(':')) return h.startsWith("fc") || h.startsWith("fd") || h.startsWith("fe80:")
            val p = h.split('.').map { it.toIntOrNull() ?: return false }
            if (p.size != 4 || p.any { it !in 0..255 }) return false
            return p[0] == 10 || p[0] == 127 || p[0] == 192 && p[1] == 168 ||
                p[0] == 172 && p[1] in 16..31 || p[0] == 169 && p[1] == 254
        }
    }
}

/** Codes are translated by the UI; never display a server body or an exception containing credentials. */
class HubException(val code: String, val status: Int? = null) : Exception(code)

data class WatchSession(
    val machineId: String,
    val sessionId: String,
    val title: String,
    val state: String,
    val lastText: String = "",
    val assistant: Boolean = false,
    val canSend: Boolean = false,
    val canInterrupt: Boolean = false,
    val canApprove: Boolean = false,
    val updatedAt: Long = 0,
    val quiet: Boolean = false,
    val archived: Boolean = false,
    val groupId: String = "",
    val groupName: String = "",
) { val key: String get() = scopedKey(groupId, "$machineId\u0000$sessionId") }

data class WatchQuestion(val question: String, val options: List<QuestionOption>, val multiSelect: Boolean = false)
data class QuestionOption(val label: String, val description: String = "")
data class DialogOption(val key: String, val label: String, val checked: Boolean = false)
data class WatchInteraction(
    val id: String,
    val machineId: String,
    val sessionId: String,
    val kind: String,
    val title: String,
    val summary: String,
    val questions: List<WatchQuestion> = emptyList(),
    val plan: String = "",
    val screen: String = "",
    val options: List<DialogOption> = emptyList(),
    val multiSelect: Boolean = false,
    val requiresInput: Boolean = false,
    val canAnswer: Boolean = false,
    val canCancel: Boolean = false,
    val muted: Boolean = false,
    val delegating: Boolean = false,
    val groupId: String = "",
    val groupName: String = "",
    val rawOptions: Boolean = false,
) {
    val sessionKey: String get() = scopedKey(groupId, "$machineId\u0000$sessionId")
    val key: String get() = scopedKey(groupId, id)
}

fun scopedKey(group: String, value: String): String = if (group.isBlank()) value else "$group\u0000$value"

data class WatchMachine(val id: String, val name: String, val online: Boolean)
data class InboxItem(
    val id: String,
    val title: String,
    val preview: String,
    val source: String,
    val canReply: Boolean,
    val replyTo: String = "",
    val manager: String? = null,
    val suggestedReply: String = "",
    val accountName: String = "",
)
data class WatchWork(val text: String, val fromSeq: Long? = null, val throughSeq: Long? = null, val count: Int = 1)
data class WatchMessage(val id: String, val role: String, val text: String, val own: Boolean,
                        val work: List<WatchWork> = emptyList(), val sourceTitle: String = "", val sourceKind: String = "")
data class WatchSnapshot(
    val sessions: List<WatchSession>,
    val interactions: List<WatchInteraction>,
    val machines: List<WatchMachine>,
    val inbox: List<InboxItem>,
    val updatedAt: Long,
    val inboxUnavailable: Boolean = false,
)
data class ResolveResult(val already: Boolean = false, val gone: Boolean = false)

object WatchParser {
    fun sessions(body: JSONObject): List<WatchSession> = body.requiredRows("sessions").mapNotNull { o ->
        val machine = o.text("machine_id")
        val id = o.text("session_id")
        if (machine.isEmpty() || id.isEmpty()) return@mapNotNull null
        val capabilities = o.optJSONObject("capabilities")
        val legacy = o.text("source_kind").isEmpty()
        val state = o.text("state").ifEmpty { when (o.text("status")) {
            "working" -> "working"; "blocked" -> "needs_you"; "done" -> "unread"; "idle" -> "idle"; else -> "offline"
        } }
        WatchSession(machine, id, o.text("title").ifBlank { o.text("name").ifBlank { "Persona" } }, state,
            o.text("last_text"), o.optBoolean("assistant"),
            capabilities?.optBoolean("send", false) ?: (legacy && !o.has("capabilities")),
            capabilities?.optBoolean("interrupt", false) ?: (legacy && !o.has("capabilities")),
            capabilities?.optBoolean("approve", false) ?: (legacy && !o.has("capabilities")),
            timestamp(o.text("updated_at")), o.optBoolean("quiet"), o.optBoolean("archived"))
    }

    fun interactions(body: JSONObject, sessions: List<WatchSession>): List<WatchInteraction> {
        val byKey = sessions.associateBy { it.key }
        return body.requiredRows("interactions").mapNotNull { o ->
            val id = o.text("id")
            if (id.isEmpty()) return@mapNotNull null
            val machine = o.text("machine_id")
            val session = o.text("session_id")
            val owner = byKey["$machine\u0000$session"]
            if (o.optBoolean("muted") || o.optBoolean("delegating") || owner?.quiet == true) return@mapNotNull null
            val kind = o.text("kind")
            if (kind == "elicitation") return@mapNotNull null
            val dialog = o.optJSONObject("dialog")
            val protectedInput = o.optBoolean("secret") || o.optJSONArray("fields").let { it != null && it.length() > 0 } ||
                o.text("tool_name") in setOf("secret_run", "secret_handoff", "secrets_run", "secrets_handoff") ||
                o.text("tool_name").startsWith("secret_")
            val legacyScreen = kind == "screen" && dialog == null && !protectedInput
            val screenOptions = when {
                legacyScreen -> rawScreenOptions(o.text("screen"))
                kind == "screen" && dialog != null && !protectedInput -> dialog.optJSONArray("options").objects().filter { it.text("kind").isEmpty() }.map {
                    DialogOption(it.text("key"), it.text("label"), it.optBoolean("checked"))
                }
                else -> emptyList()
            }
            val questions = o.optJSONArray("questions").objects().map { q ->
                WatchQuestion(q.text("question"), q.optJSONArray("options").objects().map { opt ->
                    QuestionOption(opt.text("label"), opt.text("description"))
                }, q.optBoolean("multi_select"))
            }
            WatchInteraction(id, machine, session, kind,
                o.text("task_title").ifBlank { o.text("session_title").ifBlank { owner?.title.orEmpty() } },
                o.text("summary").ifBlank { questions.firstOrNull()?.question ?: o.text("tool_title").ifBlank { o.text("tool_name") } },
                questions, o.text("plan"), o.text("screen"),
                screenOptions, kind == "screen" && !protectedInput && dialog?.optBoolean("multi") == true,
                protectedInput,
                (session.isEmpty() || owner?.canApprove == true) && kind in setOf("question", "plan", "permission", "screen") &&
                    (!protectedInput || kind == "permission"),
                o.optBoolean("can_cancel"), rawOptions = legacyScreen && screenOptions.isNotEmpty())
        }
    }

    fun machines(body: JSONObject): List<WatchMachine> = body.requiredRows("machines").mapNotNull { o ->
        o.text("id").takeIf { it.isNotEmpty() }?.let { WatchMachine(it, o.text("name").ifBlank { it }, o.optBoolean("online")) }
    }

    fun inbox(body: JSONObject): List<InboxItem> = body.requiredRows("items").filter { o ->
        timestamp(o.text("done_at")) == 0L && o.text("status").ifBlank { o.text("tier").ifBlank { "action" } } == "action"
    }.mapNotNull { o ->
        val id = o.text("id")
        if (id.isEmpty()) return@mapNotNull null
        val connector = o.optJSONObject("connector")
        val source = o.text("source")
        InboxItem(id, o.text("title"), o.text("summary").ifBlank { o.text("preview") }, source,
            connector?.optBoolean("can_reply", false) ?: (source in setOf("lark", "mail") && !o.has("doc")),
            connector?.text("reply_to").orEmpty(), o.text("manager").takeIf { it.isNotBlank() },
            o.optJSONObject("agent")?.text("draft").orEmpty(), o.text("account_name"))
    }

    /** Per turn, only the newest assistant segment is outside the collapsed work. */
    fun messages(body: JSONObject, labels: WatchTextLabels = WatchTextLabels.empty()): List<WatchMessage> {
        val result = mutableListOf<WatchMessage>()
        val turn = mutableListOf<JSONObject>()
        var turnId = ""
        fun flush() {
            if (turn.isEmpty()) return
            val texts = turn.filter { it.text("kind") == "text" && WatchText.shown(it.text("text"), labels).isNotBlank() }
            val last = texts.lastOrNull()
            val work = turn.filter { it !== last }.mapNotNull(::workItem)
            if (last != null || work.isNotEmpty()) {
                val first = turn.first()
                result += WatchMessage("turn:" + first.text("id").ifBlank { first.opt("seq").toString() },
                    "assistant", last?.let { WatchText.shown(it.text("text"), labels) }.orEmpty(), false, work)
            }
            turn.clear()
            turnId = ""
        }
        for (item in body.requiredRows("items")) {
            val kind = item.text("kind")
            val role = item.text("role")
            if (kind == "text" && role == "user") {
                flush()
                val shown = WatchText.message(item, labels)
                if (shown.text.isNotBlank()) result += WatchMessage(item.text("id").ifBlank { item.opt("seq").toString() },
                    role, shown.text, shown.own, sourceTitle = shown.source?.title.orEmpty(), sourceKind = shown.source?.kind.orEmpty())
            } else if (kind in setOf("text", "thinking", "tool_use", "tool_result", "fold")) {
                val nextTurn = item.text("turn_id")
                if (nextTurn.isNotBlank() && turnId.isNotBlank() && nextTurn != turnId) flush()
                if (nextTurn.isNotBlank()) turnId = nextTurn
                turn += item
            } else if (kind == "recap") flush()
        }
        flush()
        return result.takeLast(6)
    }

    internal fun workItem(item: JSONObject): WatchWork? {
        val fold = item.optJSONObject("fold")
        if (item.text("kind") == "fold" && fold != null) {
            val from = item.optLong("seq", -1)
            val through = fold.optLong("through_seq", -1)
            if (from < 0 || through < from) return null
            val names = fold.optJSONArray("tool_names")
            val label = if (names == null) "" else (0 until names.length()).map { names.optString(it) }.joinToString(", ")
            return WatchWork(label, from, through, fold.optInt("items", 1).coerceAtLeast(1))
        }
        val words = plainText(item.text("text"))
        val name = item.text("tool_name")
        val text = listOf(name, words).filter { it.isNotBlank() }.joinToString("\n")
        return text.takeIf { it.isNotBlank() }?.let { WatchWork(it) }
    }

    fun snapshot(sessions: List<WatchSession>, interactions: List<WatchInteraction>, machines: List<WatchMachine>,
                 inbox: List<InboxItem>, now: Long, inboxUnavailable: Boolean = false): WatchSnapshot = WatchSnapshot(
        sessions.filter { !it.quiet && !it.archived && (it.assistant || it.state in setOf("working", "needs_you", "unread", "idle")) }
            .sortedWith(compareBy<WatchSession> { when (it.state) { "needs_you" -> 0; "unread" -> 1; "working" -> 2; else -> 3 } }
                .thenByDescending { it.updatedAt }), interactions, machines, inbox, now, inboxUnavailable)

    fun plainText(raw: String): String = raw
        .replace(Regex("<persona-(?:context|view-errors)\\b[^>]*>[\\s\\S]*?</persona-(?:context|view-errors)>"), "")
        .replace(Regex("(?m)^\\s*```.*$"), "")
        .replace(Regex("(?m)^#{1,6}\\s+"), "")
        .replace(Regex("\\*\\*([^*]+)\\*\\*"), "$1")
        .replace(Regex("`([^`]+)`"), "$1")
        .replace(Regex("\\[([^]\\n]+)]\\([^)]*\\)"), "$1")
        .trim()

    /** Old hubs expose only numbered terminal choices. Never infer arbitrary typing or more than one key. */
    fun rawScreenOptions(screen: String): List<DialogOption> {
        val pattern = Regex("^\\s*(❯\\s*)?([1-9])\\.\\s+(.+?)\\s*$")
        val parsed = screen.lineSequence().mapNotNull { line -> pattern.matchEntire(line)?.let {
            DialogOption(it.groupValues[2], it.groupValues[3], it.groupValues[1].isNotEmpty())
        } }.toList()
        val start = parsed.indexOfLast { it.key == "1" }
        if (start < 0) return emptyList()
        return parsed.drop(start).takeIf { options -> options.indices.all { options[it].key == (it + 1).toString() } }.orEmpty()
    }

    private fun timestamp(value: String): Long = runCatching { OffsetDateTime.parse(value).toInstant().toEpochMilli() }
        .getOrDefault(0).coerceAtLeast(0)
}

internal fun JSONObject.text(key: String): String = (opt(key) as? String).orEmpty()
internal fun JSONArray?.objects(): List<JSONObject> = if (this == null) emptyList() else
    (0 until length()).mapNotNull { optJSONObject(it) }
internal fun JSONObject.requiredRows(key: String): List<JSONObject> =
    (optJSONArray(key) ?: throw HubException("invalid_response")).objects()
