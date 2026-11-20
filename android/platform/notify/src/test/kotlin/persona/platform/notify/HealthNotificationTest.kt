package persona.platform.notify

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.navigation.NotificationRoute

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en", application = NotifyRestoreApplication::class)
class HealthNotificationTest {
    private val app = ApplicationProvider.getApplicationContext<NotifyRestoreApplication>()
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow<List<Machine>>(emptyList())
        override val sessions = MutableStateFlow(listOf(Session(machineId = "machine", sessionId = "sample", title = "Private fixture title", assistant = true, space = AssistantSpace.HEALTH)))
        override suspend fun refresh() = Unit
    }
    private fun notifier(): Notifier {
        shadowOf(app).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        return Notifier(app, sessions, object : NotificationPreferencesSource {
            override val preferences = MutableStateFlow<DeviceNotificationPreferences?>(DeviceNotificationPreferences(input = true, lockDetails = true))
            override suspend fun refreshPreferences() = preferences.value
        })
    }
    private fun shown(tag: String) = app.getSystemService(NotificationManager::class.java).activeNotifications.single { it.tag == tag }.notification
    private val card = JsonCodec.decodeFromString<Interaction>("""{"id":"sample-card","machine_id":"machine","session_id":"sample","kind":"question","session_title":"Private fixture title","questions":[{"question":"Private fixture question","options":[{"label":"Private fixture option"}]}]}""")
    private val safe = NotifyEvent("interaction", "machine", "sample", "Persona", "Health", "New in your health space. Open Persona to see it.", card.id)

    @Test fun liveHealthCardKeepsHubCopyAndDeepLinkWithoutPrivateQuestionOrOption() {
        notifier().postLiveInteraction(card, safe)
        val note = shown("interaction:${card.id}")
        assertEquals("Persona", note.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals(safe.body, note.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(safe.body, note.extras.getCharSequence(Notification.EXTRA_BIG_TEXT).toString())
        assertEquals(safe.subtitle, note.extras.getCharSequence(Notification.EXTRA_SUB_TEXT).toString())
        assertTrue(note.actions.orEmpty().none { "Private fixture" in it.title.toString() })
        val route = requireNotNull(NotificationRoute.parse(shadowOf(note.contentIntent).savedIntent.data!!))
        assertEquals(card.id, route.request)
        assertEquals(card.sessionId, route.chat?.sessionId)
    }

    @Test fun catchUpWithoutSessionUsesHealthFolderAndOrdinaryFolderKeepsItsExistingCopy() {
        sessions.sessions.value = emptyList()
        val notifier = notifier()
        for (path in listOf("/fixture/.persona/health/reports", "/fixture/relocated-hub/health/reports", "C:\\Fixture\\.persona\\health-m-1ab\\reports")) {
            notifier.postInteraction(card.copy(cwd = path))
            val note = shown("interaction:${card.id}")
            assertEquals(safe.body, note.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
            assertTrue(note.actions.orEmpty().none { "Private fixture" in it.title.toString() })
        }
        notifier.postInteraction(card.copy(cwd = "/fixture/.persona/health-notes"))
        assertEquals("Private fixture title", shown("interaction:${card.id}").extras.getCharSequence(Notification.EXTRA_TITLE).toString())
    }

    @Test fun ordinaryHealthChatNoticeNeverReadsTheCachedSessionTitleForItsCopy() {
        app.extensions = object : ExtensionNotificationEnvironment {
            override fun currentScope() = null
            override fun extensionUi(record: persona.core.contracts.api.ExtensionNotificationActionRecord) = null
        }
        notifier().postNotify(safe.copy(kind = "assistant", interactionId = null))
        assertEquals(safe.body, shown("assistant:machine/sample").extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(safe.title, shown("assistant:machine/sample").extras.getCharSequence(Notification.EXTRA_TITLE).toString())
    }
}
