package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.CollectionInfo
import androidx.compose.ui.semantics.CollectionItemInfo
import androidx.compose.ui.semantics.collectionInfo
import androidx.compose.ui.semantics.collectionItemInfo
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import com.mikepenz.markdown.annotator.AnnotatorSettings
import com.mikepenz.markdown.annotator.annotatorSettings
import com.mikepenz.markdown.annotator.buildMarkdownAnnotatedString
import com.mikepenz.markdown.compose.LocalMarkdownColors
import com.mikepenz.markdown.compose.LocalMarkdownDimens
import com.mikepenz.markdown.compose.elements.MarkdownDivider
import com.mikepenz.markdown.compose.elements.MarkdownTable
import com.mikepenz.markdown.compose.elements.MarkdownTableBasicText
import org.intellij.markdown.MarkdownElementTypes
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.ast.findChildOfType
import org.intellij.markdown.flavours.gfm.GFMElementTypes.HEADER
import org.intellij.markdown.flavours.gfm.GFMElementTypes.ROW
import org.intellij.markdown.flavours.gfm.GFMTokenTypes.CELL
import org.intellij.markdown.flavours.gfm.GFMTokenTypes.TABLE_SEPARATOR
import kotlin.math.floor
import kotlin.math.min

/** The widths a table's columns draw at, and whether its content needs horizontal scrolling. */
internal data class MarkdownTablePlan(
    val widths: List<Int>,
    val scrolls: Boolean,
    /** The upstream renderer shows every cell without clipping and keeps its exact existing layout. */
    val fits: Boolean,
) {
    companion object {
        /** Natural widths include each cell's horizontal padding; all values are integer pixels. */
        fun fit(
            natural: List<Int>,
            available: Int,
            minimum: Int,
            maximum: Int,
            legacyColumnWidth: Int = minimum,
            legacyNatural: List<Int> = natural,
        ): MarkdownTablePlan {
            require(available >= 0)
            require(minimum >= 0)
            require(maximum >= minimum)
            require(legacyColumnWidth >= 0)
            require(natural.all { it >= 0 })
            require(legacyNatural.size == natural.size && legacyNatural.all { it >= 0 })
            if (natural.isEmpty()) return MarkdownTablePlan(emptyList(), scrolls = false, fits = true)
            val minimumTotal = minimum.toLong() * natural.size
            val legacyTotal = legacyColumnWidth.toLong() * natural.size
            // A whole column stays visible at the end unless the readable minimum itself needs more room.
            val columnMaximum = maxOf(minimum, min(maximum, available))
            val equalShare = available / natural.size
            // Upstream gives fitting tables equal columns and one-line cells. Keep that path only when no cell clips
            // and its fixed-width table has no positive scroll range; the scaled custom minimum is irrelevant here.
            if (legacyTotal <= available && legacyNatural.all { it <= equalShare }) {
                return MarkdownTablePlan(natural, scrolls = false, fits = true)
            }
            val content = natural.map { it.coerceAtLeast(minimum) }
            if (minimumTotal > available) {
                return MarkdownTablePlan(content.map { min(it, columnMaximum) }, scrolls = true, fits = false)
            }
            val widths = waterFill(content, available, minimum).map { min(it, columnMaximum) }
            return MarkdownTablePlan(widths, scrolls = widths.sum() > available, fits = false)
        }

        /** Short columns keep their content width; long columns share the remaining room without going below minimum. */
        private fun waterFill(natural: List<Int>, available: Int, minimum: Int): List<Int> {
            if (natural.sum() <= available) return natural
            val widths = IntArray(natural.size)
            val open = natural.indices.toMutableSet()
            var room = available
            var settled = true
            while (settled && open.isNotEmpty()) {
                settled = false
                val share = room.toDouble() / open.size
                for (column in open.sorted()) {
                    if (natural[column] <= share) {
                        widths[column] = natural[column]
                        room -= natural[column]
                        open.remove(column)
                        settled = true
                    }
                }
            }
            while (open.isNotEmpty()) {
                val total = open.sumOf { natural[it].toLong() }
                var pinned = false
                for (column in open.sorted()) {
                    if (room.toDouble() * natural[column] / total < minimum) {
                        widths[column] = minimum
                        room -= minimum
                        open.remove(column)
                        pinned = true
                        break
                    }
                }
                if (pinned) continue
                for (column in open) widths[column] = floor(room.toDouble() * natural[column] / total).toInt()
                open.clear()
            }
            return widths.toList()
        }
    }
}

/**
 * A Markdown table whose overflowing cells wrap at readable, content-derived widths. Fitting tables delegate to
 * Mikepenz unchanged. Set [enabled] false for static share pictures, which must retain the upstream full-table drawing.
 */
