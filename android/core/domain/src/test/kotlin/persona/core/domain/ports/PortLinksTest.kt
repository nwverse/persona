package persona.core.domain.ports

import persona.core.domain.zhAny

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** docs/operations/ports.md "Recognizing links in replies", the cases of iOS `PortLinksTests`. */
class PortLinksTest {
    @Test fun thisMachinesHostsWithAPort() {
        assertEquals(PortLink(5173, "/", false), PortLinks.link("http://localhost:5173/"))
        assertEquals(PortLink(5173, "/", false), PortLinks.link("http://localhost:5173"))
        assertEquals(PortLink(8000, "/docs?x=1#top", false), PortLinks.link("http://127.0.0.1:8000/docs?x=1#top"))
        assertEquals(PortLink(3000, "/", false), PortLinks.link("http://0.0.0.0:3000"))
        assertEquals(PortLink(4173, "/a", false), PortLinks.link("http://[::1]:4173/a"))
        assertEquals(PortLink(443, "/", true), PortLinks.link("https://app.localhost:443/"))
        assertEquals(PortLink(3000, "/", false), PortLinks.link("http://MacBook.local:3000/", listOf("macbook")))
        assertEquals(PortLink(3000, "/", false), PortLinks.link("http://macbook:3000/", listOf("MacBook.local")))
    }

    @Test fun otherLinksAreNot() {
        assertNull(PortLinks.link("http://localhost/")) // no port: not a dev server's link
        assertNull(PortLinks.link("http://192.0.2.5:3000/")) // a LAN address opens as a web page
        assertNull(PortLinks.link("https://example.com:8443/"))
        assertNull(PortLinks.link("http://other:3000/", listOf("macbook")))
        assertNull(PortLinks.link("ftp://localhost:21/"))
        assertNull(PortLinks.link("http://localhost:70000/"))
    }

    @Test fun findsThemInTextIncludingBareOnes() {
        val text = "Local:   http://localhost:5173/\n  Network: http://192.0.2.5:5173/\nAPI at 127.0.0.1:8000/docs, and http://localhost:5173/ again."
        assertEquals(listOf(PortLink(5173, "/", false), PortLink(8000, "/docs", false)), PortLinks.find(text))
        assertEquals(emptyList<PortLink>(), (PortLinks.find("no links here")).zhAny())
        // A bare host inside a longer URL is not another link.
        assertEquals(listOf(PortLink(3000, "/x", false)), PortLinks.find("see http://localhost:3000/x."))
    }
}
