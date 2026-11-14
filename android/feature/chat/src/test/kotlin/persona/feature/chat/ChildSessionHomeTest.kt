package persona.feature.chat

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Session
import persona.core.contracts.SessionKey

class ChildSessionHomeTest {
    @Test fun childLinksOpenTheParentAndRetainTheCardTargetBeforeDraftsAreCreated() {
        val key = SessionKey("m~g12345678", "child")
        val row = Session(key.machineId, key.sessionId, foldedUnder = "parent")
        assertEquals(ChildSessionHome(SessionKey(key.machineId, "parent"), "child"), childSessionHome(key, row))
        assertEquals(ChildSessionHome(key), childSessionHome(key, null))
        assertEquals(ChildSessionHome(key), childSessionHome(key, row.copy(machineId = "another")))
        assertEquals(ChildSessionHome(key), childSessionHome(key, row.copy(foldedUnder = key.sessionId)))
        assertEquals(ChildSessionHome(key), childSessionHome(key, row.copy(foldedUnder = null)))
    }
}
