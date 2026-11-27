package persona.feature.files

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.SessionGitFile
import java.io.ByteArrayOutputStream
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

/** Names the tests read easily; the app's come from its string resources. */
private object Names : UnitNames {
    override fun page(n: Int) = "P$n"
    override fun slide(n: Int) = "S$n"
    override fun sheet(name: String) = "T:$name"
    override val notes = "[notes]"
}

private fun zip(vararg parts: Pair<String, String>): ByteArray {
    val out = ByteArrayOutputStream()
    ZipOutputStream(out).use { z -> parts.forEach { (name, xml) -> z.putNextEntry(ZipEntry(name)); z.write(xml.toByteArray()); z.closeEntry() } }
    return out.toByteArray()
}

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class DocumentCompareTest {
    @Test fun whichFilesAreCompared() {
        assertEquals(CompareKind.Image, DocumentCompare.kind("art/Icon.PNG"))
        assertEquals(CompareKind.Document, DocumentCompare.kind("docs/spec.docx"))
        assertEquals(CompareKind.Document, DocumentCompare.kind("a.pdf"))
        assertEquals(CompareKind.Document, DocumentCompare.kind("book.xlsx"))
        assertNull(DocumentCompare.kind("main.kt"))
        assertNull(DocumentCompare.kind("Makefile"))
    }

    @Test fun theVersionsOfAChange() {
        val unstaged = SessionGitFile("art/icon.png", index = ".", worktree = "M")
        assertEquals(listOf("art/icon.png", "INDEX", "WORKTREE"), compareChange(unstaged, staged = false).let { listOf(it.beforePath, it.before, it.after) })
        assertEquals(listOf("HEAD", "INDEX"), compareChange(unstaged, staged = true).let { listOf(it.before, it.after) })
        // Staged and changed again: the page offers both halves, as the text diff does.
        val both = SessionGitFile("art/icon.png", index = "M", worktree = "M")
        assertTrue(compareChange(both, staged = false).bothHalves)
        assertTrue(!compareChange(unstaged, staged = false).bothHalves)
        // A rename reads its old version under the old name; a file deleted in the tree has no new one.
        val renamed = SessionGitFile("art/new.png", originalPath = "art/old.png", index = "R")
        assertEquals("art/old.png", compareChange(renamed, staged = true).beforePath)
        assertTrue(compareChange(SessionGitFile("a.png", worktree = "D"), staged = false).deleted)
        assertEquals(listOf("abc123^", "abc123"), compareCommit("abc123", SessionGitFile("a.png", index = "M")).let { listOf(it.before, it.after) })
    }

    @Test fun aLineDiffLikeGits() {
        val ops = requireNotNull(DocumentCompare.diff(listOf("a", "b", "c", "d"), listOf("a", "c", "x", "d")))
        assertEquals(listOf("=a", "-b", "=c", "+x", "=d"), ops.map { it.kind.mark + it.text })
        assertNull(DocumentCompare.diff(List(10) { "a$it" }, List(10) { "b$it" }, maxEdits = 5))
        assertNull(DocumentCompare.diff(List(40) { "a" }, List(40) { "a" }, limit = 50))
    }

    @Test fun hunksKeepTwoLinesAroundAndSayWhereTheyAre() {
        val a = DocumentCompare.flatten(listOf(DocUnit("P1", (1..10).map { "line $it" }), DocUnit("P2", listOf("end"))))
        val b = DocumentCompare.flatten(listOf(DocUnit("P1", (1..10).map { if (it == 8) "changed" else "line $it" }), DocUnit("P2", listOf("end", "more"))))
        val hunks = DocumentCompare.hunks(requireNotNull(DocumentCompare.diff(a, b)))
        assertEquals(2, hunks.size)
        assertEquals("P1", hunks[0].unit)
        assertEquals(5, hunks[0].skippedBefore) // lines 1–5; the page's header is not a line of the document
        assertEquals(listOf("=line 6", "=line 7", "-line 8", "+changed", "=line 9", "=line 10"), hunks[0].ops.map { it.kind.mark + it.text })
        assertEquals("P2", hunks[1].unit)
        assertEquals(listOf("=end", "+more"), hunks[1].ops.map { it.kind.mark + it.text })
        assertTrue(DocumentCompare.hunks(requireNotNull(DocumentCompare.diff(a, a))).isEmpty())
    }

    @Test fun aChangeAtTheTopOfAPageSaysThatPage() {
        val a = DocumentCompare.flatten(listOf(DocUnit("P1", listOf("a", "b", "c", "d", "e")), DocUnit("P2", listOf("first", "x", "y", "z", "w"))))
        val b = DocumentCompare.flatten(listOf(DocUnit("P1", listOf("a", "b", "c", "d", "e")), DocUnit("P2", listOf("FIRST", "x", "y", "z", "w"))))
        assertEquals(listOf("P2"), DocumentCompare.hunks(requireNotNull(DocumentCompare.diff(a, b))).map { it.unit })
    }

    @Test fun aLongDiffStaysSmall() {
        // Thousands of changes come back as too many, not as a frontier per edit that fills the phone's memory.
        assertNull(DocumentCompare.diff(List(20_000) { "a$it" }, List(20_000) { "b$it" }))
    }

    @Test fun theCountsAreTheDocumentsLinesNotTheUnitHeaders() {
        val outcome = compareUnits(listOf(DocUnit("S1", listOf("a"))), listOf(DocUnit("S1", listOf("a")), DocUnit("S2", listOf("b", "c"))))
        assertEquals(2, (outcome as DocOutcome.Changes).added)
    }

    @Test fun aChangedTableRowShowsOnlyItsChangedCells() {
        val ops = requireNotNull(DocumentCompare.diff(listOf("x", "1 | 2 | 3"), listOf("x", "1 | 5 | 3", "new")))
        val lines = DocumentCompare.pairRows(ops)
        assertEquals(CompareLine.Same("x"), lines[0])
        assertEquals(CompareLine.Row(listOf("1" to "1", "2" to "5", "3" to "3")), lines[1])
        assertEquals(CompareLine.Added("new"), lines[2])
    }

    @Test fun aWordDocumentByPageWithItsTablesAndWithoutDeletedText() {
        val w = "xmlns:w=\"http://schemas.openxmlformats.org/wordprocessingml/2006/main\""
        val doc = zip("word/document.xml" to """<w:document $w><w:body>
            <w:p><w:r><w:t>Title</w:t></w:r></w:p>
            <w:p><w:r><w:t>kept </w:t></w:r><w:del><w:r><w:delText>gone</w:delText></w:r></w:del><w:ins><w:r><w:t>added</w:t></w:r></w:ins></w:p>
            <w:tbl><w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
            <w:p><w:pPr><w:tabs><w:tab w:val="left" w:pos="720"/></w:tabs></w:pPr><w:r><w:t>Before</w:t><w:br w:type="page"/><w:t>Second</w:t></w:r></w:p>
            <w:p><w:r><w:t xml:space="preserve">Intro </w:t></w:r><w:r><mc:AlternateContent xmlns:mc="m"><mc:Choice><w:txbxContent><w:p><w:r><w:t>Box</w:t></w:r></w:p></w:txbxContent></mc:Choice><mc:Fallback><w:txbxContent><w:p><w:r><w:t>Box</w:t></w:r></w:p></w:txbxContent></mc:Fallback></mc:AlternateContent></w:r><w:r><w:t>end</w:t></w:r></w:p>
            <w:p><w:r><w:br w:type="page"/></w:r></w:p>
            <w:p><w:r><w:lastRenderedPageBreak/><w:t>Third</w:t></w:r></w:p>
            </w:body></w:document>""")
        // A tab stop is not text; a text box's words count once, in its paragraph; a page break splits where it is, and
        // Word's explicit break followed by its rendered one is one new page.
        assertEquals(listOf(DocUnit("P1", listOf("Title", "kept added", "a | b", "Before")), DocUnit("P2", listOf("Second", "Intro Boxend")),
            DocUnit("P3", listOf("Third"))), DocumentText.units("docx", doc, Names))
    }

    @Test fun aDeckSlideBySlideInItsOrderWithNotes() {
        val p = "xmlns:p=\"http://schemas.openxmlformats.org/presentationml/2006/main\" xmlns:a=\"http://schemas.openxmlformats.org/drawingml/2006/main\" " +
            "xmlns:r=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships\""
        val rel = "xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\""
        fun slide(vararg paras: String) = "<p:sld $p><p:cSld><p:spTree><p:sp><p:txBody>" +
            paras.joinToString("") { "<a:p><a:r><a:t>$it</a:t></a:r></a:p>" } + "</p:txBody></p:sp></p:spTree></p:cSld></p:sld>"
        val deck = zip(
            "ppt/presentation.xml" to "<p:presentation $p><p:sldIdLst><p:sldId id=\"256\" r:id=\"rId2\"/><p:sldId id=\"257\" r:id=\"rId1\"/></p:sldIdLst></p:presentation>",
            "ppt/_rels/presentation.xml.rels" to "<Relationships $rel><Relationship Id=\"rId1\" Target=\"slides/slide1.xml\"/><Relationship Id=\"rId2\" Target=\"slides/slide2.xml\"/></Relationships>",
            "ppt/slides/slide1.xml" to slide("Later"),
            "ppt/slides/slide2.xml" to slide("First", "Point"),
            "ppt/slides/_rels/slide2.xml.rels" to "<Relationships $rel><Relationship Id=\"rId9\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide\" Target=\"../notesSlides/notesSlide1.xml\"/></Relationships>",
            "ppt/notesSlides/notesSlide1.xml" to "<p:notes $p><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Say hi</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:notes>",
        )
        assertEquals(listOf(DocUnit("S1", listOf("First", "Point", "[notes]", "Say hi")), DocUnit("S2", listOf("Later"))),
            DocumentText.units("pptx", deck, Names))
    }

    @Test fun pptxOutOfRangeIndentLevelsClampSafelyWithoutCrashOrTruncation() {
        val p = "xmlns:p=\"http://schemas.openxmlformats.org/presentationml/2006/main\" xmlns:a=\"http://schemas.openxmlformats.org/drawingml/2006/main\" " +
            "xmlns:r=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships\""
        val rel = "xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\""
        val slideXml = """<p:sld $p><p:cSld><p:spTree><p:sp><p:txBody>
            <a:p><a:pPr lvl="-3"/><a:r><a:t>Negative</a:t></a:r></a:p>
            <a:p><a:pPr lvl="2"/><a:r><a:t>Normal</a:t></a:r></a:p>
            <a:p><a:pPr lvl="2000000000"/><a:r><a:t>Huge</a:t></a:r></a:p>
            <a:p><a:r><a:t>After</a:t></a:r></a:p>
        </p:txBody></p:sp></p:spTree></p:cSld></p:sld>"""
        val deck = zip(
            "ppt/presentation.xml" to "<p:presentation $p><p:sldIdLst><p:sldId id=\"256\" r:id=\"rId1\"/></p:sldIdLst></p:presentation>",
            "ppt/_rels/presentation.xml.rels" to "<Relationships $rel><Relationship Id=\"rId1\" Target=\"slides/slide1.xml\"/></Relationships>",
            "ppt/slides/slide1.xml" to slideXml,
        )
        assertEquals(
            listOf(DocUnit("S1", listOf("Negative", "    Normal", "  ".repeat(8) + "Huge", "After"))),
            DocumentText.units("pptx", deck, Names),
        )
    }

    @Test fun docxTableCellParagraphsJoinWithSpacesAndHandleNonPrefixedPageBreak() {
        val doc = zip("word/document.xml" to """<document xmlns="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><body>
            <tbl><tr ><tc>
                <p><r><t>Line 1</t></r></p>
                <p/>
                <p><r><t>Line 2</t></r></p>
            </tc><tc>
                <p><r><t>Single</t></r></p>
            </tc></tr></tbl>
            <p><r><t>Page one</t><br type="page"/><t>Page two</t></r></p>
        </body></document>""")
        assertEquals(
            listOf(
                DocUnit("P1", listOf("Line 1 Line 2 | Single", "Page one")),
                DocUnit("P2", listOf("Page two")),
            ),
            DocumentText.units("docx", doc, Names),
        )
    }

    @Test fun aWorkbookSheetBySheet() {
        val units = DocumentText.units("xlsx", SheetsTest.workbook(), Names)
        assertTrue(units.isNotEmpty())
        assertTrue(units.all { it.label.startsWith("T:") })
        assertTrue(units.first().lines.isNotEmpty())
        // A row's cells are joined like a table's, so a changed cell is shown on its own.
        assertTrue(units.first().lines.all { DocumentCompare.ROW_SEP in it })
    }
}
