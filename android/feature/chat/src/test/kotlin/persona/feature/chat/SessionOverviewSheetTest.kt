package persona.feature.chat

import android.content.Context
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.ChatAction
import persona.core.contracts.api.ChatActionResult
import persona.core.contracts.api.ChatActions
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class SessionOverviewSheetTest {
    @get:Rule val rule = createComposeRule()

    private val actions = object : ChatActions {
        override suspend fun isMuted(session: Session) = false
        override suspend fun execute(session: Session, action: ChatAction) = ChatActionResult.Done
        override suspend fun verify(session: Session, action: ChatAction) = ChatActionResult.Done
        override suspend fun info(session: Session) = SessionInfo(
            sessionId = session.sessionId,
            cwd = "/repo/app",
            model = "claude-opus-5",
            version = "2.1.0",
            contextTokens = 48_000,
            turns = 4,
            outputTokens = 10_000,
        )
    }

    @Test fun rendersSectionsInOrderCollapsesEndedByDefaultAndOpensSubagentDetail() {
        val res = ApplicationProvider.getApplicationContext<Context>().resources
        val state = ChatChromeState(res, actions, CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate), {}, {})
        val parent = Session(
            machineId = "mac",
            sessionId = "parent-1",
            title = "Main session",
            cwd = "/repo/app",
            agent = "claude",
            state = "working",
            contextTokens = 48_000,
            contextWindow = 200_000,
            subagents = listOf(
                Subagent(
                    id = "emb-done",
                    sessionId = "parent-1@emb-done",
                    description = "Finished audit",
                    running = false,
                    startedAt = "2026-11-24T00:10:00Z",
                    updatedAt = "2026-11-24T00:20:00Z",
                ),
            ),
            background = listOf(
                BackgroundTask(id = "bg-1", kind = "shell", description = "Watch tests", startedAt = "2026-11-24T00:30:00Z"),
            ),
        )
        val crossChild = Session(
            machineId = "linux-box",
            sessionId = "remote-sub",
            title = "Cross-machine build",
            agent = "codex",
            state = "needs_you",
            attentionReason = "Approve release step",
            isSubagent = true,
            parentMachineId = "mac",
            parentSessionId = "parent-1",
            foldedUnder = "parent-1",
            foldedUnderMachineId = "mac",
            a2aTaskId = "a2a-7",
            subagentOrigin = "a2a",
            startedAt = "2026-11-24T00:40:00Z",
        )
        val opened = mutableListOf<SessionKey>()

        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                SessionOverviewSheet(
                    session = parent,
                    machine = Machine("mac", "MacBook", online = true),
                    machines = listOf(Machine("mac", "MacBook", online = true), Machine("linux-box", "87K-itx", online = true)),
                    childRows = listOf(crossChild),
                    state = state,
                    onOpenSubagent = { opened += it },
                    onDismiss = {},
                )
            }
        }
        rule.waitForIdle()

        // Verify key section headings exist and scroll to Subagents & Background tasks.
        rule.onNodeWithText("Session overview").assertExists()
        rule.onNodeWithText("Environment").assertExists()
        rule.onNodeWithTag("session-overview-list").performScrollToKey("section-context")
        rule.onAllNodesWithText("Context").onFirst().assertExists()

        rule.onNodeWithTag("session-overview-list").performScrollToKey("section-subagents")
        rule.onNodeWithText("Subagents").assertExists()
        rule.onNodeWithText("Running · 1").assertExists()
        rule.onNodeWithText("Cross-machine build").assertExists()
        rule.onNodeWithText("87K-itx · Codex").assertExists()

        // Ended subagent starts collapsed and expands on tap.
        rule.onNodeWithText("Finished audit").assertDoesNotExist()
        rule.onNodeWithTag("session-overview-list").performScrollToKey("subagents-ended-toggle")
        rule.onNodeWithTag("overview-ended-toggle").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("session-overview-list").performScrollToKey("sub-end:embedded:emb-done")
        rule.onNodeWithText("Finished audit").assertExists()

        // Tapping ended embedded subagent opens its parent@agent target key.
        rule.onNodeWithTag("overview-subagent-row:embedded:emb-done").performClick()
        // Tapping cross-machine A2A subagent opens its remote SessionKey.
        rule.onNodeWithTag("session-overview-list").performScrollToKey("sub-run:session:linux-box/remote-sub")
        rule.onNodeWithTag("overview-subagent-row:session:linux-box/remote-sub").performClick()
        assertEquals(listOf(SessionKey("mac", "parent-1@emb-done"), SessionKey("linux-box", "remote-sub")), opened)

        // Background tasks section appears after Subagents.
        rule.onNodeWithTag("session-overview-list").performScrollToKey("section-background")
        rule.onNodeWithText("Background tasks").assertExists()
    }
}
