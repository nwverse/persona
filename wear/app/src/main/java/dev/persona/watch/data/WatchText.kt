package dev.persona.watch.data

import org.json.JSONObject

/** The caller supplies interface-language marks; the wire parser contains no display-language defaults. */
data class WatchTextLabels(
    val photo: String = "", val video: String = "", val file: String = "", val card: String = "",
    val contexts: (Int) -> String = { "" }, val untitledContext: String = "",
) {
    companion object { fun empty() = WatchTextLabels() }
}

data class WatchTextSource(val kind: String, val title: String? = null, val conversationId: String? = null)
data class WatchTextMessage(
    val text: String, val own: Boolean, val source: WatchTextSource?, val injected: Boolean, val fromCard: Boolean,
)

/** Canonical display structures only. Unknown old prompt prose remains text, never inferred automation. */
object WatchText {
    private val context = Regex("<persona-context\\b([^>]*)>([\\s\\S]*?)</persona-context>\\s*")
    private val leadingContext = Regex("^\\s*<persona-context\\b([^>]*)>([\\s\\S]*?)</persona-context>\\s*")
    private val attribute = Regex("([A-Za-z][A-Za-z0-9_-]*)=\"([^\"]*)\"")
    private val special = Regex("(?m)^ {0,3}(?:`{3,}|~{3,})[^\\r\\n]*|<(?:pasted_content|persona-context|persona-view-errors|attached_images|persona-file)\\b[^>]*>|`[^`\\r\\n]+`")
    private val action = Regex("(?:\\r\\n|\\n|\\r)<persona-action view=\"([^\"<>\\r\\n]+)\" action=\"([^\"<>\\r\\n]+)\">(\\{[^\\r\\n]*\\})</persona-action>[ \\t\\r\\n]*\\z")
    private const val INTERJECT_TITLE = "插话" // i18n-ignore: canonical Hub model-instruction title

    fun message(item: JSONObject, labels: WatchTextLabels = WatchTextLabels.empty()): WatchTextMessage {
        val raw = string(item, "text").orEmpty()
        val isUser = string(item, "role") == "user" && string(item, "kind") == "text"
        val metadata = metadata(raw)
        val explicit = item.optJSONObject("source")?.let(::source)
        val marked = if (!metadata.protectsUser) item.optJSONObject("injected")?.takeIf { clean(string(it, "kind")) != null } else null
        val infer = !present(item, "client_msg_id") && !present(item, "from")
        val from = clean(string(item, "from"))
        val origin = if (!isUser) null else explicit ?: when {
            infer && metadata.found -> metadata.source
            marked != null -> injectedSource(marked)
            from != null -> WatchTextSource("conversation", conversationId = from)
            else -> null
        }
        val injected = isUser && marked != null
        val body = when {
            !injected -> raw
            string(marked!!, "kind") == "followup" -> clean(string(marked, "detail")) ?: clean(string(marked, "line")) ?: raw
            else -> clean(string(marked!!, "line")) ?: raw
        }
        return WatchTextMessage(shown(body, labels), isUser && origin == null && !injected && from == null,
            origin, injected, isUser && stripAction(raw) != raw)
    }

    fun shown(raw: String, labels: WatchTextLabels = WatchTextLabels.empty()): String {
        val titles = mutableListOf<String>()
        val parts = mutableListOf<Part>()
        for (part in splitProtected(stripAction(raw), labels)) {
            if (!part.literal && parts.lastOrNull()?.literal == false) {
                val previous = parts.removeAt(parts.lastIndex)
                parts += Part(previous.text + part.text, false)
            } else parts += part
        }
        val result = parts.joinToString("") { part ->
            if (part.literal) part.text else {
                var text = context.replace(part.text) { match ->
                    val attrs = attributes(match.groupValues[1])
                    val title = attrs["title"].orEmpty()
                    if (attrs["kind"] != "message-source" && title != INTERJECT_TITLE) titles += title
                    ""
                }
                text = text.replace(Regex("\\s*<persona-view-errors\\b[^>]*>[\\s\\S]*?</persona-view-errors>\\s*"), "")
                text = text.replace(Regex("\\s*<attached_images>[\\s\\S]*?</attached_images>\\s*"), "\n${labels.photo}\n")
                text = text.replace(Regex("<persona-file\\b([^>]*)>(?:\\s*</persona-file>)?")) { match ->
                    "\n" + (if (attributes(match.groupValues[1])["kind"] == "video") labels.video else labels.file) + "\n"
                }
                text = text.replace(Regex("\\[Image #\\d+]\\s*")) { labels.photo }
                text = markdown(text)
                for (mark in listOf(labels.photo, labels.video, labels.file).filter { it.isNotEmpty() }) {
                    text = text.replace(Regex("(?:${Regex.escape(mark)}\\s*){2,}")) { "$mark " }
                }
                entities(text)
            }
        }.replace(Regex("\\n{3,}"), "\n\n").trim()
        val prefix = when {
            titles.isEmpty() -> ""
            titles.size == 1 && titles.single().isNotBlank() -> "〔${titles.single()}〕"
            titles.size == 1 -> labels.untitledContext.ifBlank { labels.contexts(1) }
            else -> labels.contexts(titles.size)
        }
        return listOf(prefix, result).filter { it.isNotBlank() }.joinToString("\n")
    }

