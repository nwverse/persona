package dev.persona.watch

import org.junit.Assert.*
import org.junit.Test

class HubRemovalTest {
    private class Store : HubRemovalStore {
        var rows = listOf(SavedHub("one", "One", null), SavedHub("two", "Two", null))
        var durableRows = rows
        var journal = emptySet<String>()
        var durableJournal = journal
        val credentials = mutableSetOf("one", "two")
        var failPrepare = false
        var failGroups = false
        var failCredential = false
        var failCleanup = false
        override fun groups() = rows
        override fun pending() = journal
        override fun writePending(ids: Set<String>) {
            journal = ids
            check(if (ids.isEmpty()) !failCleanup else !failPrepare)
            durableJournal = ids
        }
        override fun writeGroups(groups: List<SavedHub>) {
            rows = groups
            check(!failGroups)
            durableRows = groups
        }
        override fun clearCredential(id: String) {
            check(!failCredential)
            credentials.remove(id)
        }
        fun restart() { rows = durableRows; journal = durableJournal }
    }

    @Test fun credentialDeleteFailureIsRecoverableAfterTheRowDisappears() {
        val store = Store().apply { failCredential = true }
        assertThrows(IllegalStateException::class.java) { HubRemoval(store).remove("one") }
        assertEquals(listOf("two"), store.durableRows.map { it.id })
        assertTrue("one" in store.credentials)
        assertEquals(setOf("one"), store.durableJournal)
        store.restart()
        store.failCredential = false
        assertTrue(HubRemoval(store).recover())
        assertEquals(setOf("two"), store.credentials)
        assertTrue(store.durableJournal.isEmpty())
        assertFalse(HubRemoval(store).recover())
    }

    @Test fun retryByIdWorksAfterItsDirectoryRowWasRemoved() {
        val store = Store().apply { failCredential = true }
        assertThrows(IllegalStateException::class.java) { HubRemoval(store).remove("one") }
        store.failCredential = false
        HubRemoval(store).remove("one")
        assertEquals(setOf("two"), store.credentials)
        assertTrue(store.durableJournal.isEmpty())
    }

    @Test fun failedDirectoryCommitDoesNotDeleteTheCredentialAndCanReplayAfterRestart() {
        val store = Store().apply { failGroups = true }
        assertThrows(IllegalStateException::class.java) { HubRemoval(store).remove("one") }
        assertEquals(2, store.durableRows.size)
        assertTrue("one" in store.credentials)
        store.restart()
        store.failGroups = false
        HubRemoval(store).recover()
        assertEquals(listOf("two"), store.durableRows.map { it.id })
        assertEquals(setOf("two"), store.credentials)
    }

    @Test fun cleanupFailureIsSafeToRepeatWithAnAlreadyDeletedCredential() {
        val store = Store().apply { failCleanup = true }
        assertThrows(IllegalStateException::class.java) { HubRemoval(store).remove("one") }
        assertFalse("one" in store.credentials)
        store.restart()
        store.failCleanup = false
        assertTrue(HubRemoval(store).recover())
        assertEquals(setOf("two"), store.credentials)
        assertTrue(store.durableJournal.isEmpty())
    }

    @Test fun failingToJournalNeverHidesTheRowOrDeletesItsCredential() {
        val store = Store().apply { failPrepare = true }
        assertThrows(IllegalStateException::class.java) { HubRemoval(store).remove("one") }
        assertEquals(2, store.durableRows.size)
        assertEquals(setOf("one", "two"), store.credentials)
        assertTrue(store.durableJournal.isEmpty())
    }
}
