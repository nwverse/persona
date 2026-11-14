package persona.feature.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.ReadSettingsState

/** Settings › Agents › Manual mark as read (#7356): when the switch can be flipped, and what its footer says. */
class ManualReadSectionTest {
    @Test fun offUntilTheHubAnswersAndFlippableOnlyWhereItMayBeWritten() {
        val unknown = ReadSettingsState()
        assertFalse(unknown.manual)
        assertFalse(manualReadEditable(unknown))
        assertTrue(manualReadEditable(ReadSettingsState(manual = false, canWrite = true, supported = true, known = true)))
        assertFalse(manualReadEditable(ReadSettingsState(manual = true, canWrite = false, supported = true, known = true)))
        assertFalse(manualReadEditable(ReadSettingsState(supported = false, known = true)))
    }

    @Test fun theFooterSaysWhyItCannotBeChanged() {
        assertEquals(R.string.settings_manual_read_footer, manualReadFooter(ReadSettingsState(canWrite = true, known = true)))
        assertEquals(R.string.settings_manual_read_readonly, manualReadFooter(ReadSettingsState(canWrite = false, known = true)))
        assertEquals(R.string.settings_manual_read_old_hub, manualReadFooter(ReadSettingsState(supported = false, known = true)))
        // before the hub answered: what it does, not a refusal
        assertEquals(R.string.settings_manual_read_footer, manualReadFooter(ReadSettingsState()))
    }
}
