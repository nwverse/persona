package persona.feature.inbox

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.InboxEventFields
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.domain.inbox.InboxItem
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/**
 * Collapsed non-event chat and mail details in every style, light and dark, at default and 200% text. The source page
 * is not mounted, so its original body stays absent until View original is chosen. PNGs go to
 * `build/inbox-source-collapsed-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
class InboxSourceCollapsedShotsTest {
    @get:Rule val rule = createComposeRule()

    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private val chat = InboxRecord(
        InboxItem(
            "chat:planning:42", account = "chat", kind = "group", title = "Friday launch review", sender = "Avery",
            preview = "The checklist is ready for a final look.", summary = "Avery asks for a final launch review before Friday.",
            tier = "action", status = "action", updatedAt = "2026-11-13T10:00:00Z", where = "Planning room",
        ),
        source = "lark", sourceLabel = "Team chat", feedId = "planning-room", startPosition = 42,
        isGroup = true, groupName = "Planning room", event = InboxEventFields.None,
    )
    private val mail = InboxRecord(
        InboxItem(
            "mail:personal:88", account = "mail", kind = "mail", title = "Budget approval", sender = "Blake",
            preview = "Please review the attached totals.", summary = "Blake asks for approval of the revised project budget.",
            tier = "action", status = "action", updatedAt = "2026-11-13T11:00:00Z", where = "Project budget",
        ),
        source = "mail", sourceLabel = "Work mail", mail = InboxMailRef("personal", uids = listOf(88)),
        event = InboxEventFields.None,
    )

    private fun out(): File =
        File(System.getProperty("persona.shots") ?: "build/inbox-source-collapsed-shots").also { it.mkdirs() }

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val background = bitmap.getPixel(1, 1)
        var other = 0
        for (y in 0 until bitmap.height step 7) {
            for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) other++
        }
        assertTrue("$name drew nothing", other > 100)
        File(out(), "$name.png").outputStream().use {
            assertTrue("$name could not be written", bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
        }
    }

    @Test fun nonEventChatAndMailStayCollapsedInEveryAppearance() {
        val chatBody = "Full original chat body with every launch checklist reply."
        val mailBody = "Full original mail body with the revised totals and quoted thread."
        val fixtures = listOf(
            Triple("chat", chat, chatBody),
            Triple("mail", mail, mailBody),
        )
        assertFalse(chat.event.present)
        assertFalse(mail.event.present)
        val repository = FakeInboxRepository()
        repository.snapshot.value = InboxSnapshot(records = listOf(chat, mail))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var fontScale by mutableStateOf(1f)
        var shown by mutableStateOf(chat.item.id)
        var run by mutableIntStateOf(0)
        rule.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    key(run) {
                        Box(Modifier.testTag("shot").width(400.dp).height(1200.dp).background(PersonaTheme.palette.background)) {
                            InboxEventScreen(repository, InboxEventRoute(shown), navigator)
                        }
                    }
                }
            }
        }

        for ((kind, record, originalBody) in fixtures) {
            for (appearance in AppStyle.entries) for (night in listOf(false, true)) {
                for ((textSize, scale) in listOf("default" to 1f, "text-200" to 2f)) {
                    rule.runOnIdle {
                        shown = record.item.id
                        style = appearance
                        dark = night
                        fontScale = scale
                        run++
                    }
                    rule.waitForIdle()
                    rule.onNodeWithText(record.item.title).assertExists()
                    rule.onNodeWithText(record.item.summary!!).assertExists()
                    rule.onNode(hasText("View original") and hasClickAction()).assertExists()
                    rule.onNodeWithText(originalBody, substring = true, useUnmergedTree = true).assertDoesNotExist()
                    capture(
                        "inbox-source-collapsed-$kind-${appearance.name.lowercase()}-${if (night) "dark" else "light"}-$textSize",
                    )
                }
            }
        }
    }
}