    private data class Metadata(val found: Boolean, val source: WatchTextSource?) {
        val protectsUser get() = found && source == null
    }

    /** First persisted source block is authoritative, including an invalid block or the user sentinel. */
    private fun metadata(raw: String): Metadata {
        var rest = raw
        while (true) {
            val match = leadingContext.find(rest) ?: return Metadata(false, null)
            if (attributes(match.groupValues[1])["kind"] == "message-source") {
                val body = runCatching { JSONObject(match.groupValues[2]) }.getOrNull()
                return Metadata(true, body?.takeUnless { string(it, "author") == "user" }?.let(::source))
            }
            rest = rest.substring(match.range.last + 1)
        }
    }

    private fun source(value: JSONObject): WatchTextSource? = clean(string(value, "kind"))?.let { kind ->
        WatchTextSource(kind, clean(string(value, "title")), clean(string(value, "conversation_id")))
    }
    private fun injectedSource(value: JSONObject): WatchTextSource = when (string(value, "kind")) {
        "followup" -> WatchTextSource(if (string(value, "state") in setOf("daily", "interval")) "patrol" else "followup", clean(string(value, "title")))
        "system" -> WatchTextSource(if (string(value, "state") == "supervise") "supervision" else "system")
        else -> WatchTextSource("system", clean(string(value, "title")))
    }

    private data class Part(val text: String, val literal: Boolean)
    private data class Fence(val char: Char, val count: Int, val language: String) {
        fun closes(line: String): Boolean {
            val indent = line.takeWhile { it == ' ' }.length
            if (indent > 3) return false
            val value = line.substring(indent).trimEnd(' ', '\t')
            return value.length >= count && value.all { it == char }
        }
        companion object {
            fun opening(line: String): Fence? {
                val indent = line.takeWhile { it == ' ' }.length
                if (indent > 3) return null
                val rest = line.substring(indent)
                val character = rest.firstOrNull()?.takeIf { it == '`' || it == '~' } ?: return null
                val count = rest.takeWhile { it == character }.length
                if (count < 3) return null
                val info = rest.substring(count).trim()
                if (character == '`' && '`' in info) return null
                return Fence(character, count, info.split(Regex("[ \\t]+"), limit = 2).first().lowercase())
            }
        }
    }

    /** Fenced examples, inline code and explicit pasted content are never metadata or card instructions. */
    private fun splitProtected(raw: String, labels: WatchTextLabels): List<Part> {
        val out = mutableListOf<Part>()
        var at = 0
        while (at < raw.length) {
            val next = special.find(raw, at)
            if (next == null) { out += Part(raw.substring(at), false); break }
            if (next.range.first > at) out += Part(raw.substring(at, next.range.first), false)
            val token = next.value
            val after = next.range.last + 1
            val opaqueTag = listOf("persona-context", "persona-view-errors", "attached_images").firstOrNull { token.startsWith("<$it") }
            if (opaqueTag != null) {
                val closing = "</$opaqueTag>"
                val close = raw.indexOf(closing, after)
                if (close < 0) { out += Part(raw.substring(next.range.first), true); break }
                at = close + closing.length
                out += Part(raw.substring(next.range.first, at), false)
                continue
            }
            if (token.startsWith("<persona-file")) {
                at = after
                if (raw.startsWith("</persona-file>", after)) at += "</persona-file>".length
                out += Part(raw.substring(next.range.first, at), false)
                continue
            }
            if (token.startsWith("<pasted_content")) {
                val close = raw.indexOf("</pasted_content>", after)
                if (close < 0) { out += Part(raw.substring(next.range.first), true); break }
                out += Part(raw.substring(after, close), true)
                at = close + "</pasted_content>".length
                continue
            }
            val fence = Fence.opening(token)
            if (fence == null) {
                out += Part(if (token.startsWith('`') && token.endsWith('`')) token.drop(1).dropLast(1) else token, true)
                at = after
                continue
            }
            var scan = skipEnding(raw, after)
            val contentStart = scan
            var closingStart = -1
            var closingEnd = raw.length
            while (scan < raw.length) {
                val end = lineEnd(raw, scan)
                if (fence.closes(raw.substring(scan, end))) { closingStart = scan; closingEnd = skipEnding(raw, end); break }
                scan = skipEnding(raw, end)
            }
            val isCard = fence.language in setOf("persona-view", "persona-card")
            val body = raw.substring(contentStart, if (closingStart < 0) raw.length else closingStart)
            if (isCard) out += Part(if (closingStart < 0) labels.card else cardWords(fence.language, body, labels.card) + "\n", false)
            else out += Part(body, true)
            at = closingEnd
        }
        return out
    }

