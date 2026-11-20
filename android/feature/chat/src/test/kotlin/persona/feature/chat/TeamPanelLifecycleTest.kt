package persona.feature.chat

import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.SessionChildren
import persona.core.contracts.Subagent
import persona.core.designsystem.LocalWindowLayout
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.WindowLayout

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w900dp-h700dp")
class TeamPanelLifecycleTest {
    @get:Rule val compose = createComposeRule()

    @Test fun aDeclaredWorkerOwnsItsComposerWhileHistoryAndOrdinarySubagentsStayReadOnly() {
        val worker = Session("server", "review@pair", foldedUnder = "lead", foldedAs = "worker")
        val lead = Session("server", "lead", subagents = listOf(Subagent("old", worker.sessionId)))
        var team by mutableStateOf(true)
        var history by mutableStateOf(false)
        compose.setContent {
            PersonaTheme {
                val session = rememberChatPageSession(worker.key, ChatChromeBindings(teamWorkerPage = team),
                    ChatRenderStyle.Agent, history, null, null, LocalContext.current.resources,
                    listOf(lead, worker), emptyList(), emptyList())
                Text(if (session.readOnly) "History" else "Composer")
            }
        }
        compose.onNodeWithText("Composer").assertExists()
        compose.runOnIdle { history = true }
        compose.onNodeWithText("History").assertExists()
        compose.runOnIdle { history = false; team = false }
        compose.onNodeWithText("History").assertExists()
    }

    @Test fun shortInterruptionPreservesThePickerAndEndedTeamDoesNotRecordAManualClose() {
        val lead = Session("server", "lead", title = "Release")
        val review = Session("server", "review", title = "Review", state = "needs_you", foldedUnder = "lead", foldedAs = "worker")
        val tests = review.copy(sessionId = "tests", title = "Tests", state = "working")
        var count by mutableIntStateOf(2)
        var mounted by mutableStateOf(true)
        compose.setContent {
            PersonaTheme {
                CompositionLocalProvider(LocalWindowLayout provides WindowLayout(expanded = true)) {
                    if (mounted) TeamChatLayout(lead.key, lead.copy(children = SessionChildren(workers = count)),
                        if (count > 0) listOf(review, tests) else emptyList(),
                        TeamChatHost("pair-lifecycle", null) { row, _ -> Text("Chat " + row.title) }, covered = false, requestId = null) { control, _, _ ->
                        control?.let { TeamButton(it) }
                    }
                }
            }
        }
        compose.onNodeWithText("Chat Review").assertExists()
        compose.onNodeWithContentDescription("Agent · Tests · Working").performClick()
        compose.onNodeWithText("Chat Tests").assertExists()
        compose.mainClock.autoAdvance = false
        compose.runOnIdle { count = 0 }
        compose.mainClock.advanceTimeBy(2_000)
        compose.onNodeWithTag("chat-team-panel").assertExists()
        compose.runOnIdle { count = 2 }
        compose.mainClock.advanceTimeByFrame()
        compose.onNodeWithText("Chat Tests").assertExists()
        compose.runOnIdle { count = 0 }
        compose.mainClock.advanceTimeBy(4_100)
        compose.onNodeWithTag("chat-team-panel").assertDoesNotExist()
        compose.mainClock.autoAdvance = true
        compose.runOnIdle { mounted = false }
        compose.runOnIdle { count = 2; mounted = true }
        compose.onNodeWithTag("chat-team-panel").assertExists()
        compose.onNodeWithTag("chat-team-close").performClick()
        compose.runOnIdle { mounted = false }
        compose.runOnIdle { mounted = true }
        compose.onNodeWithTag("chat-team-panel").assertDoesNotExist()
        compose.onNodeWithTag("chat-team-button").performClick()
        compose.onNodeWithText("Chat Tests").assertExists()
    }
}
