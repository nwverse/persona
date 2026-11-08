package persona.platform.capsule

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/**
 * The floating capsule in each language (docs/protocol/i18n.md): the collapsed orb, the main chat, a section's list, an empty
 * list and the completion notice, Glass light and dark. PNGs go to `build/i18n-shots/` (or `-Dpersona.shots=<dir>`).
 * The sections' names and entries come from the app; here they are written per language.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class CapsuleShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    private class Words(val unread: String, val section: String, val main: String, val inbox: String, val task: String, val preview: String,
        val reply: String, val send: String, val openChat: String, val collapse: String, val back: String, val done: String)

    @Config(sdk = [35], qualifiers = "zh-w411dp-h900dp-xhdpi")
    @Test fun simplifiedChinese() = shots("zh", Words("未读", "主对话", "主对话", "收件箱", "补齐网络层的单元测试", "测试都过了，等你看一眼",
        "好的，我来看看。", "发送", "打开会话", "收起", "返回列表", "· 已完成"))

    @Config(sdk = [35], qualifiers = "en-w411dp-h900dp-xhdpi")
    @Test fun english() = shots("en", Words("Unread", "Main chat", "Main chat", "Inbox", "Add network layer unit tests",
        "All tests pass, take a look", "Sure, I'll take a look.", "Send", "Open chat", "Collapse", "Back to list", "· Done"))

    @Config(sdk = [35], qualifiers = "ja-w411dp-h900dp-xhdpi")
    @Test fun japanese() = shots("ja", Words("未読", "メイン", "メインチャット", "受信トレイ", "ネットワーク層の単体テストを追加",
        "テストはすべて通りました", "了解です。確認します。", "送信", "チャットを開く", "閉じる", "一覧に戻る", "· 完了"))

    private fun shots(language: String, words: Words) {
        val sections = listOf(CapsuleSection("unread", words.unread, 1), CapsuleSection("main", words.section, list = false),
            CapsuleSection("inbox", words.inbox, 0))
        val task = CapsuleEntry("agent", words.task, words.preview, CapsuleState.Done)
        val main = CapsuleEntry("main-chat", words.main, "", CapsuleState.Idle)
        val pages = listOf(
            "main" to CapsuleContent(entries = listOf(main), selectedId = main.id, sections = sections, sectionId = "main",
                messages = listOf(CapsuleMessage("reply", words.reply, false)), canSend = true, available = true),
            "list" to CapsuleContent(entries = listOf(task), sections = sections, sectionId = "unread", available = true),
            "empty" to CapsuleContent(entries = emptyList(), sections = sections, sectionId = "inbox", available = true),
            "detail" to CapsuleContent(entries = listOf(task), selectedId = task.id, sections = sections, sectionId = "unread",
                available = true, canSend = true),
        )
        var page by mutableStateOf(pages.first().second)
        var dark by mutableStateOf(false)
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                Column(Modifier.testTag("shot").width(380.dp).background(PersonaTheme.palette.background).padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    CapsuleSurface(page.copy(badgeCount = 1), expanded = false, onExpand = {}, onCollapse = {}, onSelect = {},
                        onSelectSection = {}, onOpen = {}, onSend = { _, _ -> }, onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts)
                    CapsuleCompletionNotice(CapsuleCompletion("done", task.id, task.title)) {}
                    CapsuleSurface(page, expanded = true, onExpand = {}, onCollapse = {}, onSelect = {}, onSelectSection = {},
                        onOpen = {}, onSend = { _, _ -> }, onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts)
                }
            }
        }
        for ((name, content) in pages) for (d in listOf(false, true)) {
            page = content; dark = d
            rule.waitForIdle()
            rule.onNodeWithTag("capsule.open").assertExists()
            rule.onNodeWithTag("capsule.more").assertExists()
            rule.onNodeWithText(words.done).assertExists()
            if (name == "main") rule.onNodeWithTag("capsule.send").assertExists()
            if (name == "detail") rule.onNodeWithTag("capsule.back").assertExists()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "capsule-$language-$name-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
