package persona.feature.files

import org.xml.sax.Attributes
import org.xml.sax.helpers.DefaultHandler

/** A document that can't be read for comparing: what to say. */
internal class CompareFailure(val reason: Int) : Exception()

/**
 * A document's text cut into the units a person thinks in, for comparing two versions (web `lib/compare.ts` readers,
 * iOS `DocumentText`): a Word document by page (at its page breaks; a table row one line, its cells joined; deleted
 * runs left out), a deck by slide in the deck's order with its notes, a workbook by sheet. PDF is read on the device
 * (`PdfText`), not here.
 */
internal object DocumentText {
    fun units(ext: String, bytes: ByteArray, names: UnitNames): List<DocUnit> = try {
        when (ext.lowercase()) {
            "docx" -> docx(Xlsx.unzip(bytes, "word/"), names)
            "pptx" -> pptx(Xlsx.unzip(bytes, "ppt/"), names)
            // A row's cells joined like a table's (each row as wide as the sheet), so a changed cell shows on its own.
            else -> Xlsx.read(bytes).map { sheet ->
                DocUnit(names.sheet(sheet.name), sheet.rows.filter { row -> row.any(String::isNotBlank) }
                    .map { row -> (row + List(maxOf(0, sheet.columns - row.size)) { "" }).joinToString(DocumentCompare.ROW_SEP) })
            }
        }
    } catch (failure: SheetFailure) {
        throw CompareFailure(if (failure.reason == R.string.files_sheet_too_big) R.string.files_compare_too_big_file else R.string.files_compare_unreadable)
    }

    private fun docx(parts: Map<String, ByteArray>, names: UnitNames): List<DocUnit> {
        val xml = parts["word/document.xml"] ?: throw CompareFailure(R.string.files_compare_unreadable)
        val pages = mutableListOf(mutableListOf<String>())
        // A page break seen: the next line starts a page (Word's explicit break and the rendered one after it are one).
        var newPage = false
        fun add(line: String) {
            if (newPage && pages.last().isNotEmpty()) pages += mutableListOf<String>()
            newPage = false
            pages.last() += line
        }
        Xml.parse(xml, object : DefaultHandler() {
            var tables = 0
            var paragraphs = 0 // nested: a text box's paragraphs are inside their paragraph
            var cellParagraphs = 0 // separate counter inside table cells so paragraphs stay space-joined
            var runs = 0
            var skipped = 0 // inside w:del / w:instrText / mc:Fallback (a copy of the text box above it)
            val text = StringBuilder()
            var row: MutableList<String>? = null
            val cell = StringBuilder()
            val cellPara = StringBuilder()
            var inText = false

            fun here() = if (tables > 0) cellPara else text

            /** A page break inside a paragraph: what came before it stays on this page. */
            fun breakPage() {
                if (tables > 0 || skipped > 0) return
                if (text.isNotBlank()) add(text.toString().trimEnd())
                text.clear()
                newPage = true
            }

            fun flushCellParagraph() {
                val part = cellPara.toString().trim()
                if (part.isNotEmpty()) {
                    if (cell.isNotEmpty()) cell.append(' ')
                    cell.append(part)
                }
                cellPara.clear()
            }

            override fun startElement(uri: String?, localName: String?, qName: String, attributes: Attributes) {
                when (Xml.local(qName)) {
                    "tbl" -> tables++
                    "tr" -> if (tables == 1) row = mutableListOf()
                    "tc" -> if (tables == 1) { cell.clear(); cellPara.clear(); cellParagraphs = 0 }
                    "p" -> if (tables == 0) {
                        paragraphs++
                        if (paragraphs == 1) text.clear()
                    } else {
                        cellParagraphs++
                        if (cellParagraphs == 1) cellPara.clear()
                    }
                    "r" -> runs++
                    "del", "instrText", "Fallback" -> skipped++
                    "t" -> inText = skipped == 0
                    // w:tab in a run is a tab; in w:pPr/w:tabs it is a tab stop's definition.
                    "tab" -> if (skipped == 0 && runs > 0) here().append('\t')
                    "br" -> if (attributes.localValue("type") == "page") breakPage()
                    "lastRenderedPageBreak" -> breakPage()
                }
            }

            override fun characters(ch: CharArray, start: Int, length: Int) {
                if (inText) here().appendRange(ch, start, start + length)
            }

            override fun endElement(uri: String?, localName: String?, qName: String) {
                when (Xml.local(qName)) {
                    "t" -> inText = false
                    "r" -> if (runs > 0) runs--
                    "del", "instrText", "Fallback" -> if (skipped > 0) skipped--
                    "tc" -> if (tables == 1) {
                        flushCellParagraph()
                        row?.add(cell.toString().trim())
                    }
                    "tr" -> if (tables == 1) {
                        row?.takeIf { cells -> cells.any { it.isNotEmpty() } }?.let { add(it.joinToString(DocumentCompare.ROW_SEP)) }
                        row = null
                    }
                    "tbl" -> if (tables > 0) tables--
                    "p" -> if (tables == 0) {
                        if (paragraphs > 0) paragraphs--
                        if (paragraphs == 0 && text.isNotBlank()) add(text.toString().trimEnd())
                    } else {
                        if (cellParagraphs > 0) cellParagraphs--
                        if (cellParagraphs == 0) flushCellParagraph()
                    }
                }
            }
        })
        // Numbered once the empty pages are gone: a break with nothing after it doesn't skip a number.
        return pages.filterIndexed { i, lines -> lines.isNotEmpty() || i == 0 }.mapIndexed { i, lines -> DocUnit(names.page(i + 1), lines) }
    }

