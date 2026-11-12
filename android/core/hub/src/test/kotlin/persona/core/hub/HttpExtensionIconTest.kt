package persona.core.hub

import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okio.Buffer
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.ExtensionIconPath
import java.util.concurrent.TimeUnit

class HttpExtensionIconTest {
    private val installed = "/api/v1/extensions/notes/-/icon?v=1.0.0"
    private val catalog = "/api/v1/extensions/-/catalog/official/notes/icon?sha256=" + "a".repeat(64)

    @Test fun onlyExactRawRelativeOrSameOriginIconPathsAreAuthenticated() {
        val api = HttpExtensionManagement({ _, _, _ -> error("No request expected") }, iconBase = { "http://hub.example/prefix/" })
        for (path in listOf(installed, catalog)) {
            assertTrue(ExtensionIconPath.matches(path))
            assertEquals(path, api.iconPath(path))
            assertEquals(path, api.iconPath("http://hub.example/prefix$path"))
        }
        for (value in listOf("", "//hub.example$installed", installed + "&token=anything", installed + "#fragment",
            installed.replace("notes", ".."), installed.replace("notes", "%2e%2e"), installed.replace("notes", "notes/../notes"),
            installed.replace("notes", "notes%2fextra"), installed.replace("/-/", "/"),
            "http://other.example/prefix$installed", "https://hub.example/prefix$installed", "http://hub.example:81/prefix$installed",
            "http://user:pass@hub.example/prefix$installed", "http://hub.example/prefix/x/..$installed",
            "https://images.example/logo.png", "/api/v1/extensions/notes/-/image/signed")) assertNull(value, api.iconPath(value))
        assertTrue(HttpExtensionManagement.isImagePath("/api/v1/extensions/notes/-/image/signed"))
        assertFalse(HttpExtensionManagement.isImagePath(installed))
    }

    @Test fun downloadsBothRoutesWithExistingAuthAndRefusesOtherTargetsAndBadResponses() = runBlocking {
        ProtocolCompatibilityMockServer().use { server ->
            val http = OkHttpClient.Builder().followRedirects(false).retryOnConnectionFailure(false).build()
            val api = HttpExtensionManagement({ path, method, body ->
                http.newCall(Request.Builder().url(server.url(path)).header("Authorization", "Bearer synthetic-icon-token")
                    .method(method, body).build()).awaitResponse(buffered = false)
            }, iconBase = { server.url("/").toString() })
            for (path in listOf(installed, catalog)) {
                server.enqueue(MockResponse().setHeader("Content-Type", "image/png").setBody("fixture-image"))
                assertArrayEquals("fixture-image".toByteArray(), api.icon(path))
                val sent = server.takeRequest()
                assertEquals(path, sent.path)
                assertEquals("Bearer synthetic-icon-token", sent.getHeader("Authorization"))
                assertFalse(sent.path!!.contains("synthetic-icon-token"))
            }
            for (path in listOf("https://other.example$installed", "//other.example$installed", "/api/v1/extensions/notes/-/image/signed")) {
                assertTrue(runCatching { api.icon(path) }.exceptionOrNull() is IllegalArgumentException)
            }
            assertEquals(2, server.requestCount)
            for (response in listOf(
                MockResponse().setResponseCode(404),
                MockResponse().setHeader("Content-Type", "text/html").setBody("not an image"),
                MockResponse().setHeader("Content-Type", "image/png").setBody(""),
                MockResponse().setHeader("Content-Type", "image/png").setHeader("Content-Length", HttpExtensionManagement.MaxImageBytes + 1),
                MockResponse().setResponseCode(302).setHeader("Location", "https://other.example/logo.png"),
                MockResponse().setHeader("Content-Type", "image/png").setChunkedBody(Buffer().write(ByteArray((HttpExtensionManagement.MaxImageBytes + 1).toInt())), 8192),
            )) {
                server.enqueue(response)
                assertTrue(runCatching { api.icon(installed) }.exceptionOrNull() is HubFailure)
                server.takeRequest()
            }
        }
    }

    @Test fun cancelledIconDownloadCancelsTheExistingHttpCall() = runBlocking {
        ProtocolCompatibilityMockServer().use { server ->
            val http = OkHttpClient.Builder().readTimeout(5, TimeUnit.SECONDS).build()
            var call: okhttp3.Call? = null
            val api = HttpExtensionManagement({ path, _, _ ->
                http.newCall(Request.Builder().url(server.url(path)).build()).also { call = it }.awaitResponse(buffered = false)
            })
            server.enqueue(MockResponse().setHeadersDelay(2, TimeUnit.SECONDS).setHeader("Content-Type", "image/png").setBody("fixture"))
            val job = async { api.icon(installed) }
            kotlinx.coroutines.yield()
            assertNotNull(server.takeRequest(2, TimeUnit.SECONDS))
            job.cancelAndJoin()
            assertTrue(call!!.isCanceled())
        }
    }
}
