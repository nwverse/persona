package persona.feature.assistant

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.time.Instant

/** Synthetic tag rows and chips, in every style, light/dark and with default/large text; no hub or user data. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
class ConversationTagsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun chat(id: String, title: String, tags: List<String>, minutes: Long) = Session(
        machineId = "hub", sessionId = id, title = title, assistant = true, status = "idle", tags = tags,
        updatedAt = Instant.parse("2026-11-17T12:00:00Z").minusSeconds(minutes * 60).toString(),
    )

    @Test fun tagsInEveryStyleAppearanceAndTextSize() {
        val main = chat("main", "Main", listOf("Planning"), 0)
        val recent = listOf(
            chat("release", "Release plan", listOf("Planning", "Research"), 2),
            chat("trip", "Trip itinerary", listOf("Travel"), 8),
            chat("notes", "Research notes", listOf("Research"), 20),
        )
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var fontScale by mutableFloatStateOf(1f)
        rule.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, fontScale)) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.testTag("conversation-tags-shot").width(400.dp).height(1100.dp)
                        .background(PersonaTheme.palette.background)) {
                        AssistantDrawer("", {}, ConversationGroups(main, emptyList(), recent), active = null,
                            hasMain = true, onEnd = null, onMain = {}, onOpen = {}, onInfo = {}, onNew = {},
                            onSettings = {}, tagChoices = listOf("Planning", "Research", "Travel"))
                    }
                }
            }
        }
        rule.onNodeWithText("All chats (3)").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Release plan").assertExists()
        val standard = mutableMapOf<Pair<AppStyle, Boolean>, Bitmap>()
        for (s in AppStyle.entries) for (d in listOf(false, true)) for (scale in listOf(1f, 2f)) {
            style = s
            dark = d
            fontScale = scale
            Snapshot.sendApplyNotifications()
            rule.waitForIdle()
            rule.onNodeWithTag("conversation-tag-filter").assertExists()
            val suffix = if (scale > 1f) "-large" else ""
            val name = "android-conversation-tags-${s.name.lowercase()}-${if (d) "dark" else "light"}$suffix"
            val bitmap = rule.onNodeWithTag("conversation-tags-shot").captureToImage().asAndroidBitmap()
            if (scale == 1f) standard[s to d] = bitmap
            else assertFalse("large text must change the rendered pixels", standard.getValue(s to d).sameAs(bitmap))
            historyShot(bitmap, name)
        }
    }
}