    private fun pptx(parts: Map<String, ByteArray>, names: UnitNames): List<DocUnit> {
        val presentation = parts["ppt/presentation.xml"] ?: throw CompareFailure(R.string.files_compare_unreadable)
        val targets = relationships(parts, "ppt/presentation.xml")
        val slides = Xml.elements(presentation, setOf("sldId")).mapNotNull { it["r:id"]?.let(targets::get)?.first }
        return slides.mapIndexed { i, path ->
            val lines = parts[path]?.let(::slideLines).orEmpty().toMutableList()
            val notes = relationships(parts, path).values.firstOrNull { (_, type) -> type.endsWith("/notesSlide") }?.first
            val noteLines = notes?.let(parts::get)?.let(::slideLines).orEmpty().map { it.trimStart() }
            if (noteLines.isNotEmpty()) lines += listOf(names.notes) + noteLines
            DocUnit(names.slide(i + 1), lines)
        }
    }

    /** OOXML `ST_TextIndentLevelType` restricts paragraph indentation levels to 0..8. */
    private const val MAX_INDENT_LEVEL = 8

    private fun Attributes.localValue(localName: String): String? =
        (0 until length).firstOrNull { Xml.local(getQName(it)) == localName }?.let(::getValue)

    /** A slide's (or its notes') text: each paragraph's lines, indented by level; a table row one line. */
    private fun slideLines(xml: ByteArray): List<String> {
        val out = ArrayList<String>()
        Xml.parse(xml, object : DefaultHandler() {
            val para = StringBuilder()
            var level = 0
            var inText = false
            var row: MutableList<String>? = null
            var cell: StringBuilder? = null

            override fun startElement(uri: String?, localName: String?, qName: String, attributes: Attributes) {
                when (Xml.local(qName)) {
                    "p" -> { para.clear(); level = 0 }
                    "pPr" -> level = (attributes.getValue("lvl")?.toIntOrNull() ?: 0).coerceIn(0, MAX_INDENT_LEVEL)
                    "t" -> inText = true
                    "br" -> para.append('\n')
                    "tr" -> row = mutableListOf()
                    "tc" -> cell = StringBuilder()
                }
            }

            override fun characters(ch: CharArray, start: Int, length: Int) {
                if (inText) para.appendRange(ch, start, start + length)
            }

            override fun endElement(uri: String?, localName: String?, qName: String) {
                when (Xml.local(qName)) {
                    "t" -> inText = false
                    "p" -> cell?.let { c -> if (para.isNotBlank()) { if (c.isNotEmpty()) c.append(' '); c.append(para.trim()) } }
                        ?: out.addAll(linesOf(para.toString()).map { "  ".repeat(level) + it })
                    "tc" -> { row?.add(cell?.toString().orEmpty()); cell = null }
                    "tr" -> { row?.takeIf { it.any(String::isNotEmpty) }?.let { out += it.joinToString(DocumentCompare.ROW_SEP) }; row = null }
                }
            }
        })
        return out
    }

    /** A part's relationships: id → (the target part's path, the relationship's type). */
    private fun relationships(parts: Map<String, ByteArray>, part: String): Map<String, Pair<String, String>> {
        val folder = part.substringBeforeLast('/', "")
        val rels = parts["$folder/_rels/${part.substringAfterLast('/')}.rels"] ?: return emptyMap()
        return Xml.elements(rels, setOf("Relationship")).mapNotNull { r ->
            val id = r["Id"] ?: return@mapNotNull null
            val target = r["Target"] ?: return@mapNotNull null
            id to (Xlsx.normalizePath(if (target.startsWith("/")) target.drop(1) else "$folder/$target") to r["Type"].orEmpty())
        }.toMap()
    }

    fun linesOf(text: String): List<String> = text.split(Regex("\r?\n")).map { it.trimEnd() }.filter { it.isNotBlank() }
}
