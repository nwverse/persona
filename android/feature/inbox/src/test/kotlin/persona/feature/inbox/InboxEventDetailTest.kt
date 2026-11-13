package persona.feature.inbox

import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.InboxChainEntry
import persona.core.contracts.InboxChainLink
import persona.core.contracts.InboxEventFields
import persona.core.contracts.InboxHandling
import persona.core.contracts.InboxSuggestion
import persona.core.contracts.Session
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.domain.inbox.InboxItem
import persona.core.domain.sort.AgentSession
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

/** The detail's header, current-state fallbacks and the timeline interaction, separate from the showcase captures. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w500dp-h1200dp-xhdpi")
class InboxEventDetailTest {
    @get:Rule val rule = createComposeRule()
    private val repository = FakeInboxRepository()
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private fun record(now: String? = "confirm_reply", where: String? = "Launch group"): InboxRecord {
        val event = InboxEventFields(
            status = "action", where = where, now = now, handling = InboxHandling(state = "done"),
            suggestion = InboxSuggestion(version = 1, kind = "reply", text = "Friday works"), recap = "Recap",
            chain = listOf(
                InboxChainEntry("c1", at = "2026-01-01T11:02:00Z", kind = "received", text = "Received", via = "chat"),
                InboxChainEntry("c2", at = "2026-01-01T11:03:00Z", kind = "handling", text = "Handling", via = "assistant"),
                InboxChainEntry("c3", at = "2026-01-01T11:04:00Z", kind = "agent", text = "Agent", via = "agent",
                    link = InboxChainLink(type = "session", machineId = "m1", sessionId = "s-agent")),
                InboxChainEntry("c4", at = "2026-01-01T11:05:00Z", kind = "reported", text = "Reported", via = "agent"),
            ),
        )
        return InboxRecord(
            InboxItem("event", kind = "group", title = "Event title", sender = "Alex", preview = "Can we?", summary = "Summary",
                status = event.status, where = where),
            source = "chat", sourceLabel = "Team chat", event = event,
        )
    }

    private fun show(record: InboxRecord, sessions: List<AgentSession> = emptyList()) {
        repository.snapshot.value = InboxSnapshot(records = listOf(record), sessions = sessions)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = true) {
                Box { InboxEventScreen(repository, InboxEventRoute(record.item.id), navigator) }
            }
        }
    }

    @Test fun headerStateAndTimelineFollowTheSharedOrderAndSources() {
        val working = AgentSession(Session(machineId = "m1", sessionId = "s-agent", status = "working", state = "working"))
        show(record(), listOf(working))
        val meta = rule.onNodeWithText("Launch group · Alex: Can we?", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val now = rule.onNodeWithText("Waiting for you to confirm a reply", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val suggestion = rule.onNodeWithText("Friday works", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val summary = rule.onNodeWithText("Summary", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val recap = rule.onNodeWithText("Recap", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val chain = rule.onNodeWithText("What happened", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        assertTrue(meta.top < now.top && now.top < suggestion.top && suggestion.top < summary.top && summary.top < recap.top && recap.top < chain.top)
        rule.onNodeWithText("Received", useUnmergedTree = true).assertDoesNotExist()
        rule.onNodeWithText("Reported", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("Assistant says", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("Show all (4)").performClick()
        rule.onNodeWithText("Received", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("Handling", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("Agent", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("Working", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("Collapse").performClick()
        rule.onNodeWithText("Received", useUnmergedTree = true).assertDoesNotExist()
    }

    @Test fun olderHubsKeepAStateLineForSuggestionsTiersAndDone() {
        val suggested = record(now = null, where = null)
        show(suggested)
        rule.onNodeWithText("Alex: Can we?", useUnmergedTree = true).assertExists()
        // The suggestion card itself carries the old hub's suggestion; the separate state line does not repeat it.
        rule.onNodeWithText("Friday works", useUnmergedTree = true).assertExists()

        val tierOnly = suggested.copy(event = suggested.event.copy(suggestion = null, handling = null, status = "fyi"),
            item = suggested.item.copy(status = "fyi", tier = "fyi"))
        repository.snapshot.value = InboxSnapshot(records = listOf(tierOnly))
        rule.onNodeWithText("FYI", useUnmergedTree = true).assertExists()

        val done = tierOnly.copy(item = tierOnly.item.copy(doneAt = "2026-01-01T12:00:00Z", status = null),
            event = tierOnly.event.copy(status = null, doneBy = "user"))
        repository.snapshot.value = InboxSnapshot(records = listOf(done))
        rule.onNodeWithText("Done", useUnmergedTree = true).assertExists()
    }
}