    private fun cardWords(language: String, json: String, fallback: String): String {
        val body = runCatching { JSONObject(json) }.getOrNull() ?: return fallback
        if (language == "persona-view") clean(string(body, "fallback"))?.let { return it }
        return clean(string(body, "title"))?.let { "〔$it〕" } ?: fallback
    }

    private fun stripAction(raw: String): String {
        val match = action.find(raw) ?: return raw
        val prefix = raw.substring(0, match.range.first)
        if (insideFence(prefix) || prefix.lastIndexOf("<pasted_content") > prefix.lastIndexOf("</pasted_content>") ||
            match.groupValues[3].contains('<')) return raw
        if (listOf(match.groupValues[1], match.groupValues[2]).any { !validAttribute(it) }) return raw
        val body = runCatching { JSONObject(match.groupValues[3]) }.getOrNull() ?: return raw
        val keys = body.keys().asSequence().toSet()
        val values = body.optJSONObject("values") ?: return raw
        if (values.keys().asSequence().any { values.opt(it) !is String }) return raw
        if (keys != setOf("values") && !(keys == setOf("values", "html") && body.opt("html") == true && values.length() == 0)) return raw
        return prefix
    }

    private fun insideFence(raw: String): Boolean {
        var fence: Fence? = null
        for (line in raw.split(Regex("\\r\\n|\\n|\\r"))) {
            if (fence == null) fence = Fence.opening(line) else if (fence.closes(line)) fence = null
        }
        return fence != null
    }

    private fun markdown(text: String): String = text
        .replace(Regex("(?m)^#{1,6}[ \\t]+"), "")
        .replace(Regex("(?m)^[ \\t]*>[ \\t]?"), "")
        .replace(Regex("(?m)^[ \\t]*([-*_][ \\t]*){3,}$"), "")
        .replace(Regex("(?m)^([ \\t]*)[-*+][ \\t]+"), "$1• ")
        .replace(Regex("\\*\\*([^*\\n]+)\\*\\*"), "$1")
        .replace(Regex("__([^_\\n]+)__"), "$1")
        .replace(Regex("(?<![*A-Za-z0-9_])\\*([^*\\s][^*\\n]*?)\\*(?![*A-Za-z0-9_])"), "$1")
        .replace(Regex("\\[([^]\\n]+)]\\([^)\\s]+\\)"), "$1")

    private fun attributes(value: String) = attribute.findAll(value).associate { it.groupValues[1] to entities(it.groupValues[2]) }
    private fun entities(value: String): String = value.replace("&quot;", "\"").replace("&#34;", "\"")
        .replace("&apos;", "'").replace("&#39;", "'").replace("&lt;", "<").replace("&gt;", ">").replace("&amp;", "&")
    private fun validAttribute(value: String): Boolean = value.isNotEmpty() && !entities(value).any { it.isISOControl() } &&
        !Regex("&(?!amp;|lt;|gt;|#34;|#39;|quot;|apos;)").containsMatchIn(value)
    private fun clean(value: String?): String? = value?.trim()?.takeIf { it.isNotEmpty() }
    private fun string(value: JSONObject, key: String): String? = value.opt(key) as? String
    private fun present(value: JSONObject, key: String) = value.has(key) && !value.isNull(key)
    private fun lineEnd(value: String, at: Int): Int = value.indexOfAny(charArrayOf('\r', '\n'), at).takeIf { it >= 0 } ?: value.length
    private fun skipEnding(value: String, at: Int): Int = when {
        value.startsWith("\r\n", at) -> at + 2
        at < value.length && value[at] in "\r\n" -> at + 1
        else -> at
    }
}
