package persona.platform.phonetools

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class WorkoutSplitsTest {
    private val t0: Instant = Instant.parse("2026-11-15T06:00:00Z")
    private fun at(seconds: Long): Instant = t0.plusSeconds(seconds)

    /** Each 500 m sample takes 150 s, so a kilometre is two samples = 300 s. */
    private fun steady(count: Int): List<WorkoutSplits.Sample> = (0 until count).map { i ->
        WorkoutSplits.Sample(at(i * 150L), at((i + 1) * 150L), 500.0)
    }

    @Test fun wholeKilometresOnlyAndEachTakesItsOwnSeconds() {
        // Five samples = 2.5 km: two whole kilometres, the half kilometre is left out.
        val splits = WorkoutSplits.of(steady(5), at(0), at(750), emptyList())
        assertEquals(listOf(1 to 300.0, 2 to 300.0), splits.map { it.km to it.seconds })
    }

    @Test fun pausesAreLeftOutOfTheSeconds() {
        // A 60 s pause inside the first kilometre: that split takes 240 s of active time.
        val pause = listOf(at(100) to at(160))
        val splits = WorkoutSplits.of(steady(2), at(0), at(300), pause)
        assertEquals(240.0, splits.single().seconds, 0.0)
    }

    @Test fun aSampleOutsideTheWorkoutGivesNoSplitsAtAll() {
        val outside = steady(2) + WorkoutSplits.Sample(at(300), at(900), 500.0)
        assertTrue(WorkoutSplits.of(outside, at(0), at(600), emptyList()).isEmpty())
    }

    @Test fun aSplitShorterThanOneSecondGivesNoSplitsAtAll() {
        // 1 km covered in 0.5 s: a wrong pace is worse than none.
        val blink = listOf(WorkoutSplits.Sample(at(0), at(0).plusMillis(500), 1000.0))
        assertTrue(WorkoutSplits.of(blink, at(0), at(1), emptyList()).isEmpty())
    }

    @Test fun activeTimeIgnoresPausesOutsideTheInterval() {
        val pause = listOf(at(0) to at(10))
        assertEquals(20.0, WorkoutSplits.active(at(5), at(30), pause), 0.0)
        assertEquals(0.0, WorkoutSplits.active(at(30), at(5), pause), 0.0)
    }
}