@Composable
fun ReadableMarkdownTable(content: String, node: ASTNode, style: TextStyle, enabled: Boolean = true) {
    if (!enabled) {
        MarkdownTable(content, node, style)
        return
    }
    val dimens = LocalMarkdownDimens.current
    val colors = LocalMarkdownColors.current
    val density = LocalDensity.current
    val annotation = annotatorSettings()
    val scrollState = rememberScrollState()
    val header = node.findChildOfType(HEADER)
    val columnCount = header?.children?.count { it.type == CELL } ?: 0
    val source = content.substring(node.startOffset, node.endOffset)
    if (columnCount == 0) {
        MarkdownTable(content, node, style, annotatorSettings = annotation)
        return
    }
    BoxWithConstraints(Modifier.widthIn(max = dimens.tableMaxWidth)) {
        if (!constraints.hasBoundedWidth) {
            MarkdownTable(content, node, style, annotatorSettings = annotation)
            return@BoxWithConstraints
        }
        val cellPaddingPx = with(density) { dimens.tableCellPadding.roundToPx() }
        val minimumPx = with(density) { (RichTextTokens.tableColumnMinimum * fontScale).roundToPx() }
        val maximumPx = with(density) { (RichTextTokens.tableColumnMaximum * fontScale).roundToPx() }
        val headerStyle = style.copy(fontWeight = FontWeight.Bold)
        val textMeasurer = rememberTextMeasurer()
        val measured = remember(source, style, headerStyle, density, cellPaddingPx, textMeasurer,
            annotation.linkTextSpanStyle, annotation.codeSpanStyle, annotation.highlightSpanStyle, annotation.annotator) {
            measureColumns(content, node, columnCount, style, headerStyle, annotation, textMeasurer, cellPaddingPx)
        }
        val availablePx = with(density) { maxWidth.roundToPx() }.coerceAtLeast(0)
        val legacyColumnWidthPx = with(density) { dimens.tableCellWidth.roundToPx() }
        val plan = remember(measured, availablePx, minimumPx, maximumPx, legacyColumnWidthPx) {
            MarkdownTablePlan.fit(measured.natural, availablePx, minimumPx, maximumPx, legacyColumnWidthPx,
                measured.legacyNatural)
        }
        if (plan.fits) {
            MarkdownTable(content, node, style, annotatorSettings = annotation)
        } else {
            val totalWidth = with(density) { plan.widths.sum().toDp() }
            val columnWidths = plan.widths.map { with(density) { it.toDp() } }
            val rows = node.children.count { it.type == ROW } + 1
            Column(
                Modifier
                    .background(colors.tableBackground, RoundedCornerShape(dimens.tableCornerSize))
                    .semantics { collectionInfo = CollectionInfo(rowCount = rows, columnCount = columnCount) },
            ) {
                Column(
                    (if (plan.scrolls) Modifier.horizontalScroll(scrollState) else Modifier)
                        .requiredWidth(totalWidth),
                ) {
                    var rowIndex = 1
                    node.children.forEach { row ->
                        when (row.type) {
                            HEADER -> TableRow(content, row, rowIndex = 0, totalWidth, columnWidths,
                                headerStyle, dimens.tableCellPadding, annotation)
                            ROW -> {
                                TableRow(content, row, rowIndex, totalWidth, columnWidths,
                                    style, dimens.tableCellPadding, annotation)
                                rowIndex++
                            }
                            TABLE_SEPARATOR -> MarkdownDivider()
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun TableRow(
    content: String,
    row: ASTNode,
    rowIndex: Int,
    totalWidth: Dp,
    columnWidths: List<Dp>,
    style: TextStyle,
    cellPadding: Dp,
    annotation: AnnotatorSettings,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.requiredWidth(totalWidth).then(if (rowIndex == 0) Modifier.height(IntrinsicSize.Max) else Modifier),
    ) {
        row.children.filter { it.type == CELL }.forEachIndexed { column, cell ->
            val width = columnWidths.getOrElse(column) { Dp(0f) }
            Column(
                Modifier
                    .requiredWidth(width)
                    .padding(cellPadding)
                    .semantics {
                        if (rowIndex == 0) heading()
                        collectionItemInfo = CollectionItemInfo(
                            rowIndex = rowIndex, rowSpan = 1, columnIndex = column, columnSpan = 1,
                        )
                    },
            ) {
                MarkdownTableBasicText(
                    content = content,
                    cell = cell,
                    style = style,
                    maxLines = Int.MAX_VALUE,
                    overflow = TextOverflow.Clip,
                    annotatorSettings = annotation,
                )
            }
        }
    }
}

private data class MeasuredColumns(val natural: List<Int>, val legacyNatural: List<Int>)

private fun measureColumns(
    content: String,
    node: ASTNode,
    columnCount: Int,
    bodyStyle: TextStyle,
    headerStyle: TextStyle,
    annotation: AnnotatorSettings,
    textMeasurer: androidx.compose.ui.text.TextMeasurer,
    cellPaddingPx: Int,
): MeasuredColumns {
    val widths = IntArray(columnCount)
    val legacyWidths = IntArray(columnCount)
    fun measure(cell: ASTNode, column: Int, style: TextStyle) {
        if (column >= columnCount) return
        val text = buildAnnotatedString {
            pushStyle(style.toSpanStyle())
            buildMarkdownAnnotatedString(content, cell, annotation)
            pop()
        }
        val textWidth = textMeasurer.measure(
            text = text,
            style = style,
            maxLines = 1,
            softWrap = false,
            overflow = TextOverflow.Clip,
        ).size.width
        val measuredWidth = textWidth + cellPaddingPx * 2
        widths[column] = maxOf(widths[column], measuredWidth)
        // Inline image source text is not the resolved image's drawn width; it must not change a fitting upstream table.
        val legacyWidth = if (cell.isImageOnly(content)) cellPaddingPx * 2 else measuredWidth
        legacyWidths[column] = maxOf(legacyWidths[column], legacyWidth)
    }
    node.children.forEach { row ->
        val style = if (row.type == HEADER) headerStyle else bodyStyle
        if (row.type == HEADER || row.type == ROW) {
            row.children.filter { it.type == CELL }.forEachIndexed { column, cell -> measure(cell, column, style) }
        }
    }
    return MeasuredColumns(widths.toList(), legacyWidths.toList())
}

private fun ASTNode.isImageOnly(content: String): Boolean {
    var foundImage = false
    fun onlyImages(node: ASTNode): Boolean = when {
        node.type == MarkdownElementTypes.IMAGE -> {
            foundImage = true
            true
        }
        node.children.isNotEmpty() -> node.children.all(::onlyImages)
        else -> content.substring(node.startOffset, node.endOffset).isBlank()
    }
    return onlyImages(this) && foundImage
}
