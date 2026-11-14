package persona.feature.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ProviderImportLinkTest {
    @Test fun takingAnImportRequestDropsItSoItCannotReplay() {
        val link = "persona://provider/import?v=1&data=FAKE-EXAMPLE-KEY"
        ProviderImportRequests.pending.value = link
        assertEquals(link, ProviderImportRequests.take())
        assertNull("the request leaves no key-bearing link behind", ProviderImportRequests.pending.value)
        assertNull("a later providers page cannot replay it", ProviderImportRequests.take())
    }
}
