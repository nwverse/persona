package persona.feature.settings

import org.junit.Assert.*
import org.junit.Test

class ProtocolCompatibilityTest {
    @Test fun baselineExtensionSourceLinksUseTheSameInAppTargetAsCurrentLinks() {
        for (scheme in listOf("jarvis", "persona")) {
            assertEquals("test:item", ExtensionViewText.inboxSource("$scheme://inbox/test%3Aitem/source"))
            assertNull(ExtensionViewText.inboxSource("$scheme://user@example.com/inbox/test/source"))
            assertNull(ExtensionViewText.inboxSource("$scheme://inbox/test/source?other=1"))
        }
        assertNull(ExtensionViewText.inboxSource("other://inbox/test/source"))
    }

    @Test fun spaceConversationLinkStaysWithinItsExtension() {
        val link = "persona://tracks/computer-example/reading/session-example"
        assertEquals("computer-example" to "session-example", ExtensionViewText.spaceConversation(link))
        assertTrue(ExtensionViewText.opens("space-conversations", link))
        assertFalse(ExtensionViewText.opens("other", link))
        assertNull(ExtensionViewText.spaceConversation("persona://tracks/computer-example/health/session-example"))
        assertNull(ExtensionViewText.spaceConversation("persona://settings/extensions/space-conversations"))
        assertNull(ExtensionViewText.spaceConversation("https://example.com/tracks/computer-example/reading/session-example"))
    }

    @Test fun settingsLinkOpensOnlyThePagesOwnExtension() {
        for (scheme in listOf("jarvis", "persona")) {
            assertEquals("example-notes", ExtensionViewText.settings("example-notes", "$scheme://settings/extensions/example-notes"))
            assertEquals("a/b c", ExtensionViewText.settings("a/b c", "$scheme://settings/extensions/a%2Fb%20c"))
            assertTrue(ExtensionViewText.opens("example-notes", "$scheme://settings/extensions/example-notes"))
        }
        assertNull(ExtensionViewText.settings("example-notes", "persona://settings/extensions/another"))
        assertNull(ExtensionViewText.settings("example-notes", "persona://settings/extensions/example-notes/extra"))
        assertNull(ExtensionViewText.settings("example-notes", "persona://settings/extensions/example-notes?x=1"))
        assertNull(ExtensionViewText.settings("example-notes", "https://settings/extensions/example-notes"))
        assertFalse(ExtensionViewText.opens("", "persona://settings/extensions/example-notes"))
    }
}
