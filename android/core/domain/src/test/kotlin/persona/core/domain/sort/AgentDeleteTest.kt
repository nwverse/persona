package persona.core.domain.sort

import persona.core.domain.zhAny

import persona.core.contracts.Session
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** "Delete conversation" (issue #1749): which conversations offer it, and what the confirmation says. */
class AgentDeleteTest {
    @Test fun onlyConversationsThatAreNotRunningCanBeDeleted() {
        // What the hub accepts and then hides (server/internal/hub/deleted_sessions.go stillDeleted).
        for (status in listOf("offline", "done", "idle", "unknown", "interrupted")) assertTrue(status, Session("m", "s", status = status).canDelete)
        for (status in listOf("working", "blocked")) assertFalse(status, Session("m", "s", status = status).canDelete)
        assertFalse(Session("m", "s", status = "offline", starting = true).canDelete)
        // The assistant's conversations archive instead.
        assertFalse(Session("m", "s", status = "offline", assistant = true).canDelete)
    }

    @Test fun theConfirmationSaysTheRecordStays() {
        val session = Session(machineId = "box", sessionId = "s", title = "修登录", cwd = "/srv/app", status = "offline")
        val confirmation = session.deleteConfirmation("工作站")
        assertEquals("删除这个对话？", (confirmation.title).zhAny())
        assertEquals("「修登录」会从各端的列表里移除。工作站 上的对话记录不会删除，之后在电脑上用 claude --resume 继续，它会重新出现。\n\n如果 Agent 仍处于打开状态且可关闭，它也会关闭。",
            (confirmation.message).zhAny())
        val codex = Session(machineId = "box", sessionId = "s", title = "修登录", agent = "codex", status = "offline")
        assertEquals("「修登录」会从各端的列表里移除。电脑上的对话记录不会删除，之后在电脑上用 codex resume 继续，它会重新出现。\n\n如果 Agent 仍处于打开状态且可关闭，它也会关闭。",
            (codex.deleteConfirmation(null).message).zhAny())
    }
}
