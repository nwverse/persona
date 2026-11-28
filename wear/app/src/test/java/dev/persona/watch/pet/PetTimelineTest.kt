package dev.persona.watch.pet

import org.junit.Assert.*
import org.junit.Test

class PetTimelineTest {
    private val observedAt = 10_000_000L
    private val stored = PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 4), observedAt)

    @Test fun requestHalfwayThroughFreshnessRetainsTheActualObservationStart() {
        val halfway = observedAt + PetStoredStatus.STALE_MS / 2
        val interval = checkNotNull(stored.freshInterval(halfway))
        assertEquals(observedAt, interval.startMs)
        assertEquals(observedAt + PetStoredStatus.STALE_MS, interval.endExclusiveMs)
        for (time in listOf(observedAt, halfway - 1, halfway, interval.endExclusiveMs - 1)) {
            assertTrue(time >= interval.startMs && time < interval.endExclusiveMs)
            assertEquals(stored.status, stored.visible(time))
        }
    }

    @Test fun repeatedRequestsCannotMoveEitherBoundaryOrExtendExpiry() {
        val expected = PetFreshInterval(observedAt, observedAt + PetStoredStatus.STALE_MS)
        for (time in listOf(observedAt, observedAt + 1, expected.endExclusiveMs - 1)) {
            assertEquals(expected, stored.freshInterval(time))
        }
        assertNull(stored.freshInterval(expected.endExclusiveMs))
        assertEquals(PetStatus(PetState.OFFLINE), stored.visible(expected.endExclusiveMs))
        assertNull(stored.freshInterval(expected.endExclusiveMs + 1))
    }

    @Test fun toleratedClockSkewStillCoversNowWithoutChangingTheOriginalDeadline() {
        val now = observedAt - 60_000
        val interval = checkNotNull(stored.freshInterval(now))
        assertEquals(now, interval.startMs)
        assertEquals(observedAt + PetStoredStatus.STALE_MS, interval.endExclusiveMs)
        assertEquals(stored.status, stored.visible(now))
        assertNull(stored.freshInterval(now - 1))
    }

    @Test fun missingInvalidAndOfflineStatusCannotCreateAFreshEntry() {
        for (date in listOf(-1L, 0L, observedAt + 60_001, Long.MAX_VALUE)) {
            assertNull(stored.copy(updatedAt = date).freshInterval(observedAt))
        }
        assertNull(stored.freshInterval(-1))
        assertNull(stored.copy(status = PetStatus(PetState.OFFLINE)).freshInterval(observedAt))
    }
}
