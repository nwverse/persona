package persona.feature.settings

import android.Manifest
import android.app.NotificationManager
import android.content.Context
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.LocalActivityResultRegistryOwner
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.core.app.ActivityOptionsCompat
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1600dp-xhdpi")
class ReadinessNotificationPermissionTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val context get() = ApplicationProvider.getApplicationContext<Context>()
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() = Unit
    }

    private class RecordingRegistry : ActivityResultRegistry(), ActivityResultRegistryOwner {
        override val activityResultRegistry get() = this
        val inputs = mutableListOf<Any?>()

        override fun <I, O> onLaunch(
            requestCode: Int,
            contract: ActivityResultContract<I, O>,
            input: I,
            options: ActivityOptionsCompat?,
        ) {
            inputs += input
        }
    }

    @Before fun resetNotificationState() {
        preferences().edit().clear().commit()
        shadowOf(context.getSystemService(NotificationManager::class.java)).setNotificationsEnabled(false)
    }

    private fun preferences() = context.getSharedPreferences("settings-readiness", Context.MODE_PRIVATE)
    private fun text(id: Int) = rule.activity.getString(id)

    private fun show(registry: RecordingRegistry, style: () -> AppStyle = { AppStyle.Glass }) {
        rule.setContent {
            PersonaTheme(reduceMotion = true, style = style()) {
                CompositionLocalProvider(LocalActivityResultRegistryOwner provides registry) {
                    ReadinessScreen(sessions, notificationsOnly = true, onDiagnostics = {}, onDone = {})
                }
            }
        }
        rule.waitForIdle()
    }

    private fun tap(id: Int) {
        rule.onNode(hasText(text(id)) and hasClickAction()).performClick()
        rule.waitForIdle()
    }

    private fun tapFirst(id: Int) {
        rule.onAllNodes(hasText(text(id)) and hasClickAction()).onFirst().performClick()
        rule.waitForIdle()
    }

    private fun assertNotificationSettingsOpened() {
        val intent = shadowOf(rule.activity).nextStartedActivity
        assertEquals(Settings.ACTION_APP_NOTIFICATION_SETTINGS, intent.action)
        assertEquals(rule.activity.packageName, intent.getStringExtra(Settings.EXTRA_APP_PACKAGE))
    }

    @Test fun onlyTheFirstExplicitTapRequestsPermissionThenDenialOpensSettings() {
        val registry = RecordingRegistry()
        show(registry)
        assertTrue("readiness must not request on display, including Demo", registry.inputs.isEmpty())

        tap(R.string.settings_ready_allow_notifications)
        assertEquals(listOf(Manifest.permission.POST_NOTIFICATIONS), registry.inputs)
        assertTrue(preferences().getBoolean("notification-requested", false))
        rule.onNodeWithText(text(R.string.settings_ready_allow_notifications)).assertDoesNotExist()

        // The registry does not grant: asked is not granted, so a denial or permanent denial stays unsatisfied.
        val notification = PhoneReadiness.items(context, SystemKind("Android", "15", "")).first { it.id == "notifications" }
        assertEquals(false, notification.satisfied)
        tapFirst(R.string.settings_ready_open)
        assertNotificationSettingsOpened()
        assertEquals(1, registry.inputs.size)

        // A later permanent denial has the same recovery path and never launches the runtime request again.
        tapFirst(R.string.settings_ready_open)
        assertNotificationSettingsOpened()
        assertEquals(1, registry.inputs.size)
    }

    @Test fun aRequestRecordedByAnotherReadinessDoesNotLaunchAgain() {
        val registry = RecordingRegistry()
        show(registry)
        preferences().edit().putBoolean("notification-requested", true).commit()

        tap(R.string.settings_ready_allow_notifications)
        assertTrue(registry.inputs.isEmpty())
        rule.onNodeWithText(text(R.string.settings_ready_allow_notifications)).assertDoesNotExist()
        tapFirst(R.string.settings_ready_open)
        assertNotificationSettingsOpened()
    }

    @Test fun anAlreadyAuthorizedUpgradeDoesNotRequestAgain() {
        shadowOf(context.getSystemService(NotificationManager::class.java)).setNotificationsEnabled(true)
        val registry = RecordingRegistry()
        show(registry)

        rule.onNodeWithText(text(R.string.settings_ready_satisfied)).assertExists()
        rule.onNodeWithText(text(R.string.settings_ready_allow_notifications)).assertDoesNotExist()
        tapFirst(R.string.settings_ready_open)
        assertNotificationSettingsOpened()
        assertTrue(registry.inputs.isEmpty())
        assertFalse(preferences().getBoolean("notification-requested", false))
    }

    @Config(sdk = [32])
    @Test fun api32OpensSettingsWithoutARuntimeRequest() {
        val registry = RecordingRegistry()
        show(registry)

        rule.onNodeWithText(text(R.string.settings_ready_allow_notifications)).assertDoesNotExist()
        tapFirst(R.string.settings_ready_open)
        assertNotificationSettingsOpened()
        assertTrue(registry.inputs.isEmpty())
        assertFalse(preferences().getBoolean("notification-requested", false))
    }

    @Test fun permissionDecisionCoversApiAndGrantStates() {
        val notification = ReadyItem("notifications", "Notifications", "Detail", false, listOf(notificationSettings))
        for (sdk in listOf(32, 33, 35)) {
            assertEquals(sdk >= 33, shouldRequestNotificationPermission(notification, sdk, requested = false))
            assertFalse(shouldRequestNotificationPermission(notification, sdk, requested = true))
            assertFalse(shouldRequestNotificationPermission(notification.copy(satisfied = true), sdk, requested = false))
        }
    }

    @Config(sdk = [35], qualifiers = "en-w320dp-h1400dp-xhdpi", fontScale = 2f)
    @Test fun englishAllowActionStaysOnOneLineAtLargeText() = assertAllowActionIsSingleLine()

    @Config(sdk = [35], qualifiers = "zh-w320dp-h1400dp-xhdpi", fontScale = 2f)
    @Test fun chineseAllowActionStaysOnOneLineAtLargeText() = assertAllowActionIsSingleLine()

    @Config(sdk = [35], qualifiers = "ja-w320dp-h1400dp-xhdpi", fontScale = 2f)
    @Test fun japaneseAllowActionStaysOnOneLineAtLargeText() = assertAllowActionIsSingleLine()

    private fun assertAllowActionIsSingleLine() {
        var style by mutableStateOf(AppStyle.Glass)
        show(RecordingRegistry()) { style }
        val label = text(R.string.settings_ready_allow_notifications)
        for (nextStyle in AppStyle.entries) {
            rule.runOnIdle { style = nextStyle }
            rule.onNodeWithText(label, useUnmergedTree = true).assertIsDisplayed()
            val node = rule.onNodeWithText(label, useUnmergedTree = true).fetchSemanticsNode()
            val layouts = mutableListOf<TextLayoutResult>()
            node.config[SemanticsActions.GetTextLayoutResult].action!!(layouts)
            assertEquals("$nextStyle must keep the permission action on one line", 1, layouts.single().lineCount)
        }
    }
}
