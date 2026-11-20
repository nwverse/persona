package persona.platform.phonetools

import kotlinx.serialization.json.jsonObject
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.HealthWorkout

/** The native pages consume the same daily and workout records as the authorized phone tools. */
class HealthRecordsTest {
    @Test fun absentFailedAndRefusedMeasurementsStayApartFromARecordedZero() {
        val raw = JsonCodec.parseToJsonElement("""{"data":[{"date":"2026-10-01","steps":0,"sleep":7.5},{"date":"2026-10-02","steps":{"error":"sample failure"}}],"not_granted":["weight"]}""").jsonObject
        val result = healthTrends(raw)
        assertEquals(0.0, result.days.first().steps!!, 0.0)
        assertEquals(7.5, result.days.first().sleep!!, 0.0)
        assertNull(result.days.last().steps)
        assertNull(result.days.last().sleep)
        assertNull(result.days.first().weight)
        assertEquals(setOf("steps"), result.failed)
        assertEquals(setOf("weight"), result.notGranted)
    }

    @Test fun workoutStoresOnlySuppliedMeasurementsAndItsOptionalDetail() {
        val raw = """{"id":"sample","activity":"Walking","start":"2026-10-01T07:00:00Z","duration":1800,"distance":2000,"heart_rate":[{"minute":1,"bpm":112}],"splits":[{"km":1,"seconds":850}]}"""
        val result = JsonCodec.decodeFromString<HealthWorkout>(raw)
        assertEquals(1800.0, result.duration, 0.0)
        assertEquals(2000.0, result.distance!!, 0.0)
        assertEquals(112.0, result.heartRate.single().bpm, 0.0)
        assertEquals(1, result.splits.single().km)
        assertNull(result.energy)
        assertNull(result.maxHeartRate)
    }
}
