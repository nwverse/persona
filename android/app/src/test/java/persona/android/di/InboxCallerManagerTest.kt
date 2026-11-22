package persona.android.di

import kotlinx.coroutines.CoroutineName
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.ManagerPins
import persona.core.contracts.api.RequestManager

class InboxCallerManagerTest {
    private suspend fun seen() = currentCoroutineContext().let { Triple(it[ManagerPins], it[RequestManager]?.id, it[CoroutineName]?.name) }

    @Test
    fun anItemPagesManagerReachesTheSharedInboxsCallsAndNothingElse() = runBlocking {
        val pairing = CoroutineScope(SupervisorJob() + CoroutineName("pairing"))
        try {
            val pins = checkNotNull(ManagerPins.of(listOf(listOf("lark", "moments")), "m-2", hubId = "hub"))
            val named = withContext(pins + RequestManager("m-3")) { pairing.awaitAsCaller { seen() } }
            assertEquals(Triple(pins, "m-3", "pairing"), named)
            assertEquals(Triple(null, null, "pairing"), pairing.awaitAsCaller { seen() })
            val pairingJob = checkNotNull(pairing.coroutineContext[Job])
            assertTrue(withContext(pins) { pairing.awaitAsCaller { currentCoroutineContext()[Job] in pairingJob.children } })
        } finally {
            pairing.cancel()
        }
    }
}
