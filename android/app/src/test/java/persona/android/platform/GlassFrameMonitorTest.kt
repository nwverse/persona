package persona.android.platform

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class GlassFrameMonitorTest {
    @Test fun severeJankLowersEarlyWithoutWeakeningTheNormalWindow() {
        assertFalse(GlassFrameMonitor.shouldLower(frames = 119, janky = 72))
        assertFalse(GlassFrameMonitor.shouldLower(frames = 120, janky = 72))
        assertTrue(GlassFrameMonitor.shouldLower(frames = 120, janky = 73))
        assertFalse(GlassFrameMonitor.shouldLower(frames = 239, janky = 72))
        assertFalse(GlassFrameMonitor.shouldLower(frames = 240, janky = 72))
        assertTrue(GlassFrameMonitor.shouldLower(frames = 240, janky = 73))
    }
}
