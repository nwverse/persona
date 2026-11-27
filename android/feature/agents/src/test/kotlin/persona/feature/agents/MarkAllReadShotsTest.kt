package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/** "Mark all as read" asks first and names how many it marks, as on iOS (#3045), in every style, light and dark. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h700dp-xhdpi")
class MarkAllReadShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun confirmsWithTheCount() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var marked = 0
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.width(400.dp).height(700.dp).background(PersonaTheme.palette.background)) {
                    MarkAllReadDialog(3, onDismiss = {}) { marked++ }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/parity-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onNodeWithText("把 3 个完成未看的会话标为已读？").assertExists()
            val bitmap = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
            File(out, "agents-mark-all-read-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        rule.onNodeWithText("全部标为已读（3）").performClick()
        assertEquals(1, marked)
    }
}
