package persona.feature.inbox

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.ManagerRoster

/**
 * "Muted" (docs/assistant/inbox.md "Muting") with several managers (docs/assistant/managers.md "Grouping"): each one's
 * mutes are read with it named, under its name, the default first; one that can't be read keeps its place; Unmute goes
 * to the manager whose mute it is and, failing, puts it back there.
 */
class InboxMutesModelTest {
    private val managers = ManagerRoster(listOf(HubManager("assistant", "Assistant", online = true, isDefault = true),
        HubManager("m-2", "Work", online = true), HubManager("m-3", "Home")), "assistant")

    private class Mutes : InboxRepository by FakeInboxRepository() {
        val unmuted = mutableListOf<String>()
        var failUnmute: Exception? = null
        override suspend fun inboxMutes(manager: String?): List<InboxMute> = when (manager) {
            "m-3" -> throw IllegalStateException("503")
            "m-2" -> listOf(InboxMute("u1", "github", tag = "repo", value = "o/two", name = "o/two", createdAt = "2026-02-01T00:00:00Z"))
            else -> listOf(InboxMute("u1", "github", tag = "repo", value = "o/one", name = "o/one", createdAt = "2026-01-01T00:00:00Z"))
        }
        override suspend fun unmute(id: String, manager: String?) {
            unmuted += "$manager:$id"
            failUnmute?.let { throw it }
        }
    }

    @Test fun eachManagersMutesAreReadUnderItsNameAndTakenBackOnIt() = runBlocking {
        val repository = Mutes()
        val model = InboxMutesModel(repository)
        model.load(managers)
        val shares = checkNotNull(model.shares)
        assertEquals(listOf(listOf("o/one"), listOf("o/two"), emptyList()), shares.map { s -> s.group.items.map { it.name } })
        assertEquals(listOf("assistant", "m-2"), shares.flatMap { s -> s.group.items.map { it.manager } })
        assertEquals("503", shares[2].failed?.message)
        assertEquals(listOf("o/two", "o/one"), model.mutes?.map { it.name })

        model.unmute(shares[1].group.items.single())
        assertEquals(listOf("assistant", "m-3"), model.shares?.filter { it.shown }?.map { it.group.id })
        assertEquals(listOf("o/one"), model.mutes?.map { it.name }) // the default's mute with the same id stays

        repository.failUnmute = IllegalStateException("offline")
        val left = checkNotNull(model.shares)[0].group.items.single()
        try { model.unmute(left); fail("a failed unmute throws") } catch (e: IllegalStateException) { assertEquals("offline", e.message) }
        assertEquals(listOf(listOf(left), emptyList(), emptyList()), model.shares?.map { it.group.items })
        assertEquals(listOf("m-2:u1", "assistant:u1"), repository.unmuted)
    }

    @Test fun aReadOverTakenByANewerOneDoesntLand() = runBlocking {
        val first = CompletableDeferred<Unit>()
        var calls = 0
        val repository = object : InboxRepository by Mutes() {
            override suspend fun inboxMutes(manager: String?): List<InboxMute> {
                if (calls++ == 0) {
                    first.await()
                    return listOf(InboxMute("old", "github", tag = "repo", value = "o/old", name = "o/old"))
                }
                return listOf(InboxMute("new", "github", tag = "repo", value = "o/new", name = "o/new"))
            }
        }
        val model = InboxMutesModel(repository)
        val before = launch { model.load(null) } // the managers changed while it was on its way
        yield()
        model.load(null)
        first.complete(Unit)
        before.join()
        assertEquals(listOf("new"), model.mutes?.map { it.id })
    }

    @Test fun aFailedReadOverTakenByANewerOneSaysNothing() = runBlocking {
        val first = CompletableDeferred<Unit>()
        var calls = 0
        val repository = object : InboxRepository by Mutes() {
            override suspend fun inboxMutes(manager: String?): List<InboxMute> {
                if (calls++ == 0) {
                    first.await()
                    throw IllegalStateException("timeout")
                }
                return listOf(InboxMute("new", "github", tag = "repo", value = "o/new", name = "o/new"))
            }
        }
        val model = InboxMutesModel(repository)
        var thrown: Throwable? = null
        val before = launch { try { model.load(null) } catch (e: IllegalStateException) { thrown = e } }
        yield()
        model.load(null)
        first.complete(Unit)
        before.join()
        assertNull(thrown)
        assertEquals(listOf("new"), model.mutes?.map { it.id })
    }

    @Test fun anUnmuteStaysGoneWhenAReloadReadBeforeItLands() = runBlocking {
        val answer = CompletableDeferred<Unit>()
        val repository = object : InboxRepository by Mutes() {
            override suspend fun unmute(id: String, manager: String?) = answer.await()
        }
        val model = InboxMutesModel(repository)
        model.load(managers)
        val gone = checkNotNull(model.shares)[1].group.items.single()
        val unmuting = launch { model.unmute(gone) }
        yield()
        model.load(managers) // read while the unmute was on its way: it still has the mute
        assertEquals(listOf("o/two", "o/one"), model.mutes?.map { it.name })
        answer.complete(Unit)
        unmuting.join()
        assertEquals(listOf("o/one"), model.mutes?.map { it.name })
        assertEquals(listOf(listOf("o/one"), emptyList(), emptyList()), model.shares?.map { s -> s.group.items.map { it.name } })
    }

    @Test fun withOneManagerTheMutesAreOneListAsBefore() = runBlocking {
        val model = InboxMutesModel(Mutes())
        model.load(ManagerRoster(listOf(managers.managers.first()), "assistant"))
        assertNull(model.shares)
        assertEquals(listOf<String?>(null), model.mutes?.map { it.manager })
        model.load(null)
        assertTrue(model.mutes?.size == 1 && model.shares == null)
    }
}
