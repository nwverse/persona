package persona.core.domain.answer

import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.JsonCodec
import persona.core.contracts.Question
import persona.core.contracts.QuestionOption

class ShadowAnswerIsolationTest {
    private val twoQuestions = Interaction(id = "example", kind = "question", questions = listOf(
        Question("One", options = listOf(QuestionOption("A"), QuestionOption("B"))),
        Question("Two", options = listOf(QuestionOption("C"), QuestionOption("D"))),
    ))

    @Test fun boundedQueueDropsOnlyNewDiagnosticsAndReleasesOldContent() {
        val release = CountDownLatch(1)
        val reports = mutableListOf<AnswerMismatch>()
        val shadow = ShadowAnswerAdapter(projectRust = {
            release.await(30, TimeUnit.SECONDS)
            ""
        }, sink = reports::add)
        val before = ShadowAnswerDiagnostics.droppedCount
        val accepted = CountDownLatch(1)
        try {
            val expected = KotlinAnswerAdapter().build(twoQuestions, AnswerAction.Advance,
                AnswerDraft(picks = mapOf("One" to setOf("A"))))
            val returned = shadow.build(twoQuestions, AnswerAction.Advance,
                AnswerDraft(picks = mapOf("One" to setOf("A"))))
            assertEquals(expected, returned)
            repeat(80) {
                shadow.build(twoQuestions, AnswerAction.Advance, AnswerDraft(picks = mapOf("One" to setOf("A"))))
                shadow.display("A")
            }
            assertTrue("At most the running job plus 32 queued inputs may be retained", ShadowAnswerDiagnostics.queuedCount <= 32)
            assertTrue(ShadowAnswerDiagnostics.droppedCount > before)
            ShadowAnswerDiagnostics.testDrained = { accepted.countDown() }
        } finally {
            // A failed assertion must not strand a native test job and poison the shared test worker for later classes.
            release.countDown()
        }
        ShadowAnswerDiagnostics.awaitIdle()
        assertTrue(accepted.await(30, TimeUnit.SECONDS))
        ShadowAnswerDiagnostics.testDrained = null
        assertTrue(ShadowAnswerDiagnostics.queuedCount <= 32)
    }

    @Test fun advanceAndSubmitComparePicksAndOtherValuesExactlyWithoutLoggingThem() {
        val reports = mutableListOf<AnswerMismatch>()
        val original = AnswerDraft(page = 0, picks = mapOf("One" to setOf("A")), other = mapOf("One" to "custom"))
        val shadow = ShadowAnswerAdapter(projectRust = {
            """{"allowed":true,"page":1,"picks":{"One":["B"]},"other":{"One":"changed"},"resolution":null}"""
        }, sink = reports::add)
        val result = shadow.build(twoQuestions, AnswerAction.Advance, original)
        assertEquals(KotlinAnswerAdapter().build(twoQuestions, AnswerAction.Advance, original), result)
        ShadowAnswerDiagnostics.awaitIdle()
        assertEquals(setOf("picks", "other-state"), reports.map { it.rule }.toSet())
        reports.forEach {
            val json = JsonCodec.encodeToString(it)
            assertFalse(json.contains("custom"))
            assertFalse(json.contains("changed"))
            assertFalse(json.contains("One"))
        }
    }

    @Test fun defaultSinkCountsEachMismatchOnce() {
        val before = ShadowAnswerDiagnostics.mismatchCount
        val shadow = ShadowAnswerAdapter(displayRust = { """{"text":"different","recommended":false}""" })
        shadow.display("A")
        ShadowAnswerDiagnostics.awaitIdle()
        assertEquals(before + 1, ShadowAnswerDiagnostics.mismatchCount)
    }

    @Test fun repeatedDisplayIsComparedOnceAndStillReturnsKotlin() {
        val calls = AtomicInteger()
        val shadow = ShadowAnswerAdapter(displayRust = {
            calls.incrementAndGet()
            """{"text":"different","recommended":false}"""
        })
        repeat(100) { assertEquals(OptionDisplay("A", false), shadow.display("A")) }
        ShadowAnswerDiagnostics.awaitIdle()
        assertEquals(1, calls.get())
    }
}
