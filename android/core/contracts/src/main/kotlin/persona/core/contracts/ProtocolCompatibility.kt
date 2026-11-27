package persona.core.contracts

/** Rolling-upgrade contracts for baseline 39a706a338. Keep legacy names out of product code and copy. */
object ProtocolCompatibility {
    const val CAPABILITY = "persona_contracts_v1"
    val pairingMasterKeyUris = listOf("android-keystore://persona.hub-data.master", "android-keystore://jarvis.hub-data.master")
    const val LEGACY_PANE_PREFERENCES = "jarvis-panes"
    fun legacyPaneKey(key: String) = if (key == "split.persona") "split.jarvis" else key
    fun isUpdateAPK(name: String) = name.endsWith(".apk") && (name.startsWith("persona-update-") || name.startsWith("jarvis-update-"))
    val phoneConfirmationActions = setOf("persona.phone.confirm", "jarvis.phone.confirm")
    val appSchemes = setOf("persona", "jarvis")
    val personaServers = setOf("persona", "jarvis")
    val clientHeaders = listOf("X-Persona-Client", "X-Jarvis-Client")
    val operationHeaders = listOf("X-Persona-Op-Id", "X-Jarvis-Op-Id")
    val legacyBuiltins = setOf("jarvis", "jarvis-circuit", "jarvis-pixel", "jarvis-facet", "jarvis-blob")
    val contextTag = Regex("<(?:persona|jarvis)-context\\b([^>]*)>([\\s\\S]*?)</(?:persona|jarvis)-context>")
    val contextBlock = Regex("${contextTag.pattern}\\s*")
    val leadContext = Regex("^\\s*<(?:persona|jarvis)-context\\b[^>]*>[\\s\\S]*?</(?:persona|jarvis)-context>")
    val contextClosingTag = Regex("<(\\s*/\\s*(?:persona|jarvis)-context\\s*>)", RegexOption.IGNORE_CASE)
    val contextTagPrefix = Regex("<(\\s*/?\\s*(?:persona|jarvis)-context\\b)", RegexOption.IGNORE_CASE)
    val escapedContextTagPrefix = Regex("&lt;(\\s*/?\\s*(?:persona|jarvis)-context\\b)", RegexOption.IGNORE_CASE)
    private const val names = "(?:persona|jarvis)-(?:context|file)"
    val unclosedContextBlock = Regex("<$names\\b[^>]*>[\\s\\S]*$", RegexOption.IGNORE_CASE)
    val strayClosingTag = Regex("</\\s*$names\\s*>", RegexOption.IGNORE_CASE)
    val internalTagBlock = Regex(
        "<(system-reminder|command-name|command-message|command-args|local-command-[a-z-]+)\\b[^>]*>[\\s\\S]*?</\\1>",
        RegexOption.IGNORE_CASE,
    )
    val internalTagSingle = Regex(
        "</?(?:system-reminder|command-name|command-message|command-args|local-command-[a-z-]+|persona-[a-z-]+|" + "jarvis-[a-z-]+)\\b[^>]*>",
        RegexOption.IGNORE_CASE,
    )
    val fileTag = Regex("\\s*<(?:persona|jarvis)-file\\b([^>]*?)/?>(?:\\s*</(?:persona|jarvis)-file>)?\\s*")
    private val contextToken = Regex("<(?:persona|jarvis)-context(?:\\s+[^<>]*)?>|</(?:persona|jarvis)-context\\s*>")
    private val legacyOpenTag = Regex("^<persona-context(?=[\\s>])")
    private val legacyCloseTag = Regex("^</persona-context(?=[\\s>])")
    private val outgoingFileElement = Regex("<\\s*/?\\s*(?:persona|jarvis)-file\\b[^>]*>", RegexOption.IGNORE_CASE)
    private val outgoingFileTag = Regex("(^<\\s*/?\\s*)persona-(file)", RegexOption.IGNORE_CASE)
    private val outgoingURL = Regex("(\\burl=[\"'])persona://")

    data class ContextBlockSpan(
        val start: Int,
        val openEnd: Int,
        val closeAt: Int,
        val end: Int,
        val attributes: String,
        val body: String,
    )

    private class ContextTagToken(val start: Int, val end: Int, val isClose: Boolean, val attributes: String)

    /** Depth-aware linear O(n) scanner for top-level context blocks; recovers 2-opens-1-close historical blocks too. */
    fun findContextBlocks(text: String): List<ContextBlockSpan> {
        if (!text.contains("-context")) return emptyList()
        val tokens = ArrayList<ContextTagToken>()
        var hasClose = false
        for (match in contextToken.findAll(text)) {
            val tok = match.value
            val start = match.range.first
            val end = match.range.last + 1
            if (tok.startsWith("</")) {
                tokens += ContextTagToken(start, end, isClose = true, attributes = "")
                hasClose = true
                continue
            }
            if (tok.endsWith("/>")) continue
            var nameEnd = 1
            while (nameEnd < tok.length && !tok[nameEnd].isWhitespace() && tok[nameEnd] != '>') nameEnd++
            tokens += ContextTagToken(start, end, isClose = false, attributes = tok.substring(nameEnd, tok.length - 1))
        }
        if (!hasClose || tokens.size < 2) return emptyList()
        val count = tokens.size
        val bal = IntArray(count + 1)
        for (i in 0 until count) bal[i + 1] = bal[i] + if (tokens[i].isClose) -1 else 1
        val matchIdx = IntArray(count) { -1 }
        val nextAtBal = HashMap<Int, Int>(count)
        var minVal = count + 1
        var minIdx = -1
        for (i in count - 1 downTo 0) {
            if (tokens[i].isClose) {
                val v = bal[i + 1]
                nextAtBal[v] = i
                if (v <= minVal) {
                    minVal = v
                    minIdx = i
                }
                continue
            }
            matchIdx[i] = nextAtBal[bal[i]] ?: minIdx
        }
        val blocks = ArrayList<ContextBlockSpan>()
        var i = 0
        while (i < count) {
            if (tokens[i].isClose || matchIdx[i] < 0) {
                i++
                continue
            }
            val j = matchIdx[i]
            blocks += ContextBlockSpan(
                start = tokens[i].start,
                openEnd = tokens[i].end,
                closeAt = tokens[j].start,
                end = tokens[j].end,
                attributes = tokens[i].attributes,
                body = text.substring(tokens[i].end, tokens[j].start),
            )
            i = j + 1
        }
        return blocks
    }

