package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.io.RandomAccessFile
import java.nio.file.Files
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.CopyOption
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap

enum class WidgetFaceKind { SHORT, LONG }
data class WidgetBinding(val instanceId: Int, val group: String, val identity: String, val owner: String, val widget: String, val family: String, val kind: WidgetFaceKind)
data class CachedWidget(val widget: ExtensionWidget, val family: String, val view: ExtensionWidgetView, val savedAtMs: Long) {
    val freshUntilMs: Long get() = minOf(view.expiresAtMs, savedAtMs + MAX_AGE_MS)
    fun fresh(now: Long) = now >= savedAtMs && now < freshUntilMs
    companion object { const val MAX_AGE_MS = 3_600_000L }
}

/** Private, bounded snapshots and instance mappings. No address or token is serialized. */
class ExtensionCacheStore(root: File,
    private val moveFile: (Path, Path, Array<CopyOption>) -> Unit = { source, target, options -> Files.move(source, target, *options); Unit },
    private val now: () -> Long = System::currentTimeMillis,
) {
    private val root = root.canonicalFile
    private val lock = locks.computeIfAbsent(this.root.path) { Any() }
    init { require(this.root.isDirectory || this.root.mkdirs()) }

    fun catalog(group: String, config: HubConfig): List<ExtensionWidget>? = access {
        matching(read(), group, config)?.let { ExtensionParser.catalog(JSONObject().put("widgets", it.getJSONArray("catalog"))) }
    }

    fun saveCatalog(group: String, config: HubConfig, widgets: List<ExtensionWidget>) = access {
        requireGroup(group)
        val safe = ExtensionParser.catalog(JSONObject().put("widgets", JSONArray(widgets.map(::encodeWidget))))
        val state = read()
        val previous = matching(state, group, config)
        val views = JSONArray()
        previous?.optJSONArray("views")?.let { list ->
            for (i in 0 until list.length()) list.optJSONObject(i)?.let { row ->
                if (safe.any { it.key == row.optString("key") && row.optString("family") in it.families }) views.put(row)
            }
        }
        groups(state).put(group, JSONObject().put("identity", identity(config)).put("catalog", JSONArray(safe.map(::encodeWidget)))
            .put("views", views).put("saved", now()))
        val groups = groups(state)
        val overflow = groups.length() - MAX_GROUPS
        if (overflow > 0) {
            val slots = bindings(state)
            val boundGroups = slots.keys().asSequence().mapNotNull { key ->
                key.toIntOrNull()?.let { parseBinding(it, slots.optJSONObject(key)) }
            }.filter { groups.optJSONObject(it.group)?.optString("identity") == it.identity }.map { it.group }.toSet()
            val removable = groups.keys().asSequence().filter { it != group && it !in boundGroups }
                .sortedBy { groups.optJSONObject(it)?.optLong("saved") ?: 0 }.take(overflow).toList()
            check(removable.size == overflow) { "widget_cache_capacity" }
            removable.forEach(groups::remove)
        }
        trim(state)
        write(state)
    }

    fun projection(group: String, config: HubConfig, widget: ExtensionWidget, family: String): CachedWidget? = access {
        val saved = matching(read(), group, config) ?: return@access null
        val catalog = ExtensionParser.catalog(JSONObject().put("widgets", saved.getJSONArray("catalog")))
        if (widget !in catalog || family !in widget.families) return@access null
        val rows = saved.optJSONArray("views") ?: return@access null
        for (i in 0 until rows.length()) {
            val row = rows.optJSONObject(i) ?: continue
            if (row.optString("key") == widget.key && row.optString("family") == family) {
                return@access runCatching { CachedWidget(widget, family, ExtensionParser.view(row.getJSONObject("answer"), family), row.getLong("saved")) }.getOrNull()
            }
        }
        null
    }

    fun saveProjection(group: String, config: HubConfig, widget: ExtensionWidget, family: String, view: ExtensionWidgetView,
                       savedAtMs: Long = now()): Boolean = access {
        val state = read()
        val saved = matching(state, group, config) ?: return@access false
        val catalog = ExtensionParser.catalog(JSONObject().put("widgets", saved.getJSONArray("catalog")))
        if (widget !in catalog || family !in widget.families || family !in ExtensionParser.FAMILIES) return@access false
        val answer = encodeView(view)
        if (answer.toString().toByteArray(Charsets.UTF_8).size > MAX_PROJECTION_BYTES) return@access false
        // Reparse before persisting: stored data obeys the same read-only bounds as a network projection.
        val bounded = encodeView(ExtensionParser.view(answer, family))
        val keep = mutableListOf<JSONObject>()
        saved.optJSONArray("views")?.let { rows -> for (i in 0 until rows.length()) rows.optJSONObject(i)?.let {
            if (it.optString("key") != widget.key || it.optString("family") != family) keep += it
        } }
        keep += JSONObject().put("key", widget.key).put("family", family).put("saved", savedAtMs).put("answer", bounded)
        saved.put("views", JSONArray(keep.sortedByDescending { it.optLong("saved") }.take(MAX_VIEWS)))
        trim(state)
        write(state)
        true
    }

    fun invalidate(group: String, config: HubConfig) = access {
        val state = read()
        if (matching(state, group, config) != null) { groups(state).remove(group); write(state) }
    }

    /** A missing projection does not revoke other widgets or even its other advertised families. */
    fun invalidateProjection(group: String, config: HubConfig, widget: ExtensionWidget, family: String) = access {
        val state = read()
        val saved = matching(state, group, config) ?: return@access
        val rows = saved.optJSONArray("views") ?: return@access
        val keep = (0 until rows.length()).mapNotNull { rows.optJSONObject(it) }
            .filterNot { it.optString("key") == widget.key && it.optString("family") == family }
        if (keep.size != rows.length()) { saved.put("views", JSONArray(keep)); write(state) }
    }

    fun bind(instanceId: Int, group: String, config: HubConfig, widget: ExtensionWidget, family: String, kind: WidgetFaceKind) = access {
        require(instanceId >= 0 && familyFor(widget, kind) == family)
        val state = read()
        val saved = matching(state, group, config) ?: error("widget_catalog_missing")
        require(widget in ExtensionParser.catalog(JSONObject().put("widgets", saved.getJSONArray("catalog"))))
        val entries = bindings(state)
        require(entries.has(instanceId.toString()) || entries.length() < MAX_BINDINGS)
        entries.put(instanceId.toString(), JSONObject().put("group", group).put("identity", identity(config))
            .put("owner", widget.owner).put("widget", widget.id).put("family", family).put("kind", kind.name))
        write(state)
    }

    fun binding(instanceId: Int): WidgetBinding? = access { parseBinding(instanceId, bindings(read()).optJSONObject(instanceId.toString())) }
    fun configured(group: String, config: HubConfig, widget: ExtensionWidget): List<WidgetBinding> = access {
        val rows = bindings(read())
        rows.keys().asSequence().mapNotNull { key -> key.toIntOrNull()?.let { parseBinding(it, rows.optJSONObject(key)) } }
            .filter { it.group == group && it.identity == identity(config) && it.owner == widget.owner && it.widget == widget.id && it.family in widget.families }.toList()
    }
    fun remove(instanceId: Int) = access { val state = read(); bindings(state).remove(instanceId.toString()); write(state) }

    private fun parseBinding(id: Int, row: JSONObject?): WidgetBinding? = runCatching {
        if (id < 0 || row == null) return null
        val group = row.getString("group"); requireGroup(group)
        val owner = row.getString("owner"); val widget = row.getString("widget")
        require(ExtensionParser.validID(owner) && ExtensionParser.validID(widget))
        val identity = row.getString("identity"); require(identity.matches(Regex("[a-f0-9]{64}")))
        val kind = WidgetFaceKind.valueOf(row.getString("kind"))
        val family = row.getString("family"); require(family in families(kind))
        WidgetBinding(id, group, identity, owner, widget, family, kind)
    }.getOrNull()

    private fun matching(state: JSONObject, group: String, config: HubConfig): JSONObject? {
        requireGroup(group)
        return groups(state).optJSONObject(group)?.takeIf { it.optString("identity") == identity(config) }
    }
    private fun groups(state: JSONObject) = state.optJSONObject("groups") ?: JSONObject().also { state.put("groups", it) }
    private fun bindings(state: JSONObject) = state.optJSONObject("bindings") ?: JSONObject().also { state.put("bindings", it) }
    private fun read(): JSONObject = runCatching {
        val file = File(root, "widgets.json")
        if (!file.isFile || file.length() > MAX_BYTES) return@runCatching JSONObject()
        JSONObject(file.readText()).takeIf { it.optInt("version") == 1 } ?: JSONObject()
    }.getOrElse { JSONObject() }
    private fun write(state: JSONObject) {
        state.put("version", 1)
        val bytes = state.toString().toByteArray(Charsets.UTF_8)
        require(bytes.size <= MAX_BYTES)
        val temp = File(root, "widgets.new")
        FileOutputStream(temp).use { it.write(bytes); it.fd.sync() }
        val target = File(root, "widgets.json").toPath()
        try {
            moveFile(temp.toPath(), target, arrayOf(StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE))
        } catch (_: AtomicMoveNotSupportedException) {
            moveFile(temp.toPath(), target, arrayOf(StandardCopyOption.REPLACE_EXISTING))
        }
    }
    private fun trim(state: JSONObject) {
        val groups = groups(state)
        while (state.toString().toByteArray(Charsets.UTF_8).size > MAX_BYTES - 1024) {
            val oldest = groups.keys().asSequence().mapNotNull { key ->
                val list = groups.optJSONObject(key)?.optJSONArray("views") ?: return@mapNotNull null
                (0 until list.length()).mapNotNull { index -> list.optJSONObject(index)?.let { Triple(key, index, it.optLong("saved")) } }
                    .minByOrNull { it.third }
            }.minByOrNull { it.third } ?: break
            groups.getJSONObject(oldest.first).getJSONArray("views").remove(oldest.second)
        }
    }
    private fun <T> access(action: () -> T): T = synchronized(lock) {
        RandomAccessFile(File(root, "widgets.lock"), "rw").channel.use { channel -> channel.lock().use { action() } }
    }
    companion object {
        private val locks = ConcurrentHashMap<String, Any>()
        private const val MAX_BYTES = 1_048_576
        private const val MAX_PROJECTION_BYTES = 262_144
        private const val MAX_VIEWS = 32
        private const val MAX_BINDINGS = 64
        private const val MAX_GROUPS = 4
        fun identity(config: HubConfig): String = digest(listOf(config.baseUrl, config.token, config.fingerprint.orEmpty(), config.deviceId.orEmpty()).joinToString("\u0000"))
        private fun digest(value: String) = MessageDigest.getInstance("SHA-256").digest(value.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }
        private fun requireGroup(group: String) { require(group.isEmpty() || group.matches(Regex("[a-f0-9-]{36}"))) }
        fun families(kind: WidgetFaceKind) = if (kind == WidgetFaceKind.SHORT) listOf("accessory_circular", "accessory_inline") else listOf("accessory_rectangular")
        fun familyFor(widget: ExtensionWidget, kind: WidgetFaceKind): String? = families(kind).firstOrNull { it in widget.families }
        private fun encodeWidget(widget: ExtensionWidget) = JSONObject().put("extension", widget.owner).put("id", widget.id).put("title", widget.title)
            .put("extension_name", widget.source).put("families", JSONArray(widget.families)).apply { widget.opens?.let { put("opens", it) } }
        private fun encodeView(view: ExtensionWidgetView): JSONObject {
            fun metric(metric: WidgetMetric) = JSONObject().put("label", metric.label).put("value", metric.value).put("detail", metric.detail)
            val items = view.items.map { value -> when (value) {
                is WidgetItem.Text -> JSONObject().put("type", "text").put("text", value.text)
                is WidgetItem.Metric -> metric(value.metric).put("type", "metric")
                is WidgetItem.Progress -> JSONObject().put("type", "progress").put("label", value.label).put("ratio", value.ratio).put("detail", value.detail)
                is WidgetItem.Row -> JSONObject().put("type", "row").put("title", value.title).put("subtitle", value.subtitle).put("value", value.value)
                is WidgetItem.Stats -> JSONObject().put("type", "stat_grid").put("stats", JSONArray(value.values.map(::metric)))
                is WidgetItem.Chart -> JSONObject().put("type", "chart").put("title", value.title).put("kind", value.kind).put("unit", value.unit)
                    .put("series", JSONArray(value.series.map { series -> JSONObject().put("name", series.name).put("points", JSONArray(series.points.map { point ->
                        JSONObject().put("label", point.label).put("value", point.value).put("display", point.display)
                            .put("open", point.open).put("high", point.high).put("low", point.low).put("close", point.close)
                    })) }))
            } }
            return JSONObject().put("expires_at", Instant.ofEpochMilli(view.expiresAtMs).toString())
                .put("view", JSONObject().put("title", view.title).put("sections", JSONArray().put(JSONObject().put("items", JSONArray(items)))))
        }
    }
}
