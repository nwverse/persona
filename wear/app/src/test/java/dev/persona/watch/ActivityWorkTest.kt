package dev.persona.watch

import org.junit.Assert.*
import org.junit.Test

class ActivityWorkTest {
    @Test fun connectionChangeDiscardsOldMutationWithoutLeavingBusyOrClearingANewerOne() {
        val state = ActivityWork()
        val old = state.beginMutation()!!
        assertTrue(state.busy)
        state.connectionsChanged()
        assertFalse(state.busy)
        assertFalse(state.finishMutation(old))
        val next = state.beginMutation()!!
        assertFalse(state.finishMutation(old))
        assertTrue(state.busy)
        assertTrue(state.finishMutation(next))
        assertFalse(state.busy)
    }
    @Test fun pausedRefreshCannotCompleteOrCancelTheResumedRefresh() {
        val state = ActivityWork()
        val old = state.beginRefresh()!!
        state.pause()
        assertFalse(state.refreshing)
        val next = state.beginRefresh()!!
        assertFalse(state.finishRefresh(old))
        assertTrue(state.refreshing)
        assertTrue(state.finishRefresh(next))
        assertFalse(state.refreshing)
    }
    @Test fun destroyedAndRecreatedActivityDoesNotInheritBusyFlags() {
        val old = ActivityWork()
        val refresh = old.beginRefresh()!!
        old.connectionsChanged()
        assertFalse(old.finishRefresh(refresh))
        val recreated = ActivityWork()
        assertFalse(recreated.busy)
        assertFalse(recreated.refreshing)
        assertNotNull(recreated.beginRefresh())
    }
    @Test fun pauseDoesNotAbandonAMutationAndDuplicateStartsAreRejected() {
        val state = ActivityWork()
        val ticket = state.beginMutation()!!
        assertNull(state.beginMutation())
        assertNull(state.beginRefresh())
        state.pause()
        assertTrue(state.finishMutation(ticket))
        assertNotNull(state.beginRefresh())
    }
}
