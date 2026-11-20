package persona.android.di

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*

class ChildPageGroupTest {
    @Test fun coldWorkerLinksReadTheTargetPairingsCurrentHelloAndAdvertisedLookup() = runTest {
        val hello = Hello(features = listOf("sessions_lookup", "sessions_hierarchy"))
        val socket = MutableStateFlow<SocketHello?>(null)
        val connection = object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override val lastHello = hello
            override val currentHello = socket
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = false
        }
        val paired = connection.inGroup(GroupNames("g12345678"), backgroundScope)
        assertSame(hello, paired.lastHello)
        assertNull(paired.currentHello.value)
        socket.value = SocketHello(7L, hello)
        assertEquals(listOf("sessions_lookup", "sessions_hierarchy"), paired.currentHello.value?.hello?.features)
        socket.value = null
        assertNull(paired.currentHello.value)
    }

    @Test fun childDeltasKeepTheirHubForRowsAndRemovals() = runBlocking {
        var requested: SessionKey? = null
        var checkpoint: String? = null
        val source = object : ChatActions {
            override suspend fun isMuted(session: Session) = false
            override suspend fun execute(session: Session, action: ChatAction) = ChatActionResult.Done
            override suspend fun verify(session: Session, action: ChatAction) = ChatActionResult.Done
            override suspend fun childPage(session: Session, since: String?): SessionListPage {
                requested = session.key; checkpoint = since
                return SessionListPage(listOf(Session("m", "child", foldedUnder = "parent")),
                    remove = listOf("""["m","old/child"]"""), revision = "r2", total = 1)
            }
        }
        val names = GroupNames("g12345678")
        val page = source.inGroup(names).childPage(Session(names.machineId("m"), "parent"), "r1")
        assertEquals(SessionKey("m", "parent"), requested)
        assertEquals("r1", checkpoint)
        assertEquals(names.machineId("m"), page.sessions.single().machineId)
        assertEquals(listOf(names.machineId("m"), "old/child"), JsonCodec.decodeFromString<List<String>>(page.remove.single()))
        assertEquals("r2", page.revision)
    }
}
