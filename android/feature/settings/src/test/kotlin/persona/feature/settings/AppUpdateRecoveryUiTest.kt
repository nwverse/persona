package persona.feature.settings

import android.content.Context
import android.content.pm.PackageInstaller
import android.graphics.Bitmap
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.util.ReflectionHelpers
import persona.core.contracts.api.AppUpdates
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class AppUpdateRecoveryUiTest {
    @get:Rule val rule = createComposeRule()

    @Test fun recoveryRequiresConfirmationWithoutANewerReleaseInEveryAppearance() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val installer = context.packageManager.packageInstaller
        val id = installer.createSession(PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
        })
        ReflectionHelpers.setField(installer.getSessionInfo(id), "sealed", true)
        val controller = AppUpdateState(object : AppUpdates {
            override suspend fun check() = null
        }, 100)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val cancel = context.getString(R.string.settings_update_pending_cancel)
        val confirmation = context.getString(R.string.settings_update_pending_confirm)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) { AppUpdateRecovery(controller) }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitUntil(10_000) {
                rule.onAllNodes(androidx.compose.ui.test.hasText(cancel)).fetchSemanticsNodes().isNotEmpty()
            }
            rule.onNodeWithText(cancel).performClick()
            rule.onNodeWithText(confirmation).assertExists()
            assertNotNull(installer.getSessionInfo(id))
            val bitmap = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
            val out = File("build/update-recovery-shots").apply { mkdirs() }
            File(out, "recovery-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
            }
            rule.onNodeWithText("Cancel", substring = false).performClick()
            assertNotNull(installer.getSessionInfo(id))
        }
        rule.onNodeWithText(cancel).performClick()
        rule.onNode(androidx.compose.ui.test.hasText(cancel) and androidx.compose.ui.test.hasClickAction() and
            androidx.compose.ui.test.hasAnyAncestor(isDialog())).performClick()
        rule.waitUntil(10_000) { installer.getSessionInfo(id) == null }
        assertNull(installer.getSessionInfo(id))
        rule.onNodeWithText(context.getString(R.string.settings_update_pending_cancelled)).assertExists()
    }
}
