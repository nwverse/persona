package persona.core.hub

import persona.core.contracts.ServerEvent
import persona.core.contracts.api.ConnectionState
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections

/**
 * A subscriber slow over an event (a reload over the network, a lock) must not hold up the socket's loop (#3627): over
 * real sockets, a hub that restarts or drops is reconnected to and the connection comes back Online while it waits.
 */
class HubSlowSubscriberTest {
    private fun connection(hub: LoopbackHub) = OkHttpHubConnection(
        HubConnectionConfig("test-hub", null, "test-token", listOf(hub.endpoint("lan")), "test"), HubNetwork("network"))

    private suspend fun OkHttpHubConnection.awaitOnline(millis: Long = 10_000) =
        withTimeout(millis) { state.first { it == ConnectionState.Online } }

    /** A subscriber that takes each event's type, then waits on [gate] before taking the next. */
    private class Subscriber(connection: OkHttpHubConnection, private val gate: suspend () -> Unit) : AutoCloseable {
        val got: MutableList<String> = Collections.synchronizedList(mutableListOf())
        private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        init { scope.launch { connection.events.collect { event: ServerEvent -> got += event::class.simpleName.orEmpty(); gate() } } }
        override fun close() = scope.cancel()
    }

    @Test fun aHubRestartIsReconnectedToWhileASubscriberIsStuck() = runBlocking<Unit> {
        LoopbackHub().use { hub ->
            connection(hub).use { connection ->
                connection.awaitOnline()
                val stuck = CompletableDeferred<Unit>()
                Subscriber(connection) { stuck.await() }.use { subscriber ->
                    eventually { hub.opened.get() == 1 }
                    hub.socket!!.send("""{"type":"todos"}""")
                    eventually { subscriber.got.isNotEmpty() }
                    // The deploy: the hub says it is going away and closes the socket.
                    val start = System.currentTimeMillis()
                    hub.socket!!.close(1001, "hub restarting")
                    eventually(10_000) { hub.opened.get() == 2 }
                    connection.awaitOnline()
                    println("reading: reconnected after a 1001 close in ${System.currentTimeMillis() - start} ms, subscriber stuck")
                    assertEquals(ConnectionState.Online, connection.state.value)
                    // Online alone proves little (a restart's grace keeps showing it); the loop still taking the next
                    // drop does: a loop stuck on the new hello would never open a third socket.
                    hub.socket!!.close(1001, "hub restarting")
                    eventually(10_000) { hub.opened.get() == 3 }
                    stuck.complete(Unit)
                    // Let go, it gets the second socket's hello (already being handed over, not taken back), then the
                    // third's.
                    eventually { subscriber.got.size >= 3 }
                    delay(300)
                    assertEquals(listOf("TodosEvent", "Hello", "Hello"), subscriber.got.toList())
                }
            }
        }
    }

    @Test fun anOrdinaryDropIsReconnectedToWhileASubscriberIsStuck() = runBlocking<Unit> {
        LoopbackHub().use { hub ->
            connection(hub).use { connection ->
                connection.awaitOnline()
                val stuck = CompletableDeferred<Unit>()
                Subscriber(connection) { stuck.await() }.use { subscriber ->
                    eventually { hub.opened.get() == 1 }
                    hub.socket!!.send("""{"type":"todos"}""")
                    eventually { subscriber.got.isNotEmpty() }
                    // Not a restart (no 1001): the ordinary drop, retried with the backoff.
                    val start = System.currentTimeMillis()
                    hub.socket!!.close(1011, "internal error")
                    eventually(10_000) { hub.opened.get() == 2 }
                    connection.awaitOnline()
                    println("reading: reconnected after an ordinary drop (1011) in ${System.currentTimeMillis() - start} ms, subscriber stuck")
                    hub.socket!!.close(1011, "internal error")
                    eventually(10_000) { hub.opened.get() == 3 }
                    stuck.complete(Unit)
                }
            }
        }
    }

    @Test fun restartsInARowAreEachReconnectedToWhileASubscriberStaysStuck() = runBlocking<Unit> {
        LoopbackHub().use { hub ->
            connection(hub).use { connection ->
                connection.awaitOnline()
                val stuck = CompletableDeferred<Unit>()
                Subscriber(connection) { stuck.await() }.use { subscriber ->
                    eventually { hub.opened.get() == 1 }
                    hub.socket!!.send("""{"type":"todos"}""")
                    eventually { subscriber.got.isNotEmpty() }
                    val times = mutableListOf<Long>()
                    for (round in 2..4) {
                        val start = System.currentTimeMillis()
                        val previousHello = connection.lastHello
                        hub.socket!!.close(1001, "hub restarting")
                        eventually(10_000) { hub.opened.get() == round }
                        eventually(10_000) { connection.lastHello !== previousHello }
                        connection.awaitOnline()
                        times += System.currentTimeMillis() - start
                        hub.socket!!.send("""{"type":"todos"}""")
                    }
                    println("reading: three restarts in a row reconnected in ${times.joinToString()} ms, subscriber stuck")
                    // Still on the first event the whole time.
                    assertEquals(1, subscriber.got.size)
                    stuck.complete(Unit)
                    // Then the second socket's hello (being handed over, not taken back) and the latest socket's events; the
                    // rest of the earlier sockets' were dropped when a later one started over (the loopback hub does not
                    // resume).
                    eventually { subscriber.got.size >= 4 }
                    delay(300)
                    assertEquals(listOf("TodosEvent", "Hello", "Hello", "TodosEvent"), subscriber.got.toList())
                }
            }
        }
    }

    @Test fun aSlowSubscriberGetsABurstInOrder() = runBlocking<Unit> {
        LoopbackHub().use { hub ->
            connection(hub).use { connection ->
                connection.awaitOnline()
                Subscriber(connection) { delay(20) }.use { subscriber ->
                    eventually { hub.opened.get() == 1 }
                    repeat(20) { i -> hub.socket!!.send(if (i % 2 == 0) """{"type":"todos"}""" else """{"type":"inbox"}""") }
                    eventually { subscriber.got.size >= 20 }
                    assertEquals(List(10) { listOf("TodosEvent", "InboxEvent") }.flatten(), subscriber.got.toList())
                }
            }
        }
    }

    @Test fun aSlowSubscriberDoesNotDelayTheReconnect() = runBlocking<Unit> {
        LoopbackHub().use { hub ->
            connection(hub).use { connection ->
                connection.awaitOnline()
                Subscriber(connection) { delay(200) }.use { subscriber ->
                    eventually { hub.opened.get() == 1 }
                    // Twenty events: four seconds for this subscriber to take them all.
                    repeat(20) { hub.socket!!.send("""{"type":"todos"}""") }
                    eventually { subscriber.got.isNotEmpty() }
                    val start = System.currentTimeMillis()
                    hub.socket!!.close(1001, "hub restarting")
                    eventually(10_000) { hub.opened.get() == 2 }
                    connection.awaitOnline()
                    val reconnect = System.currentTimeMillis() - start
                    val taken = subscriber.got.size
                    println("reading: reconnected in $reconnect ms with $taken of 21 events taken by a 200 ms/event subscriber")
                    assertTrue("reconnect waited for the subscriber: $reconnect ms", taken < 20)
                    // The old socket's events still queued are dropped once the new one starts over; its hello comes last.
                    eventually(5_000) { subscriber.got.lastOrNull() == "Hello" }
                    val got = subscriber.got.toList()
                    assertEquals(List(got.size - 1) { "TodosEvent" } + "Hello", got)
                    assertTrue("old events delivered after the restart: ${got.size - 1}", got.size - 1 < 20)
                }
            }
        }
    }
}
