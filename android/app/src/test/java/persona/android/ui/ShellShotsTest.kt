package persona.android.ui

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.android.R
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/** The two primary home destinations in every language/style, never a Settings or Inbox tab. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class)
class ShellShotsTest {
    @get:Rule val rule = createEmptyComposeRule()
    private fun out(): File = File("build/i18n-shots").also { it.mkdirs() }

    @Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi")
    @Test fun simplifiedChinese() = shots("zh", "助手", "收件箱", "设置", "体验模式没有终端数据", "侧边栏")

    @Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
    @Test fun english() = shots("en", "Assistant", "Inbox", "Settings", "The demo has no terminal data", "Sidebar")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h800dp-xhdpi")
    @Test fun japanese() = shots("ja", "アシスタント", "受信トレイ", "設定", "体験モードにはターミナルのデータがありません", "サイドバー")

    private fun shots(language: String, assistant: String, inbox: String, settings: String, unavailable: String, sidebar: String) {
        val navigator = object : Navigator {
            override fun navigate(key: NavKey) = Unit
            override fun back() = Unit
            override fun selectTab(tab: AppTab) = Unit
        }
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var selected by mutableStateOf(AppTab.Persona)
        var sidebarOpened = false
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    val items = navigationItems(emptyMap())
                    Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
                        HomeNavigation(items, selected.name, { selected = AppTab.valueOf(it) }, leading = {
                            JPersonaButton(PersonaState.Idle, stringResource(R.string.app_sidebar), { sidebarOpened = true })
                        })
                        UnavailablePage(unavailable, navigator)
                    }
                }
            } }
            for (s in AppStyle.entries) {
                var lightPixel: Int? = null
                for (d in listOf(false, true)) {
                    style = s; dark = d
                    Snapshot.sendApplyNotifications()
                    rule.waitForIdle()
                    rule.onAllNodesWithText(assistant).assertCountEquals(1)
                    rule.onAllNodesWithText("Agents").assertCountEquals(1)
                    rule.onAllNodesWithText(inbox).assertCountEquals(0)
                    rule.onAllNodesWithText(settings).assertCountEquals(0)
                    rule.onNodeWithText("Agents").performClick()
                    assertEquals(AppTab.Agents, selected)
                    rule.onNodeWithText(assistant).performClick()
                    assertEquals(AppTab.Persona, selected)
                    rule.onNodeWithContentDescription(sidebar).performClick()
                    assertTrue(sidebarOpened)
                    val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                    if (!d) lightPixel = bitmap.getPixel(1, 1)
                    else assertTrue("light and dark must differ", lightPixel != bitmap.getPixel(1, 1))
                    File(out(), "shell-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                        .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
                }
            }
        }
    }
}
