package persona.platform.capsule

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class CapsuleCompletionQueueTest {
    private val one = CapsuleCompletion("one", "machine/first", "First task")
    private val two = CapsuleCompletion("two", "machine/second", "Second task")
    private val three = CapsuleCompletion("three", "other/third", "Third task")

    @Test fun noticeRetractsAfterFourSeconds() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        runCurrent()
        advanceTimeBy(3_999)
        assertEquals(one, queue.current.value)
        advanceTimeBy(1)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun simultaneousCompletionsAreFifoAndDuplicateIdsNeverExtendOrRepeatATimer() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        queue.offer(two)
        queue.offer(one.copy(title = "Duplicate title"))
        runCurrent()
        advanceTimeBy(4_000)
        runCurrent()
        assertEquals(two, queue.current.value)
        advanceTimeBy(4_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun manualCardDropsCurrentAndIncomingNoticesWithoutReplayingThem() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        queue.offer(two)
        runCurrent()
        advanceTimeBy(1_000)
        queue.setManual(true)
        queue.offer(three)
        assertNull(queue.current.value)
        queue.setManual(false)
        queue.offer(two)
        queue.offer(three)
        advanceTimeBy(10_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun aCanceledTimerCannotRetractANewerNoticeAfterManualUse() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        runCurrent()
        advanceTimeBy(1_000)
        queue.setManual(true)
        queue.setManual(false)
        queue.offer(two)
        runCurrent()
        advanceTimeBy(3_000)
        runCurrent()
        assertEquals(two, queue.current.value)
        advanceTimeBy(1_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun hidingClearsTheQueueAndOnlyTheProvidersUnshownPendingEventCanRestore() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        queue.offer(two)
        runCurrent()
        queue.noticeShown(one.id)
        queue.setVisible(false)
        queue.offer(three)
        queue.setVisible(true)
        assertNull(queue.current.value)
        queue.offer(one)
        assertNull(queue.current.value)
        queue.offer(three)
        assertEquals(three, queue.current.value)
        runCurrent()
        advanceTimeBy(4_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun aNoticeHiddenBeforeItsFirstDrawCanRestoreFromPersistentPending() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        queue.setVisible(false)
        queue.setVisible(true)
        queue.offer(one)
        assertEquals(one, queue.current.value)
        queue.close()
    }

    @Test fun readDeletedOrMutedTargetsDisappearWithoutOldTimersCuttingTheNextNotice() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        queue.offer(two)
        queue.offer(three)
        runCurrent()
        advanceTimeBy(1_000)
        queue.retainTargets(setOf(two.targetId))
        assertEquals(two, queue.current.value)
        runCurrent()
        advanceTimeBy(3_000)
        runCurrent()
        assertEquals(two, queue.current.value)
        advanceTimeBy(1_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun completionAndMessageKindsShareFifoDedupAndTheFourSecondTimer() = runTest {
        val queue = CapsuleCompletionQueue(this)
        val message = two.copy(kind = CapsuleNoticeKind.NewMessage)
        queue.setVisible(true)
        queue.offer(one)
        queue.offer(message)
        runCurrent()
        advanceTimeBy(4_000)
        runCurrent()
        assertEquals(message, queue.current.value)
        queue.offer(message)
        advanceTimeBy(4_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun clickTakesOnlyTheDisplayedTargetAndClearsEveryPendingCompletion() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        queue.offer(two)
        queue.offer(three)
        runCurrent()
        advanceTimeBy(4_000)
        runCurrent()
        assertNull(queue.take(one.id))
        assertEquals(two, queue.current.value)
        val clicked = queue.take(two.id)
        assertEquals("machine/second", clicked?.targetId)
        assertNull(queue.current.value)
        advanceTimeBy(20_000)
        runCurrent()
        assertNull(queue.current.value)
    }

    @Test fun closeCancelsTheTimerAndRejectsFutureOffersAndVisibilityChanges() = runTest {
        val queue = CapsuleCompletionQueue(this)
        queue.setVisible(true)
        queue.offer(one)
        runCurrent()
        queue.close()
        queue.setVisible(true)
        queue.offer(two)
        advanceTimeBy(10_000)
        runCurrent()
        assertNull(queue.current.value)
    }
}
