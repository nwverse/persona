package dev.persona.watch.pet

import org.junit.Assert.*
import org.junit.Test

class PetForegroundPollTest {
    @Test fun ambientWakesKeepTheChainAndTheNextInteractiveWakeRefreshes() {
        var foreground = true
        var interactive = false
        var reads = 0
        val pending = ArrayDeque<Runnable>()
        val delays = mutableListOf<Long>()
        val poll = PetForegroundPoll({ foreground }, { interactive }, { reads++ }, { task, delay ->
            pending += task; delays += delay
        })
        poll.run()
        repeat(2) { assertSame(poll, pending.removeFirst().also { it.run() }) }
        assertEquals(0, reads)
        assertEquals(1, pending.size)
        interactive = true
        pending.removeFirst().run()
        assertEquals(1, reads)
        assertEquals(1, pending.size)
        assertEquals(List(4) { PetTokens.pollMs }, delays)
        foreground = false
        pending.removeFirst().run()
        assertTrue(pending.isEmpty())
        assertEquals(1, reads)
    }

    @Test fun aPausedOrDestroyedPageDoesNotReadOrSchedule() {
        for (interactive in listOf(false, true)) {
            PetForegroundPoll({ false }, { interactive }, { fail("No background reads") },
                { _, _ -> fail("No background scheduling") }).run()
        }
    }

    @Test fun foregroundLostDuringRefreshDoesNotRearmAfterPauseCleanup() {
        var foreground = true
        var reads = 0
        PetForegroundPoll({ foreground }, { true }, { reads++; foreground = false },
            { _, _ -> fail("Pause must leave the chain stopped") }).run()
        assertEquals(1, reads)
    }
}
