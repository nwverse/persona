package persona.platform.notify

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.SessionKey

class ResidentConnectionRequestsTest {
    @Test fun anEnabledVisibleCapsuleCanStartWithoutAnotherBackgroundSwitch() {
        assertTrue(mayStartResidentConnection(true, false, false, false, true, true))
        assertFalse(mayStartResidentConnection(true, false, false, false, true, false))
    }

    @Test fun userStopAndUnpairOverrideEveryAutomaticStartReason() {
        assertFalse(mayStartResidentConnection(true, true, true, true, true, true))
        assertFalse(mayStartResidentConnection(false, false, true, true, true, true))
    }

    @Test fun turningOffTheCapsuleRetainsTheExistingConnectionReasons() {
        assertTrue(mayStartResidentConnection(true, false, true, false, false, false))
        assertTrue(mayStartResidentConnection(true, false, false, true, false, false))
        assertFalse(mayStartResidentConnection(true, false, false, false, false, true))
    }

    @Test fun leavingTheAppRetainsItsLastExplicitConversationForTheCapsule() {
        val key = SessionKey("machine~g0a1b2c3d", "conversation")
        Notify.viewing(key)
        Notify.viewing(null)
        assertNull(Notify.visibleSession.value)
        assertEquals(key, Notify.viewedSession)
    }
}
