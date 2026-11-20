package persona.android.di

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*

class HealthSpaceGroupTest {
    private class Hub : AssistantConversations {
        override val assistant = MutableStateFlow(AssistantSnapshot())
        override val pinned = MutableStateFlow(emptySet<SessionKey>())
        override suspend fun refresh() = Unit
        override suspend fun startConversation(): SessionKey = error("No regular chat")
        val calls = mutableListOf<String>()
        override suspend fun healthSpace() = object : HealthSpace {
            override val machineId = "shared-id"
            override suspend fun reports() = emptyList<CheckupReport>()
            override suspend fun memory() = emptyList<MemoryFile>()
            override suspend fun deleteReport(name: String) { calls += name }
            override suspend fun saveMemory(name: String, content: String) { calls += name }
            override suspend fun deleteMemory(name: String) { calls += name }
            override suspend fun startReportChat(prompt: String) = SessionKey(machineId, "health")
        }
    }
    @Test fun sameMachineAndChatIdsOnTwoHubsDoNotShareAnOwner() = runTest {
        val a = Hub()
        val b = Hub()
        val first = a.inGroup(GroupNames(null), backgroundScope).healthSpace()
        val names = GroupNames("g0a1b2c3d")
        val second = b.inGroup(names, backgroundScope).healthSpace()
        assertEquals("shared-id", first.machineId)
        assertEquals(names.machineId("shared-id"), second.machineId)
        assertNotEquals(first.machineId, second.machineId)
        assertEquals(names.key(SessionKey("shared-id", "health")), second.startReportChat("fixture"))
        second.saveMemory("sample.md", "Sample")
        first.deleteReport("Sample.pdf")
        assertEquals(listOf("Sample.pdf"), a.calls)
        assertEquals(listOf("sample.md"), b.calls)
    }
}
