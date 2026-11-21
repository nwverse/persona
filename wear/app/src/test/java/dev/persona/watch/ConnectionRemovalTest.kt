package dev.persona.watch

import org.junit.Assert.*
import org.junit.Test

class ConnectionRemovalTest {
    @Test fun failedRemovalSurvivesSuccessfulAsynchronousRecoveryAndRowReload() {
        var displayed = listOf("removed", "retained")
        var error = ""
        var finishReload: (() -> Unit)? = null
        var feedback: ConnectionReloadFeedback? = null
        val result = Result.failure<Unit>(IllegalStateException("credential cleanup failed"))
        completeConnectionRemoval(result, showError = { error = "failed" }, reload = {
            finishReload = {
                displayed = listOf("retained")
                feedback = connectionReloadFeedback(result.isFailure, Result.success(true), false)
                error = feedback!!.notice.name
            }
        })
        assertEquals("failed", error)
        assertEquals(listOf("removed", "retained"), displayed)
        finishReload!!()
        assertEquals(listOf("retained"), displayed)
        assertEquals("FAILURE", error)
        assertTrue(feedback!!.retry)
    }

    @Test fun failedRemovalRetainsFailureAndRetryWhenRecoveryAlsoFails() {
        val feedback = connectionReloadFeedback(true, Result.failure(IllegalStateException("cleanup still pending")), false)
        assertEquals(ConnectionReloadNotice.FAILURE, feedback.notice)
        assertTrue(feedback.retry)
    }

    @Test fun explicitSuccessfulRetryClearsTheRetainedRemovalFailure() {
        assertTrue(connectionReloadFeedback(true, Result.success(false), false).retry)
        val retried = connectionReloadFeedback(false, Result.success(false), false)
        assertEquals(ConnectionReloadNotice.NONE, retried.notice)
        assertFalse(retried.retry)
    }

    @Test fun reloadReadAndRecoveryFailuresRemainRetryableWithoutARemoval() {
        assertEquals(ConnectionReloadFeedback(ConnectionReloadNotice.FAILURE, true),
            connectionReloadFeedback(false, Result.success(false), true))
        assertEquals(ConnectionReloadFeedback(ConnectionReloadNotice.PENDING, true),
            connectionReloadFeedback(false, Result.failure(IllegalStateException("pending registration")), false))
        assertEquals(ConnectionReloadFeedback(ConnectionReloadNotice.RECOVERED, false),
            connectionReloadFeedback(false, Result.success(true), false))
    }

    @Test fun successfulRemovalReloadsPersistedRowsWithoutAssigningAnError() {
        val persisted = mutableListOf("removed", "retained")
        var displayed = persisted.toList()
        var error = "previous status"
        var reloads = 0
        val result = runCatching { persisted.remove("removed"); Unit }

        completeConnectionRemoval(result,
            showError = { error = "failed" },
            reload = { reloads++; displayed = persisted.toList() })

        assertEquals(listOf("retained"), displayed)
        assertEquals("previous status", error)
        assertEquals(1, reloads)
    }

    @Test fun failedRemovalReloadsRetainedRowsWithTheFailureAlreadyVisible() {
        val persisted = listOf("retained")
        var displayed = emptyList<String>()
        var error = ""
        val events = mutableListOf<String>()

        completeConnectionRemoval(Result.failure(IllegalStateException("directory write failed")),
            showError = { error = "failed"; events += "error" },
            reload = {
                assertEquals("failed", error)
                events += "reload"
                displayed = persisted.toList()
            })

        assertEquals(persisted, displayed)
        assertEquals(listOf("error", "reload"), events)
    }

    @Test fun failureAfterPersistedRemovalStillRemovesTheVisibleRowOnReload() {
        val persisted = mutableListOf("removed", "retained")
        var displayed = persisted.toList()
        var error = ""
        var reloads = 0
        val result = runCatching<Unit> {
            persisted.remove("removed")
            throw IllegalStateException("credential cleanup failed")
        }

        completeConnectionRemoval(result,
            showError = { error = "failed" },
            reload = { reloads++; displayed = persisted.toList() })

        assertEquals(listOf("retained"), displayed)
        assertEquals("failed", error)
        assertEquals(1, reloads)
    }
}
