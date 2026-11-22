package persona.feature.files

import androidx.annotation.StringRes
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import java.util.Locale

/**
 * Jupyter notebooks for the file viewer (docs/android/android.md "Files and Git in chat"; web `lib/notebook.ts`, iOS `Notebook.swift`):
 * nbformat 4 (and 3's worksheets) into cells, each output reduced to the one representation the viewer shows. Pure.
 */
internal object Notebooks {
    /** Lines of output kept per text output; beyond that the viewer says it cut them. */
    const val MAX_OUTPUT_LINES = 2000

    fun isNotebook(name: String) = FilesPaths.extension(name) == "ipynb"

    sealed interface Output {
        data class Stream(val stderr: Boolean, val text: String) : Output
        /** A picture: [base64] for PNG / JPEG / GIF / WebP; SVG isn't drawn, its text alternative shows instead. */
        data class Image(val mime: String, val base64: String) : Output
        /** HTML (a pandas table, a widget's fallback): shown as its text, tags removed. */
        data class Html(val text: String) : Output
        data class Markdown(val text: String) : Output
        data class Text(val text: String) : Output
        data class Error(val name: String, val value: String, val traceback: String) : Output
    }

    enum class CellType { Code, Markdown, Raw }

    data class Cell(val type: CellType, val source: String, val count: Int? = null, val outputs: List<Output> = emptyList(),
        /** Markdown cells: pictures pasted into the cell, by name, as base64 PNG / JPEG. */
        val attachments: Map<String, String> = emptyMap())

    data class Notebook(val cells: List<Cell>, val language: String, val kernel: String?, val format: String)

    class Invalid(@StringRes val reason: Int) : Exception()

    private val json = Json { ignoreUnknownKeys = true }

    /** Removes terminal color codes (tracebacks are full of them). */
    fun stripAnsi(s: String): String = s.replace(csi, "").replace(osc, "")

    private val csi = Regex("\u001b\\[[0-9;?]*[ -/]*[@-~]")
    private val osc = Regex("\u001b\\][^\u0007]*\u0007")

    /** Text cut to [max] lines, saying how many lines were left out ([more]: the line that says so, for a count). */
    fun capLines(s: String, more: (Int) -> String, max: Int = MAX_OUTPUT_LINES): String {
        var i = -1
        repeat(max) {
            i = s.indexOf('\n', i + 1)
            if (i < 0) return s
        }
        val rest = s.substring(i + 1).removeSuffix("\n").split('\n').size
        return s.substring(0, i + 1) + more(rest)
    }

    /** HTML as the text it shows: tags dropped, block ends as line breaks, table cells apart, entities decoded. */
    fun htmlText(html: String): String {
        val noScript = html.replace(Regex("(?is)<(script|style)\\b.*?</\\1>"), "")
        val breaks = noScript.replace(Regex("(?i)<br\\s*/?>|</(p|div|tr|li|h[1-6]|thead|tbody|table)>"), "\n")
            .replace(Regex("(?i)</t[dh]>"), "\t")
        val text = breaks.replace(Regex("<[^>]*>"), "")
            .replace(numericEntity) { m ->
                val code = m.groupValues[1].let { if (it.startsWith("x", true)) it.drop(1).toIntOrNull(16) else it.toIntOrNull() }
                code?.takeIf { Character.isValidCodePoint(it) }?.let { String(Character.toChars(it)) } ?: m.value
            }
            .replace("&nbsp;", " ").replace("&lt;", "<").replace("&gt;", ">").replace("&quot;", "\"").replace("&apos;", "'").replace("&amp;", "&")
        val lines = text.lines().map { it.trimEnd() }.joinToString("\n").replace(Regex("\n{3,}"), "\n\n").trim('\n').lines()
        return alignColumns(lines).joinToString("\n").trim('\n')
    }

    private val numericEntity = Regex("&#([xX]?[0-9a-fA-F]+);")

    /** Rows of tab-separated cells (an HTML table's text) padded into columns for the mono face; other lines as they are. */
    private fun alignColumns(lines: List<String>): List<String> {
        val rows = lines.map { it.split('\t').map(String::trim) }
        val widths = IntArray(rows.maxOf { it.size })
        rows.filter { it.size > 1 }.forEach { row -> row.forEachIndexed { i, cell -> widths[i] = maxOf(widths[i], textCells(cell)) } }
        return rows.map { row ->
            if (row.size == 1) row[0]
            else row.mapIndexed { i, cell -> if (i == row.lastIndex) cell else cell + " ".repeat(widths[i] - textCells(cell) + 2) }.joinToString("").trimEnd()
        }
    }

    private fun str(v: JsonElement?): String = when (v) {
        is JsonArray -> v.joinToString("") { (it as? JsonPrimitive)?.takeIf { p -> p.isString }?.content.orEmpty() }
        is JsonPrimitive -> if (v.isString) v.content else ""
        else -> ""
    }

    private val images = listOf("image/png", "image/jpeg", "image/gif", "image/webp")

