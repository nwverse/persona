package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Test

/** [compactCount]: thousands step up to millions as the web and iOS do (#4150); ten-thousands stay ten-thousands. */
class CompactCountTest {
    private fun english(count: Long) = compactCount(count, 1_000, { "${it}K" }, { "${it}M" })
    private fun chinese(count: Long) = compactCount(count, 10_000, { "$it 万" })

    @Test fun thousandsReadAsBeforeUnderAMillion() {
        assertEquals("950", english(950))
        assertEquals("1K", english(1_000))
        assertEquals("12.3K", english(12_345))
        assertEquals("180.9K", english(180_902))
        assertEquals("500K", english(500_000))
        assertEquals("999.9K", english(999_949))
    }

    @Test fun thousandsStepUpToMillions() {
        assertEquals("1M", english(999_950))
        assertEquals("1M", english(999_999))
        assertEquals("1M", english(1_000_000))
        assertEquals("1.2M", english(1_200_000))
        assertEquals("1.3M", english(1_250_000))
        assertEquals("12.3M", english(12_345_678))
        assertEquals("1500M", english(1_500_000_000))
    }

    @Test fun withoutWordsForMillionsThousandsStay() {
        assertEquals("1200K", compactCount(1_200_000, 1_000, { "${it}K" }))
    }

    @Test fun tenThousandsHaveNoLargerStep() {
        assertEquals("9500", chinese(9_500))
        assertEquals("1.2 万", chinese(12_345))
        assertEquals("100 万", chinese(999_950))
        assertEquals("120 万", chinese(1_200_000))
        assertEquals("10000 万", chinese(100_000_000))
        assertEquals("1.2 万", compactCount(12_345, 10_000, { "$it 万" }, { "${it}M" }))
        assertEquals("120 万", compactCount(1_200_000, 10_000, { "$it 万" }, { "${it}M" }))
    }
}
