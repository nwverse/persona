package persona.feature.inbox

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.InboxEntry
import persona.core.contracts.api.ManagerRoster
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File
import java.time.Instant

/**
 * An offline assistant's inbox in English and Japanese (the mock hub's names, which are data) (the words every client shares, docs/assistant/managers.md
 * "Clients"), Glass light, into `build/managers-shots/inbox-offline-<language>.png`. Chinese and every style:
 * [InboxManagersShotsTest].
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class InboxOfflineWordsShotsTest {
    @get:Rule val rule = createComposeRule()
    @After fun resetChoice() = AssistantManagerChoice.reset()

    private val roster = ManagerRoster(listOf(
        HubManager("assistant", "助手", online = true, isDefault = true, builtin = true),
        HubManager("m-8b04d2", "家里", online = false, lastSeen = Instant.now().minusSeconds(3 * 3600).toString()),
    ), "assistant")

    private object Empty : InboxRepository {
        override val entries = MutableStateFlow(emptyList<InboxEntry>())
        override val snapshot = MutableStateFlow(InboxSnapshot())
        override val connectionState = flowOf(ConnectionState.Online)
        override suspend fun refresh() = Unit
        override fun conversation(feedId: String, startPosition: Long?) = flowOf(LarkConversation())
        override suspend fun loadOlder(feedId: String) = Unit
        override suspend fun refreshConversation(feedId: String) = Unit
        override suspend fun setDone(itemId: String, done: Boolean) = Unit
        override suspend fun readDraft(target: ReplyTarget): ReplyDraft? = null
        override suspend fun saveDraft(target: ReplyTarget, draft: ReplyDraft) = Unit
        override suspend fun reply(target: ReplyTarget, request: ReplyRequest) = Unit
        override suspend fun downloadFile(file: LarkPart.File): String = error("not used")
    }

    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private fun shoot(language: String, vararg words: String) {
        AssistantManagerChoice.choose("hub", "m-8b04d2", "assistant")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides roster) {
                    Box(Modifier.testTag("shot").width(400.dp).height(900.dp).background(PersonaTheme.palette.background)) {
                        InboxScreen(Empty, navigator)
                    }
                }
            }
        }
        rule.waitForIdle()
        words.forEach { assertTrue(it, rule.onAllNodesWithText(it).fetchSemanticsNodes().isNotEmpty()) }
        val dir = File(System.getProperty("persona.shots") ?: "build/managers-shots").also { it.mkdirs() }
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(dir, "inbox-offline-$language.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
    @Test fun english() = shoot("en", "家里 is offline", "Last synced 3 hours ago. This updates when it's back online.")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h900dp-xhdpi")
    @Test fun japanese() = shoot("ja", "家里 はオフラインです", "最終同期 3時間前。オンラインに戻ると更新されます。")
}
