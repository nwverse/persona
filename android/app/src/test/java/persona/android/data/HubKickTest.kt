package persona.android.data

import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.api.ConnectionState

class HubKickTest {
    @Test
    fun comingToTheFrontLeavesAConnectionThatIsUpAlone() {
        val kicked = ConnectionState.entries.filter(::needsKick).toSet()
        // A syncing connection (Updating) is up: starting it over throws the sync away and flashes Connecting (#3737).
        assertEquals(ConnectionState.entries.toSet() - setOf(ConnectionState.Online, ConnectionState.Updating), kicked)
    }
}
