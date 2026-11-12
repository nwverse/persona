package persona.platform.notify

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import persona.core.contracts.api.ExtensionNotificationActionRecord
import persona.core.contracts.api.ExtensionNotificationScope
import persona.core.contracts.generated.ExtensionNotification
import persona.core.contracts.generated.ExtensionNotifyAction
import persona.core.contracts.generated.ExtensionNotifyActionInput

@RunWith(RobolectricTestRunner::class)
class ExtensionNotificationActionsTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val scope = ExtensionNotificationScope("hub/generation", null, "assistant")

    @Test fun validationAcceptsAtMostThreeDistinctActionsAndPreservesTheirDeclarations() {
        val actions = listOf(
            ExtensionNotifyAction("stop", " Disable   this\nkey ", "destructive", true),
            ExtensionNotifyAction("note", "Reply", input = ExtensionNotifyActionInput("Say something")),
            ExtensionNotifyAction("refresh", "Refresh"),
        )
        val validated = ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", "usage", "state", actions))!!
        assertEquals("Disable this key", validated.actions!![0].title)
        assertEquals("destructive", validated.actions[0].role)
        assertEquals(true, validated.actions[0].foreground)
        assertEquals("Say something", validated.actions[1].input!!.placeholder)
        assertNull(ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", "usage",
            actions = actions + ExtensionNotifyAction("four", "Four"))))
        assertNull(ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", "usage",
            actions = listOf(actions[0], actions[0].copy(title = "Again"))))
        assertNull(ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", "usage",
            actions = listOf(ExtensionNotifyAction("bad", "Bad", "unknown")))))
        assertNull(ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", null,
            actions = listOf(ExtensionNotifyAction("bad", "Bad")))))
        assertNull(ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", "usage",
            state = "x".repeat(16_385))))
        for (id in listOf("", "x".repeat(129), "default", "dismiss")) {
            assertNull(ExtensionNotificationPayload.validate(ExtensionNotification("example-gateway", "usage",
                actions = listOf(ExtensionNotifyAction(id, "Title")))))
        }
    }

    @Test fun bodyDestinationsUseTheDeclaredPageOrTheExtensionDetails() {
        val page = record("body", null).copy(ui = "usage")
        val details = record("details", null).copy(ui = null)
        val intents = ExtensionNotificationIntents(context)
        val pageLink = intents.activityIntent(page, "Usage").data!!
        val detailLink = intents.activityIntent(details, "Usage").data!!
        assertEquals("usage", pageLink.getQueryParameter("ui"))
        assertEquals(page.token, pageLink.getQueryParameter("notification"))
        assertNull(detailLink.getQueryParameter("ui"))
        assertNull(detailLink.getQueryParameter("notification"))
        assertNull(pageLink.getQueryParameter("action"))
    }

    @Test fun buttonIntentsCannotOverwriteEachOtherAndInputIsOnlyDeclaredWhereAsked() {
        val records = listOf(
            record("note", ExtensionNotifyAction("note", "Reply", input = ExtensionNotifyActionInput("Say something"))),
            record("refresh", ExtensionNotifyAction("refresh", "Refresh")),
            record("stop", ExtensionNotifyAction("stop", "Stop", "destructive", true)),
        )
        val intents = ExtensionNotificationIntents(context)
        val data = records.map { intents.broadcastIntent(it).data!! }
        assertEquals(3, data.toSet().size)
        assertNotEquals(data[0], data[1])
        val actions = records.map { intents.action(it) }
        assertEquals("Say something", actions[0].remoteInputs.single().label)
        assertTrue(actions[1].remoteInputs.isNullOrEmpty())
        assertTrue(actions[2].authenticationRequired)
        assertEquals("destructive", records[2].action!!.role)
        assertTrue(data.all { it.getQueryParameter("action") == null })
    }

    private fun record(token: String, action: ExtensionNotifyAction?) = ExtensionNotificationActionRecord(
        token = token.padEnd(32, '0'), tag = "alert:$token", extension = "example-gateway", ui = "usage", state = "state",
        action = action, scope = scope, createdAt = 1, dedupeKey = token)
}