    /** The one representation of a rich output the viewer shows, best first (as Jupyter's own front ends choose). */
    private fun rich(data: JsonObject, more: (Int) -> String): Output? {
        for (mime in images) data[mime]?.let { return Output.Image(mime, str(it).replace(Regex("\\s+"), "")) }
        data["text/html"]?.let { return Output.Html(capLines(htmlText(str(it)), more)) }
        data["text/markdown"]?.let { return Output.Markdown(str(it)) }
        data["text/latex"]?.let { v ->
            val tex = str(v).trim()
            return Output.Markdown(if (tex.startsWith("$")) tex else "$$\n$tex\n$$")
        }
        data["application/json"]?.let { return Output.Text(capLines(prettyJson.encodeToString(JsonElement.serializer(), it), more)) }
        data["text/plain"]?.let { return Output.Text(capLines(stripAnsi(str(it)), more)) }
        return null
    }

    private val prettyJson = Json { prettyPrint = true }

    /** nbformat 3 keeps a rich output's representations on the output itself, under short names (png, html, text…). */
    private val v3Keys = mapOf("png" to "image/png", "jpeg" to "image/jpeg", "gif" to "image/gif", "html" to "text/html",
        "markdown" to "text/markdown", "latex" to "text/latex", "json" to "application/json", "text" to "text/plain")

    private fun v3Bundle(o: JsonObject) = JsonObject(o.mapNotNull { (k, v) -> (v3Keys[k] ?: k.takeIf { '/' in it })?.let { it to v } }.toMap())

    private fun output(o: JsonObject, more: (Int) -> String): Output? = when (str(o["output_type"])) {
        // v3 names the stream "stream", v4 "name".
        "stream" -> Output.Stream(str(o["name"] ?: o["stream"]) == "stderr", capLines(stripAnsi(str(o["text"])), more))
        "execute_result", "display_data" -> rich(o["data"] as? JsonObject ?: v3Bundle(o), more)
        "pyout" -> rich(v3Bundle(o), more)
        "error", "pyerr" -> Output.Error(str(o["ename"]), str(o["evalue"]),
            // whole: a traceback cut short loses the line that failed
            stripAnsi((o["traceback"] as? JsonArray)?.joinToString("\n") { str(it) } ?: str(o["traceback"])))
        else -> null
    }

    /** Consecutive stream outputs of the same name, as Jupyter shows them: one block. */
    private fun mergeStreams(out: List<Output>): List<Output> {
        val merged = ArrayList<Output>()
        for (o in out) {
            val last = merged.lastOrNull()
            if (o is Output.Stream && last is Output.Stream && last.stderr == o.stderr) merged[merged.lastIndex] = last.copy(text = last.text + o.text)
            else merged.add(o)
        }
        return merged
    }

    /**
     * A notebook's JSON into cells; throws [Invalid] with a readable reason for what is not one. [more] says how many
     * lines of a long output were left out ("… 3 more lines not shown").
     */
    fun parse(text: String, more: (Int) -> String): Notebook {
        val nb = try {
            json.parseToJsonElement(text)
        } catch (e: Exception) {
            throw Invalid(R.string.files_notebook_invalid_json)
        } as? JsonObject ?: throw Invalid(R.string.files_notebook_invalid)
        val meta = nb["metadata"] as? JsonObject ?: JsonObject(emptyMap())
        val kernelspec = meta["kernelspec"] as? JsonObject ?: JsonObject(emptyMap())
        val langInfo = meta["language_info"] as? JsonObject ?: JsonObject(emptyMap())
        val language = (str(kernelspec["language"]).ifEmpty { null } ?: str(langInfo["name"]).ifEmpty { null } ?: str(meta["language"]).ifEmpty { "python" })
            .lowercase(Locale.ROOT)
        val raw = when {
            nb["cells"] is JsonArray -> nb["cells"] as JsonArray
            nb["worksheets"] is JsonArray -> ((nb["worksheets"] as JsonArray).firstOrNull() as? JsonObject)?.get("cells") as? JsonArray ?: JsonArray(emptyList())
            else -> throw Invalid(R.string.files_notebook_no_cells)
        }
        val cells = raw.mapNotNull { it as? JsonObject }.map { c ->
            val kind = str(c["cell_type"])
            val type = when (kind) { "code" -> CellType.Code; "markdown", "heading" -> CellType.Markdown; else -> CellType.Raw }
            var source = str(c["source"] ?: c["input"])
            if (kind == "heading") source = "#".repeat(((c["level"] as? JsonPrimitive)?.intOrNull ?: 1).coerceIn(1, 6)) + " " + source
            val attachments = (c["attachments"] as? JsonObject).orEmpty().mapNotNull { (name, bundle) ->
                ((bundle as? JsonObject)?.let { b -> rich(b, more) } as? Output.Image)?.let { name to it.base64 }
            }.toMap()
            val outputs = if (type == CellType.Code) mergeStreams((c["outputs"] as? JsonArray).orEmpty().mapNotNull { (it as? JsonObject)?.let { o -> output(o, more) } }) else emptyList()
            val count = ((c["execution_count"] ?: c["prompt_number"]) as? JsonPrimitive)?.intOrNull
            Cell(type, source, count, outputs, attachments)
        }
        val format = "${(nb["nbformat"] as? JsonPrimitive)?.contentOrNull ?: "?"}.${(nb["nbformat_minor"] as? JsonPrimitive)?.contentOrNull ?: "0"}"
        return Notebook(cells, language, str(kernelspec["display_name"]).ifEmpty { null }, format)
    }
}
