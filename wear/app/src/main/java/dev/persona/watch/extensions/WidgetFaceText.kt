package dev.persona.watch.extensions

data class FaceWords(val title: String, val text: String, val description: String)

/** Only text, metric and progress survive the accessory-family parser. */
object WidgetFaceText {
    fun words(view: ExtensionWidgetView, fallbackTitle: String): FaceWords? {
        val first = view.items.firstOrNull() ?: return null
        val pair = when (first) {
            is WidgetItem.Text -> (view.title.ifBlank { fallbackTitle }) to first.text
            is WidgetItem.Metric -> first.metric.label.ifBlank { fallbackTitle } to first.metric.value
            is WidgetItem.Progress -> first.label.ifBlank { fallbackTitle } to "${(first.ratio.coerceIn(0.0, 1.0) * 100).toInt()}%"
            else -> return null
        }
        val title = clip(pair.first, 200)
        val value = clip(pair.second, 200)
        if (value.isBlank()) return null
        return FaceWords(title, value, listOf(title, value).filter { it.isNotBlank() }.joinToString(": "))
    }
    fun clip(value: String, maximum: Int): String {
        val normalized = value.replace(Regex("[\\s\\p{Cntrl}]+"), " ").trim()
        val count = normalized.codePointCount(0, normalized.length)
        return if (count <= maximum) normalized else normalized.substring(0, normalized.offsetByCodePoints(0, maximum.coerceAtLeast(0)))
    }
}
