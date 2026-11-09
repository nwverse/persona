package persona.core.designsystem

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcons

// The phone composer's grow-then-expand behaviour (ChatGPT's, #5688; iOS ShellComposerBar, web ComposerExpand):
// the field grows with the draft up to [composerGrowLines], then scrolls inside and shows [ComposerExpandButton] at
// its top right; the button opens [ExpandedComposer], a full-screen editor on the same draft and caret.

/** Share of the height left above the keyboard the field may grow into before it scrolls and offers Expand. */
private const val GROW_SHARE = 0.22f
private const val MIN_GROW_LINES = 3
private const val MAX_GROW_LINES = 8

/**
 * Lines the composer grows to: 22% of the [available] height (the window above the keyboard) in lines of
 * [lineHeight] (iOS ComposerGrowth's share and cap), so a small phone with its keyboard up gets about 3 and a tall screen more, between 3 and 8.
 */
fun composerGrowLines(available: Dp, lineHeight: Dp): Int {
    if (lineHeight.value <= 0f || available.value <= 0f) return MIN_GROW_LINES
    return (available.value * GROW_SHARE / lineHeight.value).toInt().coerceIn(MIN_GROW_LINES, MAX_GROW_LINES)
}

/**
 * Expand shows once the wrapped draft has more lines than the field shows. With the button the text wraps in a
 * narrower width (more lines), so the state it settles in never flips back by itself: it is stable both ways.
 */
fun composerOffersExpand(lineCount: Int, growLines: Int): Boolean = lineCount > growLines

/** [composerGrowLines] for this window now: its height less the keyboard, in lines of the body text. */
@Composable
fun rememberComposerGrowLines(): Int {
    val density = LocalDensity.current
    val windowHeight = LocalConfiguration.current.screenHeightDp.dp
    val keyboard = with(density) { WindowInsets.ime.getBottom(density).toDp() }
    val body = PersonaTheme.type.body
    val measurer = androidx.compose.ui.text.rememberTextMeasurer()
    val lineHeight = with(density) {
        if (body.lineHeight.isSp) body.lineHeight.toDp()
        else measurer.measure("Ag", style = body, maxLines = 1).size.height.toDp()
    }
    return remember(windowHeight, keyboard, lineHeight) { composerGrowLines(windowHeight - keyboard, lineHeight) }
}

/** Room the field's text leaves on its right for [ComposerExpandButton], only while the button shows. */
val ComposerExpandInset: Dp = 28.dp

/**
 * The small ↗↙ at the top right of the composer's text ([expanded] false: open the full editor) or the ↙↗ at the
 * top right of [ExpandedComposer] (collapse). Its tap area reaches into the field's padding, so it does not grow the field.
 */
@Composable
fun ComposerExpandButton(expanded: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val spoken = stringResource(if (expanded) R.string.designsystem_composer_collapse else R.string.designsystem_composer_expand)
    val size = if (expanded) 48.dp else 40.dp
    Box(
        modifier.then(if (expanded) Modifier else Modifier.offset(x = 10.dp, y = (-8).dp)).size(size).clip(CircleShape)
            .clickable(role = Role.Button, onClick = onClick).semantics { contentDescription = spoken }
            .testTag(if (expanded) "composer-collapse" else "composer-expand"),
        contentAlignment = Alignment.Center,
    ) {
        JIcon(if (expanded) PhIcons.ArrowsInSimple else PhIcons.ArrowsOutSimple, null,
            size = if (expanded) 22.dp else 18.dp, tint = PersonaTheme.palette.ink2)
    }
}

/**
 * The draft full screen: collapse at the top right, the text filling the page, [send] at the bottom right above
 * the keyboard. It edits the same [value] (text and caret) as the small field, so collapsing changes nothing.
 * Back collapses too.
 */
@Composable
fun ExpandedComposer(
    value: TextFieldValue,
    onValueChange: (TextFieldValue) -> Unit,
    placeholder: String,
    onCollapse: () -> Unit,
    enabled: Boolean = true,
    send: @Composable RowScope.() -> Unit,
) {
    val palette = PersonaTheme.palette
    val focus = remember { FocusRequester() }
    JFullScreen(onCollapse) {
        Column(Modifier.fillMaxSize().systemBarsPadding().imePadding().testTag("composer-expanded")) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 4.dp), horizontalArrangement = Arrangement.End) {
                ComposerExpandButton(expanded = true, onClick = onCollapse)
            }
            BasicTextField(
                value, onValueChange, Modifier.weight(1f).fillMaxWidth().padding(horizontal = 20.dp).focusRequester(focus),
                enabled = enabled, textStyle = PersonaTheme.type.body.copy(color = palette.ink), cursorBrush = SolidColor(palette.ink),
                decorationBox = { field ->
                    Box {
                        if (value.text.isEmpty()) Text(placeholder, style = PersonaTheme.type.body, color = palette.ink3)
                        field()
                    }
                },
            )
            Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp), horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically, content = send)
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
}
