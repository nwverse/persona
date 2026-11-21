package dev.persona.watch.extensions

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.net.URLDecoder
import java.time.OffsetDateTime

data class ExtensionWidget(
    val owner: String,
    val id: String,
    val title: String,
    val source: String,
    val families: List<String>,
    val opens: String? = null,
) {
    val key: String get() = "$owner/$id"
    val family: String get() = ExtensionParser.FAMILIES.firstOrNull { it in families }
        ?: throw ExtensionException(ExtensionProblem.UNAVAILABLE)
}

data class ExtensionWidgetView(val title: String, val items: List<WidgetItem>, val expiresAtMs: Long)
data class WidgetMetric(val label: String, val value: String, val detail: String = "")
data class WidgetPoint(val label: String, val value: Double, val display: String?, val open: Double?, val high: Double?, val low: Double?, val close: Double?)
data class WidgetSeries(val name: String, val points: List<WidgetPoint>)

/** No action identifiers, HTML, image URLs, fields or callbacks enter the watch renderer. */
sealed class WidgetItem {
    data class Text(val text: String) : WidgetItem()
    data class Metric(val metric: WidgetMetric) : WidgetItem()
    data class Progress(val label: String, val ratio: Double, val detail: String) : WidgetItem()
    data class Row(val title: String, val subtitle: String, val value: String) : WidgetItem()
    data class Stats(val values: List<WidgetMetric>) : WidgetItem()
    data class Chart(val title: String, val kind: String, val unit: String, val series: List<WidgetSeries>) : WidgetItem()
}

enum class ExtensionProblem { UNPAIRED, AUTHENTICATION, FORBIDDEN, UNAVAILABLE, RETRYABLE, INVALID_RESPONSE, INVALID_LINK, CONNECTION_CHANGED }
class ExtensionException(val problem: ExtensionProblem) : Exception(problem.name.lowercase())

/** A link carries catalogue identity only, never a server address, credentials or an executable action. */
data class ExtensionLink(val owner: String, val ui: String?, val group: String) {
    fun projection(widgets: List<ExtensionWidget>): ExtensionWidget? = widgets
        .filter { it.owner == owner && (ui == null || it.opens == ui) }.sortedBy { it.id }.firstOrNull()

    companion object {
        fun parse(raw: String, expectedGroup: String = ""): ExtensionLink? = runCatching {
            val uri = URI(raw)
            if (uri.scheme != "persona" || uri.host != "extension" || uri.rawUserInfo != null || uri.port != -1 || uri.rawFragment != null) return null
            val path = uri.rawPath.orEmpty().removePrefix("/").split('/')
            if (path.size !in 1..2 || path.any { !ExtensionParser.validID(it) }) return null
            val query = uri.rawQuery?.split('&') ?: return null
            if (query.size != 1) return null
            val pair = query.single().split('=', limit = 2)
            if (pair.size != 2 || pair[0] != "group") return null
            val group = URLDecoder.decode(pair[1], "UTF-8")
            if (group != expectedGroup) return null
            ExtensionLink(path[0], path.getOrNull(1), group)
        }.getOrNull()
    }
}

object ExtensionParser {
    val FAMILIES = listOf("large", "medium", "small", "accessory_rectangular", "accessory_circular", "accessory_inline")
    private val ID = Regex("[A-Za-z0-9._-]{1,128}")
    fun validID(value: String) = ID.matches(value) && value != "." && value != ".."

    fun catalog(body: JSONObject): List<ExtensionWidget> {
        val array = body.optJSONArray("widgets") ?: invalid()
        if (array.length() > 200) invalid()
        return rows(array).mapNotNull { row ->
            val owner = row.string("extension")
            val id = row.string("id")
            val families = row.optJSONArray("families")?.let { a -> (0 until a.length()).mapNotNull { a.opt(it) as? String } }
                .orEmpty().filter { it in FAMILIES }.distinct()
            if (!validID(owner) || !validID(id) || families.isEmpty()) return@mapNotNull null
            val title = row.string("title", 200).ifBlank { return@mapNotNull null }
            ExtensionWidget(owner, id, title, row.string("extension_name", 200), families,
                row.string("opens").takeIf(::validID))
        }.distinctBy { it.key }
    }

    fun view(body: JSONObject, family: String): ExtensionWidgetView {
        if (family !in FAMILIES) invalid()
        val view = body.optJSONObject("view") ?: invalid()
        val sections = view.optJSONArray("sections") ?: invalid()
        val candidates = rows(sections, 2).flatMap { rows(it.optJSONArray("items"), 300) }
        val accessory = family.startsWith("accessory_")
        val allowed = if (accessory) setOf("text", "metric") + if (family != "accessory_inline") setOf("progress") else emptySet()
            else setOf("text", "metric", "progress", "row", "stat_grid", "chart")
        val limit = when (family) { "small" -> 4; "medium" -> 6; else -> if (accessory) 1 else 12 }
        val items = candidates.filter { it.string("type") in allowed }.mapNotNull { item(it, if (family == "small") 2 else 4) }.take(limit)
        val expires = runCatching { OffsetDateTime.parse(body.string("expires_at")).toInstant().toEpochMilli() }.getOrDefault(0)
        return ExtensionWidgetView(view.string("title", 200), items, expires)
    }

    private fun item(row: JSONObject, statsLimit: Int): WidgetItem? = when (row.string("type")) {
        "text" -> row.string("text", 200).takeIf { it.isNotBlank() }?.let(WidgetItem::Text)
        "metric" -> WidgetItem.Metric(metric(row))
        "progress" -> finite(row, "ratio")?.let { WidgetItem.Progress(row.string("label", 200), it.coerceIn(0.0, 1.0), row.string("detail", 200)) }
        "row" -> WidgetItem.Row(row.string("title", 200), row.string("subtitle", 200), row.string("value", 200))
        "stat_grid" -> WidgetItem.Stats(rows(row.optJSONArray("stats"), statsLimit).map(::metric))
        "chart" -> if (row.string("kind") !in setOf("line", "area", "bar", "pie", "donut", "candlestick")) null else WidgetItem.Chart(row.string("title", 200), row.string("kind", 40), row.string("unit", 40),
            rows(row.optJSONArray("series"), 4).map { series -> WidgetSeries(series.string("name", 100),
                rows(series.optJSONArray("points"), 64).mapNotNull { point -> finite(point, "value")?.let { value ->
                    WidgetPoint(point.string("label", 100), value, point.string("display", 100).takeIf { it.isNotBlank() },
                        finite(point, "open"), finite(point, "high"), finite(point, "low"), finite(point, "close"))
                } }) })
        else -> null
    }

    private fun metric(row: JSONObject) = WidgetMetric(row.string("label", 200), row.string("value", 200), row.string("detail", 200))
    private fun finite(row: JSONObject, key: String) = (row.opt(key) as? Number)?.toDouble()?.takeIf { it.isFinite() }
    private fun JSONObject.string(key: String, maximum: Int = 128) = (opt(key) as? String).orEmpty().take(maximum)
        .filter { !it.isISOControl() || it == '\n' || it == '\t' }
    private fun rows(array: JSONArray?, limit: Int = 200): List<JSONObject> = if (array == null) emptyList()
        else (0 until minOf(array.length(), limit)).mapNotNull { array.optJSONObject(it) }
    private fun invalid(): Nothing = throw ExtensionException(ExtensionProblem.INVALID_RESPONSE)
}
