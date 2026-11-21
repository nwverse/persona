package dev.persona.watch

import org.junit.Assert.*
import org.junit.Test

class FixtureControlOriginTest {
    @Test fun acceptsOnlyTheExpectedLoopbackHostsWithDefaultOrFixturePort() {
        for (host in listOf("127.0.0.1", "localhost", "[::1]")) {
            for (port in listOf("", ":18765")) {
                for (root in listOf("", "/")) {
                    val parsed = FixtureControlOrigin.parse("http://$host$port$root")
                    for (path in listOf("/api/v1/health", "/__test")) {
                        val target = parsed.url(path)
                        assertEquals("http://$host$port$path", target.toExternalForm())
                        assertEquals(if (port.isEmpty()) -1 else 18765, target.port)
                        assertEquals(80, target.defaultPort)
                        assertNull(target.userInfo)
                    }
                }
            }
        }
    }

    @Test fun rebuildsCanonicalAuthorityFromParsedHostAndEffectivePort() {
        assertEquals("http://localhost:18765/__test", FixtureControlOrigin.parse("http://LOCALHOST:018765/").url("/__test").toExternalForm())
        assertEquals("http://[::1]:18765/__test", FixtureControlOrigin.parse("http://[::1]:018765/").url("/__test").toExternalForm())
    }

    @Test fun refusesUnexpectedOrInvalidPortsBeforeAnyRequestCanBePrepared() {
        for (host in listOf("127.0.0.1", "localhost", "[::1]")) {
            for (port in listOf(":", ":80", ":0", ":1", ":18764", ":18766", ":65535", ":65536", ":-1", ":+18765", ":2147483648", ":999999999999999999999")) {
                rejectedBeforeRequest("http://$host$port")
            }
        }
    }

    @Test fun rejectsUserInfoAndMalformedBracketedAuthoritiesBeforeAnyRequest() {
        for (authority in listOf("user@localhost:18765", "user:secret@[::1]:18765", "localhost:18765@elsewhere.example",
            "elsewhere.example@[::1]:18765", "[::1]:18765@localhost", "[::1]@localhost:18765", "localhost%40elsewhere.example",
            "[::1]18765", "[::1]:18765:80", "::1", "[[::1]]:18765", "[::1", "[::1%25zone]:18765", "[::1]:18765%40elsewhere.example")) {
            rejectedBeforeRequest("http://$authority")
        }
    }

    @Test fun rejectsOtherHostsSchemesAndNonRootComponents() {
        for (address in listOf("https://localhost:18765", "ftp://localhost:18765", "http:localhost", "//localhost:18765",
            "http://elsewhere.example:18765", "http://localhost.example:18765", "http://localhost.:18765", "http://127.1:18765",
            "http://2130706433:18765", "http://[::2]:18765", "http://localhost:18765/path", "http://localhost:18765/%2f",
            "http://localhost:18765?", "http://localhost:18765?next=elsewhere", "http://localhost:18765#", "http://localhost:18765#fragment")) {
            rejectedBeforeRequest(address)
        }
        val valid = FixtureControlOrigin.parse("http://localhost:18765")
        assertThrows(IllegalStateException::class.java) { valid.url("//elsewhere.example/__test") }
        assertThrows(IllegalStateException::class.java) { valid.url("/__test?next=elsewhere") }
    }

    private fun rejectedBeforeRequest(address: String) {
        var preparedRequests = 0
        val failure = assertThrows(IllegalStateException::class.java) {
            val origin = FixtureControlOrigin.parse(address)
            origin.url("/__test")
            preparedRequests++ // The caller can only open a connection/add the token after this boundary.
        }
        assertEquals(address, "fixture_control_requires_loopback_root", failure.message)
        assertEquals(address, 0, preparedRequests)
    }
}
