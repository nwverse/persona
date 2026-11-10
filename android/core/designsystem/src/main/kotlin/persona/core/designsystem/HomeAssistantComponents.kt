package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color

internal fun boxesHomeAnswer(boxed: Boolean, style: AppStyle, home: HomeChrome?): Boolean =
    boxed && style == AppStyle.Glass && home != null && home.items.firstOrNull()?.key == home.selected

internal fun homeAnswerFill(palette: PersonaPalette): Color =
    if (palette.customRoles) palette.surfaceOpaque
    else if (palette.dark) HomeTokens.personaAnswerDark else HomeTokens.personaAnswerLight

internal fun homeUserFill(palette: PersonaPalette, home: HomeChrome?): Color {
    val standard = if (palette.dark) Tokens.Semantic.Glass.Dark.userBubble else Tokens.Semantic.Glass.Light.userBubble
    if (!boxesHomeAnswer(true, AppStyle.Glass, home) || palette.userBubble != standard) return palette.userBubble
    return if (palette.dark) HomeTokens.userMessageDark else HomeTokens.userMessageLight
}

/** Final home answers match iPhone's neutral message box; intermediate work narration stays unboxed. */
@Composable
fun AssistantAnswerFrame(boxed: Boolean, content: @Composable () -> Unit) {
    if (!boxesHomeAnswer(boxed, PersonaTheme.style, LocalHomeChrome.current)) {
        content()
        return
    }
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(Tokens.Radius.bubble))
        .background(homeAnswerFill(PersonaTheme.palette)).padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.m)) {
        content()
    }
}
