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

    /** A hub with Manual mark as read (#7356): what it says, and every seen and switch call the phone makes. */
    private class ReadHub(var settings: ReadSettings? = ReadSettings(), var refusePut: Boolean = false) : HubPreferencesTransport {
        val sent = mutableListOf<String>()
        override suspend fun pins() = HubPreferenceSet()
        override suspend fun mutes() = HubPreferenceSet()
        override suspend fun putPins(value: HubPreferenceSet) = Unit
        override suspend fun putMutes(value: HubPreferenceSet) = Unit
        override suspend fun seen(session: SessionKey) { sent += "seen:${session.sessionId}" }
        override suspend fun markRead(session: SessionKey) { sent += "read:${session.sessionId}" }
        override suspend fun readSettings() = settings
        override suspend fun putReadSettings(manual: Boolean): ReadSettings {
            sent += "put:$manual"
            if (refusePut) throw java.io.IOException("read-only device")
            return ReadSettings(manual, canWrite = true).also { settings = it }
        }
    }

    @Test fun offByDefaultALookIsSentAsBefore() = runTest {
        val hub = ReadHub()
        val repository = HubPreferencesRepository(hub, backgroundScope)
        repository.refresh()
        assertEquals(ReadSettingsState(manual = false, canWrite = false, supported = true, known = true), repository.preferences.value.read)
        assertTrue(repository.markLooked(SessionKey("machine", "s1"), "v1"))
        assertEquals(listOf("seen:s1"), hub.sent)
        assertEquals("v1", repository.preferences.value.seen["machine/s1"])
    }

    @Test fun manualModeDropsLooksAndKeepsTheUsersMarkAsRead() = runTest {
        val hub = ReadHub(ReadSettings(manual = true, canWrite = true))
        val repository = HubPreferencesRepository(hub, backgroundScope)
        repository.refresh()
        assertTrue(repository.preferences.value.read.manual)
        // looking is not reading: nothing sent, nothing shown read
        assertFalse(repository.markLooked(SessionKey("machine", "s1"), "v1"))
        assertTrue(hub.sent.isEmpty())
        assertNull(repository.preferences.value.seen["machine/s1"])
        // the user's own, twice: both say manual, and it is read
        repository.markSeen(SessionKey("machine", "s1"), "v1")
        repository.markSeen(SessionKey("machine", "s1"), "v1")
        assertEquals(listOf("read:s1", "read:s1"), hub.sent)
        assertEquals("v1", repository.preferences.value.seen["machine/s1"])
        // a new turn (another version) is not covered by the old mark, and a look still does not clear it
        assertFalse(repository.markLooked(SessionKey("machine", "s1"), "v2"))
        assertEquals("v1", repository.preferences.value.seen["machine/s1"])
    }

    @Test fun aLookFollowsTheSwitchAnotherDeviceFlipped() = runTest {
        val hub = ReadHub(ReadSettings(manual = false, canWrite = true))
        val repository = HubPreferencesRepository(hub, backgroundScope)
        repository.refresh()
        hub.settings = ReadSettings(manual = true, canWrite = true) // turned on on the web; this phone was not told
        assertFalse(repository.markLooked(SessionKey("machine", "s1"), "v1"))
        assertTrue(hub.sent.isEmpty())
        assertTrue(repository.preferences.value.read.manual)
    }

    @Test fun theSwitchIsWrittenToTheHubKeptForARelaunchAndPutBackWhenRefused() = runTest {
        val hub = ReadHub(ReadSettings(manual = false, canWrite = true))
        val disk = ListSnapshotStore(temporary.newFolder())
        val repository = HubPreferencesRepository(hub, backgroundScope, disk)
        repository.refresh()
        repository.setManualRead(true)
        assertEquals(listOf("put:true"), hub.sent)
        assertTrue(repository.preferences.value.read.manual)
        // a relaunch, before the hub answers again, starts from it
        assertTrue(disk.restore<HubPreferencesSnapshot>("preferences")!!.read.manual)
        hub.refusePut = true
        assertNotNull(runCatching { repository.setManualRead(false) }.exceptionOrNull())
        assertTrue(repository.preferences.value.read.manual)
    }

    @Test fun aHubFromBeforeTheSwitchIsOffAndUnsupported() = runTest {
        val hub = ReadHub(settings = null)
        val repository = HubPreferencesRepository(hub, backgroundScope)
        repository.refresh()
        assertEquals(ReadSettingsState(manual = false, supported = false, known = true), repository.preferences.value.read)
        assertTrue(repository.markLooked(SessionKey("machine", "s1"), "v1"))
        assertEquals(listOf("seen:s1"), hub.sent)
    }

    @Test fun theHubsAnswerDecodes() {
        assertEquals(ReadSettings(manual = true, canWrite = true),
            persona.core.contracts.JsonCodec.decodeFromString<ReadSettings>("""{"manual":true,"can_write":true}"""))
        assertEquals(ReadSettings(), persona.core.contracts.JsonCodec.decodeFromString<ReadSettings>("{}"))
    }
}
