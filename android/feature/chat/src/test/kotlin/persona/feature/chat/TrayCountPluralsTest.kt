package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** The assistant trays word their item and step counts in the English plural form (#3886). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class TrayCountPluralsTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun itemCount() {
        assertEquals("1 item", res.getQuantityString(R.plurals.chat_tray_items, 1, 1))
        assertEquals("3 items", res.getQuantityString(R.plurals.chat_tray_items, 3, 3))
    }

    @Test fun stepsFollowTheTotal() {
        assertEquals("0 / 1 step", res.getQuantityString(R.plurals.chat_tray_steps, 1, 0, 1))
        assertEquals("1 / 4 steps", res.getQuantityString(R.plurals.chat_tray_steps, 4, 1, 4))
    }
}