    fun findLeadingContextBlocks(text: String): Pair<List<ContextBlockSpan>, Int> {
        val blocks = findContextBlocks(text)
        if (blocks.isEmpty()) return emptyList<ContextBlockSpan>() to 0
        var pos = 0
        var count = 0
        for (b in blocks) {
            if (text.substring(pos, b.start).isNotBlank()) break
            count++
            pos = b.end
        }
        return blocks.subList(0, count) to pos
    }

    fun stripLeadingContextBlocks(text: String): String {
        val (_, end) = findLeadingContextBlocks(text)
        return if (end > 0) text.substring(end) else text
    }

    fun stripContextBlocks(text: String, replacement: String = "", trimTrailingWhitespace: Boolean = true): String {
        val blocks = findContextBlocks(text)
        if (blocks.isEmpty()) return text
        val out = StringBuilder(text.length)
        var prev = 0
        for (b in blocks) {
            if (b.start < prev) continue
            out.append(text, prev, b.start)
            out.append(replacement)
            var end = b.end
            if (trimTrailingWhitespace) {
                while (end < text.length && text[end].isWhitespace()) end++
            }
            prev = end
        }
        out.append(text, prev, text.length)
        return out.toString()
    }

    fun hasContextTag(text: String) = text.contains("<persona-context") || text.contains("<jarvis-context")
    fun hasFileTag(text: String) = text.contains("<persona-file") || text.contains("<jarvis-file")
    fun isAppLink(text: String) = appSchemes.any { text.trim().startsWith("$it://") }
    fun canonicalAssistantPath(path: String?) = if (path == "j") "persona" else path
    fun canonicalTab(tab: String?) = if (tab.equals("jarvis", ignoreCase = true)) "persona" else tab
    private val builtinAliases = mapOf("jarvis" to "persona-visor", "jarvis-circuit" to "persona-circuit", "jarvis-pixel" to "persona-pixel", "jarvis-facet" to "persona-facet", "jarvis-blob" to "persona-blob")
    fun canonicalBuiltin(id: String?) = builtinAliases[id] ?: id
    fun builtinForHub(id: String, features: Collection<String> = emptyList()) = if (CAPABILITY in features) id else builtinAliases.entries.firstOrNull { it.value == id }?.key ?: id
    fun managedProvider(source: String?) = source in personaServers
    fun canonicalTool(name: String) = when (name) {
        "ask_jarvis" -> "ask_persona"
        "jarvis_settings" -> "persona_settings"
        else -> name
    }

    /** Canonical fields win, including explicit false; opaque tool JSON never passes through this alias lookup. */
    fun fieldAliases(name: String): List<String> = when (name) {
        "started_by_persona" -> listOf("started_by_jarvis")
        else -> emptyList()
    }

    fun replyReceiptSeed(kind: String, clientId: String) = "jarvis:reply:$kind:$clientId"
    fun operationHeaders(id: String) = operationHeaders.associateWith { id }

    private fun rewriteFileTags(segment: String): String = outgoingFileElement.replace(segment) { tag ->
        outgoingURL.replace(outgoingFileTag.replaceFirst(tag.value, "$1jarvis-$2"), "$1jarvis://")
    }

    /** Use old spellings until the target explicitly advertises its contract; the UI still composes canonical markup.
     *  Rewrites only outer matched protocol context block tags and standalone file tags, never literal tags inside bodies or prose. */
    fun messageForHub(text: String, features: Collection<String> = emptyList()): String {
        if (CAPABILITY in features) return text
        if (!text.contains("persona-")) return text
        val blocks = findContextBlocks(text)
        if (blocks.isEmpty()) return rewriteFileTags(text)
        val out = StringBuilder(text.length)
        var prev = 0
        for (b in blocks) {
            if (b.start > prev) out.append(rewriteFileTags(text.substring(prev, b.start)))
            val openTag = outgoingURL.replace(legacyOpenTag.replaceFirst(text.substring(b.start, b.openEnd), "<jarvis-context"), "$1jarvis://")
            val closeTag = legacyCloseTag.replaceFirst(text.substring(b.closeAt, b.end), "</jarvis-context")
            out.append(openTag)
            out.append(b.body)
            out.append(closeTag)
            prev = b.end
        }
        if (prev < text.length) out.append(rewriteFileTags(text.substring(prev)))
        return out.toString()
    }
}
