package persona.platform.capsule

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** A capsule row's unread count is worded in the English plural form (#3887). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class CapsuleMessageCountTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun singularAndPlural() {
        assertEquals("1 message", res.getQuantityString(R.plurals.capsule_message_count, 1, 1))
        assertEquals("2 messages", res.getQuantityString(R.plurals.capsule_message_count, 2, 2))
    }
}
