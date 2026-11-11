package persona.platform.notify

import android.app.Application
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.Resolution
import persona.core.contracts.api.InteractionCenter

/**
 * #2483: a notification button checks a second tap against the same journal its claims go to, this phone's own storage,
 * so the check still holds after the app restarted between the taps; a request this phone never claimed stays the computer's.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class NotificationActionJournalTest {
    private val app = ApplicationProvider.getApplicationContext<Application>()
    private val request = Interaction("request", machineId = "machine", sessionId = "session", kind = "permission")

    @Test fun aSecondTapOnWhatThisPhoneAnsweredGoesByItsJournal() = runBlocking {
        var writes = 0
        val center = object : InteractionCenter {
            override val pending = MutableStateFlow(listOf(request))
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) { writes++; pending.value = emptyList() }
        }
        var claims = 0
        suspend fun tap(journal: NotificationActionJournal, id: String = request.id) =
            NotificationActionExecutor(center, journal, session = { null }, onClaimed = { claims++ }).execute(id, "allow", null, { false })
        val journal = NotificationActionJournal(app)
        assertFalse(journal.claimed(request.id))
        assertEquals(NotificationActionResult.Submitted, tap(journal))
        assertTrue(journal.claimed(request.id))
        assertEquals(NotificationActionResult.AnsweredHere, tap(journal))
        assertEquals(NotificationActionResult.AnsweredHere, tap(NotificationActionJournal(app)))
        assertEquals(NotificationActionResult.Gone, tap(journal, "other"))
        assertEquals(1, writes)
        assertEquals(1, claims)
    }
}
