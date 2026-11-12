package persona.platform.phonetools

import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(AndroidJUnit4::class)
class PhoneConfirmationsTest {
    private val context get() = ApplicationProvider.getApplicationContext<Context>()

    /**
     * minSdk is 30: the confirmation must post there too (setAuthenticationRequired is API 31, #4221). The unlock flag
     * does not exist on API 30, so only posting is checked there.
     */
    @Test @Config(sdk = [30]) fun postsTheConfirmationOnApi30() = postsAndDenies(expectAuthentication = false)

    @Test @Config(sdk = [35]) fun allowNeedsAnUnlockFromApi31() = postsAndDenies(expectAuthentication = true)

    private fun postsAndDenies(expectAuthentication: Boolean) = runTest {
        val confirmations = PhoneConfirmations(context)
        val answer = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        val token = confirmations.pending.value?.token
        assertNotNull("confirmation should be pending", token)
        val posted = shadowOf(context.getSystemService(NotificationManager::class.java)).allNotifications
        assertEquals(1, posted.size)
        val actions = posted.single().actions
        assertEquals(2, actions.size)
        if (expectAuthentication) {
            assertFalse(actions[0].isAuthenticationRequired)
            assertTrue(actions[1].isAuthenticationRequired)
        }
        confirmations.resolve(token!!, false)
        assertEquals(PhoneConfirmation.Denied, answer.await())
        confirmations.close()
    }

    private val notifications get() = shadowOf(context.getSystemService(NotificationManager::class.java))

    @Test @Config(sdk = [35]) fun anAllowAnswersAllowed() = runTest {
        val confirmations = PhoneConfirmations(context)
        val answer = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        confirmations.resolve(confirmations.pending.value!!.token, true)
        assertEquals(PhoneConfirmation.Allowed, answer.await())
    }

    /** #1875: 90 seconds without an answer is not a Deny; the confirmation and its notification go. */
    @Test @Config(sdk = [35]) fun nobodyAnsweringIsUnanswered() = runTest {
        val confirmations = PhoneConfirmations(context)
        val answer = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        assertEquals(1, notifications.allNotifications.size)
        advanceTimeBy(PhoneConfirmations.TIMEOUT_MILLIS - 1)
        runCurrent()
        assertFalse(answer.isCompleted)
        advanceTimeBy(2)
        runCurrent()
        assertEquals(PhoneConfirmation.Unanswered, answer.await())
        assertNull(confirmations.pending.value)
        assertEquals(0, notifications.allNotifications.size)
    }

    /**
     * #3861: swiping the notification away ends the confirmation at once as Dismissed (not after 90 seconds, and not
     * as a Deny); the system's own timeout comes after the app's, so a timeout still reads as Unanswered.
     */
    @Test @Config(sdk = [35]) fun swipingTheNotificationAwayDismissesItAtOnce() = runTest {
        val confirmations = PhoneConfirmations(context)
        val answer = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        val notification = notifications.allNotifications.single()
        assertTrue(notification.timeoutAfter > PhoneConfirmations.TIMEOUT_MILLIS)
        assertNotNull("the notification needs a delete intent", notification.deleteIntent)
        // what the system sends when the user swipes the notification away
        PhoneConfirmationReceiver().onReceive(context, shadowOf(notification.deleteIntent).savedIntent)
        runCurrent()
        assertTrue("a dismissal must not wait for the timeout", answer.isCompleted)
        assertEquals(PhoneConfirmation.Dismissed, answer.await())
        assertNull(confirmations.pending.value)
        assertEquals(0, notifications.allNotifications.size)
    }

    /** A delete intent left over from an earlier confirmation does not end the current one. */
    @Test @Config(sdk = [35]) fun aStaleDismissalIsIgnored() = runTest {
        val confirmations = PhoneConfirmations(context)
        val first = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        val stale = shadowOf(notifications.allNotifications.single().deleteIntent).savedIntent
        confirmations.resolve(confirmations.pending.value!!.token, false)
        assertEquals(PhoneConfirmation.Denied, first.await())
        val second = async { confirmations.confirm { "Add another event" } }
        runCurrent()
        PhoneConfirmationReceiver().onReceive(context, stale)
        runCurrent()
        assertFalse(second.isCompleted)
        confirmations.close()
        assertEquals(PhoneConfirmation.Withdrawn, second.await())
    }

    @Test @Config(sdk = [35]) fun inTheBackgroundWithNotificationsOffItIsNotShown() = runTest {
        notifications.setNotificationsEnabled(false)
        val confirmations = PhoneConfirmations(context)
        assertEquals(PhoneConfirmation.NotShown, confirmations.confirm { "Add an event" })
        assertNull(confirmations.pending.value)
    }

    @Test @Config(sdk = [35]) fun leavingTheDialogWhenNoNotificationCanBePostedIsNotShown() = runTest {
        val confirmations = PhoneConfirmations(context)
        confirmations.setForeground(true)
        val answer = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        assertNotNull(confirmations.pending.value)
        assertEquals(0, notifications.allNotifications.size) // the dialog in the app asks
        notifications.setNotificationsEnabled(false)
        confirmations.setForeground(false)
        assertEquals(PhoneConfirmation.NotShown, answer.await())
    }

    @Test @Config(sdk = [35]) fun closingWithdrawsIt() = runTest {
        val confirmations = PhoneConfirmations(context)
        val answer = async { confirmations.confirm { "Add an event" } }
        runCurrent()
        confirmations.close()
        assertEquals(PhoneConfirmation.Withdrawn, answer.await())
    }
}
