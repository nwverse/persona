package persona.feature.assistant

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantSpace

/** The Health entry's rule (#4329): the newest health chat, else the health space's new-chat page. */
class HealthEntryTest {
    private fun chat(id: String, updated: String?, archived: Boolean = false) = Session(machineId = "mac", sessionId = id,
        updatedAt = updated, assistant = true, archived = archived, space = AssistantSpace.HEALTH)

    @Test fun historyOpensTheNewestAndSkipsArchivedOnes() {
        val chats = listOf(
            chat("older", "2026-11-01T08:00:00Z"),
            chat("newest", "2026-11-10T08:00:00Z"),
            chat("put-away", "2026-11-11T08:00:00Z", archived = true),
            chat("unknown-time", null),
        )
        assertEquals(HealthEntry.Target.Open(SessionKey("mac", "newest")), HealthEntry.resolve(chats))
        assertEquals(listOf("newest", "older", "unknown-time"), HealthEntry.history(chats).map { it.sessionId })
    }

    @Test fun tiesGoBySessionIdAscending() {
        val chats = listOf(chat("b", "2026-11-10T08:00:00Z"), chat("a", "2026-11-10T08:00:00Z"))
        assertEquals(HealthEntry.Target.Open(SessionKey("mac", "a")), HealthEntry.resolve(chats))
    }

    @Test fun noHistoryOpensTheNewHealthPage() {
        assertEquals(HealthEntry.Target.New, HealthEntry.resolve(emptyList()))
        assertEquals(HealthEntry.Target.New, HealthEntry.resolve(listOf(chat("put-away", "2026-11-11T08:00:00Z", archived = true))))
    }

    @Test fun newOnAHealthChatOpensTheHealthNewPage() {
        val from = SessionKey("mac", "newest")
        val health = HealthEntry.newChat(from, health = true)
        assertEquals(AssistantSpace.HEALTH, health.space)
        assertEquals(from, health.from)
        assertNull("nothing is started until the first message", health.started)
        assertNull("New elsewhere stays the assistant's own", HealthEntry.newChat(from, health = false).space)
    }
}
