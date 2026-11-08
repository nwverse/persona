package persona.core.hub

import java.net.Socket
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import okio.ByteString
import okio.ByteString.Companion.encodeUtf8
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** docs/operations/ports.md "Forwarding": a connection to the phone's port is one WebSocket to the hub, bytes as they are. */
class PortForwarderTest {
    private val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()

    @Test fun aConnectionsBytesGoBothWaysThroughTheHub() = runBlocking {
        MockWebServer().use { hub ->
            val received = LinkedBlockingQueue<String>()
            hub.enqueue(MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    received += bytes.utf8()
                    // What the dev server on the machine answers, then it closes the connection.
                    webSocket.send("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok".encodeUtf8())
                    webSocket.close(1000, null)
                }
            }))
            val forwarder = PortForwarder(http) { hub.url("/") to "device-token" }
            val port = forwarder.localPort(PortForwarder.Key("mac", 5173))
            Socket("127.0.0.1", port).use { browser ->
                browser.soTimeout = 5000
                browser.getOutputStream().apply { write("GET / HTTP/1.1\r\nHost: localhost:5173\r\n\r\n".toByteArray()); flush() }
                val answer = browser.getInputStream().readBytes().decodeToString()
                assertEquals("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok", (answer).zhAny())
            }
            assertEquals("GET / HTTP/1.1\r\nHost: localhost:5173\r\n\r\n", (received.poll(5, TimeUnit.SECONDS)).zhAny())
            val upgrade = hub.takeRequest()
            assertEquals("/api/v1/machines/mac/ports/5173/connect", (upgrade.path).zhAny())
            assertEquals("Bearer device-token", (upgrade.getHeader("Authorization")).zhAny())
            assertEquals(PortForwarder.Key("mac", 5173), forwarder.keyOf(port))
            forwarder.close()
        }
    }

    @Test fun aTakenPortGetsAnotherNumberAndTheSameKeyKeepsItsPort() = runBlocking {
        java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { taken ->
            val forwarder = PortForwarder(http) { okhttp3.HttpUrl.Builder().scheme("http").host("127.0.0.1").port(1).build() to "t" }
            val port = forwarder.localPort(PortForwarder.Key("mac", taken.localPort))
            assertNotEquals(taken.localPort, port)
            assertTrue(port > 0)
            assertEquals(port, forwarder.localPort(PortForwarder.Key("mac", taken.localPort)))
            forwarder.close()
        }
    }

    // #3483: a group's stack torn down (re-pair, unpair) frees its ports, so the next pairing's forwarder gets them back.
    @Test fun aClosedOpenerFreesItsPortForTheNext(): Unit = runBlocking {
        fun opener() = HubPortOpener(OkHttpHubConnection(
            HubConnectionConfig("test-hub", null, "t", listOf(persona.core.contracts.Endpoint("http://127.0.0.1:1/", "lan")), "test"),
            HubNetwork("offline", available = false)))
        // The free number comes from a probe that must be released before the first opener binds it, so another
        // process or an outgoing connection's ephemeral port can take it in between; the opener then rightly falls
        // back to another number. Only that precondition is retried with a new number. Once the first opener holds
        // the number, the next one must get it back; a forwarder that never prefers the machine's number fails here.
        repeat(5) {
            val port = java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { it.localPort }
            val first = opener()
            val got = first.open("mac", port, "/", secure = false, listed = true)
            (first as? AutoCloseable)?.close()
            if (got != "http://localhost:$port/") return@repeat
            val second = opener()
            assertEquals("http://localhost:$port/", second.open("mac", port, "/", secure = false, listed = true))
            (second as? AutoCloseable)?.close()
            return@runBlocking
        }
        fail("The first opener never got the free machine port number in 5 tries")
    }
}
