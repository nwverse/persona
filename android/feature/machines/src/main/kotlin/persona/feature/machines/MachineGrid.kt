package persona.feature.machines

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.Dp
import persona.core.designsystem.Tokens
import persona.core.designsystem.pagePadding
import persona.core.designsystem.readingInset
import kotlin.math.floor

/** Measures the actual pane, then mirrors the horizontal content padding JListPage will apply. */
@Composable
internal fun MachineGridWidth(content: @Composable (Dp) -> Unit) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val horizontalPadding = pagePadding + readingInset(maxWidth)
        val contentWidth = (maxWidth - horizontalPadding * 2).coerceAtLeast(Tokens.Space.flush)
        content(contentWidth)
    }
}

internal fun machineGridColumns(
    contentWidth: Dp,
    cardColumn: Dp = Tokens.Size.cardColumn,
    spacing: Dp = Tokens.Space.m,
): Int {
    if (!contentWidth.value.isFinite() || contentWidth <= Tokens.Space.flush) return 1
    return floor((contentWidth.value + spacing.value) / (cardColumn.value + spacing.value)).toInt().coerceAtLeast(1)
}

internal fun <T> machineGridRows(ids: List<T>, columns: Int): List<List<T>> = ids.chunked(columns.coerceAtLeast(1))

internal fun machineGridRowKey(row: List<String>): String =
    row.joinToString("|", prefix = "machine-row|") { "${it.length}:$it" }

/** Adds rows to the page's existing LazyColumn; no nested grid or second scrolling container. */
internal fun LazyListScope.machineGrid(
    ids: List<String>,
    contentWidth: Dp,
    card: @Composable (String, Modifier) -> Unit,
) {
    val columns = machineGridColumns(contentWidth)
    items(machineGridRows(ids, columns), key = ::machineGridRowKey, contentType = { "machine-row" }) { row ->
        MachineGridRow(row, columns, card = card)
    }
}

@Composable
internal fun MachineGridRow(
    row: List<String>,
    columns: Int,
    modifier: Modifier = Modifier,
    card: @Composable (String, Modifier) -> Unit,
) {
    Row(modifier.fillMaxWidth().height(IntrinsicSize.Max), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        row.forEach { id ->
            Box(Modifier.weight(1f).fillMaxHeight()) {
                card(id, Modifier.fillMaxHeight())
            }
        }
        repeat((columns - row.size).coerceAtLeast(0)) {
            Spacer(Modifier.weight(1f))
        }
    }
}
