package persona.core.data

import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

@OptIn(ExperimentalCoroutinesApi::class)
class HubPreferencesRepositoryTest {
    @get:Rule val temporary = TemporaryFolder()

    @Test fun acknowledgedAgentMutesPublishWithoutAnotherNetworkRequest() = runTest {
        var reads = 0
        val transport = object : HubPreferencesTransport {
            override suspend fun pins(): HubPreferenceSet { reads++; return HubPreferenceSet(sessions = listOf("machine/pinned")) }
            override suspend fun mutes(): HubPreferenceSet { reads++; return HubPreferenceSet() }
            override suspend fun putPins(value: HubPreferenceSet) = error("Unexpected write")
            override suspend fun putMutes(value: HubPreferenceSet) = error("Unexpected write")
            override suspend fun seen(session: SessionKey) = Unit
        }
        val repository = HubPreferencesRepository(transport, backgroundScope)
        runCurrent()
        val before = reads
        val mutes = HubPreferenceSet(spaces = listOf("machine/project"), sessions = listOf("machine/muted"),
            allowed = setOf("machine/allowed"))
        repository.acceptAgentPreferences(mutes = mutes)
        assertEquals(before, reads)
        assertEquals(mutes, repository.preferences.value.mutes)
        assertEquals(listOf("machine/pinned"), repository.preferences.value.pins.sessions)
    }

    @Test fun failedPinRollsBackOptimisticStateAndDoesNotPersistIt() = runTest {
        val original = HubPreferenceSet(spaces = listOf("machine/project"), sessions = listOf("machine/old"))
        val requested = CompletableDeferred<HubPreferenceSet>()
        val response = CompletableDeferred<Unit>()
        val transport = object : HubPreferencesTransport {
            override suspend fun pins() = original
            override suspend fun mutes() = HubPreferenceSet(allowed = setOf("machine/allowed"))
            override suspend fun putPins(value: HubPreferenceSet) { requested.complete(value); response.await() }
            override suspend fun putMutes(value: HubPreferenceSet) = Unit
            override suspend fun seen(session: SessionKey) = Unit
        }
        val disk = ListSnapshotStore(temporary.newFolder())
        val repository = HubPreferencesRepository(transport, backgroundScope, disk)
        repository.refresh()
        var failure: Throwable? = null
        val write = launch { failure = runCatching { repository.togglePin("machine/new") }.exceptionOrNull() }
        val sent = requested.await()
        assertEquals(original.spaces, sent.spaces)
        assertEquals(listOf("machine/old", "machine/new"), repository.preferences.value.pins.sessions)
        assertEquals(original, disk.restore<HubPreferencesSnapshot>("preferences")!!.pins)
        response.completeExceptionally(java.io.IOException("Write rejected"))
        write.join()
        assertNotNull(failure)
        assertEquals(original, repository.preferences.value.pins)
        assertEquals(original, disk.restore<HubPreferencesSnapshot>("preferences")!!.pins)
        runCurrent()
    }

    @Test fun markSeenIsTheUsersOwnMarkAsRead() = runTest {
        val sent = mutableListOf<String>()
        val transport = object : HubPreferencesTransport {
            override suspend fun pins() = HubPreferenceSet()
            override suspend fun mutes() = HubPreferenceSet()
            override suspend fun putPins(value: HubPreferenceSet) = Unit
            override suspend fun putMutes(value: HubPreferenceSet) = Unit
            override suspend fun seen(session: SessionKey) { sent += "seen:${session.sessionId}" }
            override suspend fun markRead(session: SessionKey) { sent += "read:${session.sessionId}" }
        }
        val repository = HubPreferencesRepository(transport, backgroundScope)
        repository.markSeen(SessionKey("machine", "s1"), "v1")
        assertEquals(listOf("read:s1"), sent)
        assertEquals("v1", repository.preferences.value.seen["machine/s1"])
    }
}
