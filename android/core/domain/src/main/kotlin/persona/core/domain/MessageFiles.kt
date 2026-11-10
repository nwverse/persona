package persona.core.domain

import persona.core.contracts.ProtocolCompatibility

/**
 * One `<persona-file kind="video" path="…" name="…" size="…" duration="…"/>` tag of a message (docs/protocol/protocol.md "Sending videos",
 * iOS `AttachedFile`, web `parseUserText`): a file the phone uploaded to the session's machine. [kind] is `video`,
 * `audio` or `file`; [size] 0 and [duration] null when the tag does not say.
 */
data class AttachedFile(val kind: String, val path: String, val name: String, val size: Long = 0, val duration: Double? = null) {
    /** The tag again, as iOS `AttachedFile.xml` writes it: what editing a message puts back so the file goes along. */
    fun toXml(): String {
        fun esc(s: String) = s.replace("&", "&amp;").replace("\"", "&quot;").replace("<", "&lt;").replace(">", "&gt;")
        val d = duration?.let { " duration=\"" + "%.1f".format(java.util.Locale.ROOT, it) + "\"" }.orEmpty()
        return "<persona-file kind=\"${esc(kind)}\" path=\"${esc(path)}\" name=\"${esc(name)}\" size=\"$size\"$d/>"
    }
}

private val fileTag = ProtocolCompatibility.fileTag
private val fileAttribute = Regex("(\\w+)=\"([^\"]*)\"")

/**
 * The files [text] carries as `<persona-file>` tags, in order, and the text without them (each tag a line break, the
 * whole trimmed). A tag without a path is dropped, not drawn.
 */
fun parseAttachedFiles(text: String): Pair<List<AttachedFile>, String> {
    if (!ProtocolCompatibility.hasFileTag(text)) return emptyList<AttachedFile>() to text
    val files = fileTag.findAll(text).mapNotNull { match ->
        val a = fileAttribute.findAll(match.groupValues[1]).associate { it.groupValues[1] to decodeXmlEntities(it.groupValues[2]) }
        val path = a["path"]?.takeIf(String::isNotEmpty) ?: return@mapNotNull null
        AttachedFile(
            kind = a["kind"]?.takeIf(String::isNotEmpty) ?: "video",
            path = path,
            name = a["name"]?.takeIf(String::isNotEmpty) ?: path.trimEnd('/').substringAfterLast('/').ifEmpty { path },
            size = a["size"]?.toLongOrNull()?.coerceAtLeast(0) ?: 0,
            duration = a["duration"]?.toDoubleOrNull()?.takeIf { it.isFinite() && it >= 0 },
        )
    }.toList()
    return files to fileTag.replace(text, "\n").trim()
}

/** "0:13", "1:05:03": a length as players show it, to the nearest second (iOS `RecordingFormat.clock(duration.rounded())`). */
fun mediaClock(seconds: Double): String {
    val s = Math.round(seconds).coerceAtLeast(0)
    return if (s >= 3600) "%d:%02d:%02d".format(java.util.Locale.ROOT, s / 3600, s / 60 % 60, s % 60)
    else "%d:%02d".format(java.util.Locale.ROOT, s / 60, s % 60)
}
