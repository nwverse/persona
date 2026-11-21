package dev.persona.watch.pet

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Test

class PetPublicationTest {
    private val stored = PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 42), 1234)

    @Test fun statePreferenceNamespaceUsesExactCredentialInsteadOfAssetScope() {
        val config = HubConfig("http://hub.local", "sample-token")
        val replacement = config.copy(token = "replacement-sample-token")
        assertEquals("pet_state_${petStatusScope(config)}", petStatusPreferencesName(config))
        assertNotEquals("pet_${PetPack.scope(config)}", petStatusPreferencesName(config))
        assertEquals(PetPack.scope(config), PetPack.scope(replacement))
        assertNotEquals(petStatusPreferencesName(config), petStatusPreferencesName(replacement))
    }

    @Test fun changedAccountCannotReadOldCountsEvenWhenItsArtworkIsReusable() {
        val now = 10_000_000L
        val original = HubConfig("https://hub.example", "sample-old", "A".repeat(43), "sample-device-old")
        val oldState = PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 42), now)
        val empty = PetStoredStatus(PetStatus(PetState.OFFLINE), 0)
        val manifest = PetManifest(emptyMap(), emptyMap(), "{}")
        val namespaces = mutableMapOf(petStatusPreferencesName(original) to oldState)
        // These are the same credential-scoped names used by PetBridge's preference reads/writes.
        fun read(config: HubConfig) = namespaces[petStatusPreferencesName(config)] ?: empty
        val replacements = listOf(original.copy(token = "sample-new"), original.copy(deviceId = "sample-device-new"),
            original.copy(token = "sample-new", deviceId = "sample-device-new"))
        for (next in replacements) {
            assertEquals(PetPack.scope(original), PetPack.scope(next))
            assertEquals(oldState, read(original))
            assertEquals(empty, read(next))
            val page = PetPageState()
            page.resume(original, read(original), now)
            val oldTicket = page.generation
            page.resume(next, read(next), now)
            assertTrue(page.acceptManifest(page.generation, manifest))
            assertFalse(page.acceptRefresh(oldTicket, manifest, null, 1, now))
            assertEquals(empty, page.stored)
            assertTrue(page.acceptRefresh(page.generation, manifest, null, 1, now))
            assertEquals(empty, page.stored) // Artwork or a failed read does not authenticate old task state.
        }
    }

    @Test fun lateOldCredentialPublicationCannotOverwriteTheNewIdentityNamespace() {
        val first = HubConfig("https://hub.example", "sample-old", deviceId = "sample-device")
        val next = first.copy(token = "sample-new")
        val nextState = PetStoredStatus(PetStatus(PetState.WORKING, 1), 10_000)
        val namespaces = mutableMapOf(petStatusPreferencesName(next) to nextState)
        namespaces[petStatusPreferencesName(first)] = stored
        assertEquals(nextState, namespaces[petStatusPreferencesName(next)])
        assertEquals(stored, namespaces[petStatusPreferencesName(first)])
        assertEquals(2, namespaces.size)
    }

    @Test fun successfulCommitPrecedesNotificationAndRestoreBackupCleanup() {
        val calls = mutableListOf<String>()
        val result = commitPetPublication(stored, commit = {
            assertEquals(stored, it); calls += "commit"; true
        }, requestUpdate = { calls += "notify" })
        calls += "delete-backup"
        assertEquals(stored, result)
        assertEquals(listOf("commit", "notify", "delete-backup"), calls)
    }

    @Test fun failedCommitPreventsNotificationAndKeepsRestoreBackup() {
        val calls = mutableListOf<String>()
        assertThrows(IllegalStateException::class.java) {
            commitPetPublication(stored, commit = { calls += "commit"; false }, requestUpdate = { calls += "notify" })
            calls += "delete-backup"
        }
        assertEquals(listOf("commit"), calls)
    }

    @Test fun throwingCommitAlsoPreventsNotification() {
        var notified = false
        val failure = IllegalStateException("sample storage failure")
        assertSame(failure, assertThrows(IllegalStateException::class.java) {
            commitPetPublication(stored, commit = { throw failure }, requestUpdate = { notified = true })
        })
        assertFalse(notified)
    }

    @Test fun sameStateRequestsUpdateWhenAnExpiredInputDropsOutOfTheAggregate() {
        val now = 10_000_000L
        val previous = PetStoredStatus(PetStatus(PetState.WORKING, 1), now - PetStoredStatus.STALE_MS)
        val next = previous.copy(updatedAt = now - 1)
        // No state/count change and a recent request, but the installed timeline has expired.
        assertTrue(petPublicationNeedsUpdate(previous, next, now - 10, now))
        assertFalse(petPublicationNeedsUpdate(previous, next, now - 10, now - 1))
    }

    @Test fun unchangedFreshPublicationKeepsThrottleAndOtherChangesStillNotify() {
        val now = 10_000_000L
        val previous = PetStoredStatus(PetStatus(PetState.WORKING, 1), now - 100)
        val next = previous.copy(updatedAt = now)
        assertFalse(petPublicationNeedsUpdate(previous, next, now - 10, now))
        assertTrue(petPublicationNeedsUpdate(previous, next, now - 20 * 60 * 1000 - 1, now))
        assertTrue(petPublicationNeedsUpdate(previous, next.copy(status = PetStatus(PetState.WORKING, 2)), now, now))
        assertTrue(petPublicationNeedsUpdate(previous, next.copy(status = PetStatus(PetState.IDLE)), now, now))
        val expired = previous.copy(status = PetStatus(PetState.OFFLINE), updatedAt = now - PetStoredStatus.STALE_MS)
        assertFalse(petPublicationNeedsUpdate(expired, expired, now, now))
    }
}
