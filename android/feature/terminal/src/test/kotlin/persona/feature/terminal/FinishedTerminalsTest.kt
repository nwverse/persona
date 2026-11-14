package persona.feature.terminal

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*

@OptIn(ExperimentalCoroutinesApi::class)
class FinishedTerminalsTest {
    @Test fun retainedAgentsUseTheirTitleAndSessionWhilePlainTerminalsNameTheirProgram() {
        val item = JsonCodec.decodeFromString<PaneItem>("""{"pane":"w1:p1","agent_session":"s","foreground":"zsh","cwd":"/home/me/project"}""")
        val session = Session(machineId = "desk~second", sessionId = "s", title = "Review release")
        val finished = pane("desk~second", item, session, "/home/me")
        assertTrue(finished.finished)
        assertEquals(session.key, finished.key)
        assertEquals("Review release", finished.title)
        assertEquals("zsh · ~/project", finished.detail)
        val plain = pane("desk", item.copy(agentSession = null, foreground = "vim", title = "old title"), null, "/home/me")
        assertFalse(plain.finished)
        assertEquals("pane:w1:p1", plain.key.sessionId)
        assertEquals("", plain.title)
        assertEquals("vim · ~/project", plain.detail)
    }

    @Test fun previewNeverClosesAndConfirmationOnlySendsThePreviewedIds() = runTest {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
        try {
            val panes = Panes()
            val model = TerminalPanesViewModel(panes, Sessions())
            model.previewFinished("desk"); runCurrent()
            assertEquals(listOf(Call("desk", true, emptyList())), panes.calls)
            assertEquals(listOf("Release notes"), model.confirmation.value?.titles)
            // Another terminal becomes finished while the confirmation is open; it was not approved.
            panes.preview = FinishedPanes(panes.preview.closed + FinishedPane("w1:p3", "new"))
            model.confirmFinished(); runCurrent()
            assertEquals(Call("desk", false, listOf("w1:p1")), panes.calls.last())
            assertNull(model.confirmation.value)
            assertEquals(listOf("desk"), panes.reads)
            model.close("desk", "w1:p2"); runCurrent()
            assertEquals(listOf("desk" to "w1:p2"), panes.closed)
        } finally { Dispatchers.resetMain() }
    }

    @Test fun cancelEmptyPreviewAndFailureDoNotCloseAnything() = runTest {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
        try {
            val panes = Panes()
            val model = TerminalPanesViewModel(panes, Sessions())
            model.previewFinished("desk"); runCurrent()
            model.dismissFinished(); model.confirmFinished(); runCurrent()
            assertEquals(1, panes.calls.size)
            panes.preview = FinishedPanes()
            model.previewFinished("desk"); runCurrent()
            assertNull(model.confirmation.value)
            panes.fail = true
            model.previewFinished("desk"); runCurrent()
            assertNull(model.confirmation.value)
            assertFalse(model.closing.value)
            assertTrue(panes.calls.all { it.preview })
            assertTrue(panes.closed.isEmpty())
        } finally { Dispatchers.resetMain() }
    }

    private data class Call(val machine: String, val preview: Boolean, val panes: List<String>)
    private class Panes : TerminalPanes {
        override val available = MutableStateFlow(true)
        var preview = FinishedPanes(listOf(FinishedPane("w1:p1", "s")), listOf(FinishedPane("w1:p2", "busy", why = "program")))
        val calls = mutableListOf<Call>()
        val reads = mutableListOf<String>()
        val closed = mutableListOf<Pair<String, String>>()
        var fail = false
        override suspend fun panes(machineId: String): PanesResponse { reads += machineId; return PanesResponse() }
        override suspend fun newTab(machineId: String, workspaceId: String, kind: String?, cwd: String?) = error("unused")
        override suspend fun close(machineId: String, pane: String) { closed += machineId to pane }
        override suspend fun closeFinished(machineId: String, preview: Boolean, panes: List<String>): FinishedPanes {
            calls += Call(machineId, preview, panes)
            if (fail) error("offline")
            return if (preview) this.preview else FinishedPanes(panes.map { FinishedPane(it) })
        }
    }
    private class Sessions : SessionsRepository {
        override val machines = MutableStateFlow(listOf(Machine("desk", "Workstation", online = true)))
        override val sessions = MutableStateFlow(listOf(Session(machineId = "desk", sessionId = "s", title = "Release notes")))
        override suspend fun refresh() = Unit
    }
}
