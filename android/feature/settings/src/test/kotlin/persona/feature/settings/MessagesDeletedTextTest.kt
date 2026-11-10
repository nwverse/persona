package persona.feature.settings

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.ConnectorDataDeleted

/** The confirmation after deleting the hub's Messages data words each count in its plural form (#3733). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class MessagesDeletedTextTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun singularCountsInEnglish() {
        assertEquals("Deleted 1 conversation, 1 person and 1 inbox item", messagesDeletedText(res, ConnectorDataDeleted(1, 1, 1)))
    }

    @Test fun pluralCountsInEnglish() {
        assertEquals("Deleted 3 conversations, 0 people and 2 inbox items", messagesDeletedText(res, ConnectorDataDeleted(3, 0, 2)))
    }

    @Test fun missingCountsAreZero() {
        assertEquals("Deleted 0 conversations, 0 people and 0 inbox items", messagesDeletedText(res, ConnectorDataDeleted()))
    }
}
