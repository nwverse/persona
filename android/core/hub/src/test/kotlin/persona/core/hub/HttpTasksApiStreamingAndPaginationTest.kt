package persona.core.hub

import java.io.File
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.ResponseBody
import okhttp3.mockwebserver.MockResponse
import okio.Buffer
import okio.BufferedSource
import okio.Source
import okio.Timeout
import okio.buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer

class HttpTasksApiStreamingAndPaginationTest {
    private data class RecordedCall(
        val path: String,
        val method: String,
        val options: TasksRequestOptions,
    )

    private fun trackedApi(
        server: MockWebServer,
        features: Set<String> = emptySet(),
    ): Pair<HttpTasksApi, MutableList<RecordedCall>> {
        val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
        val recorded = mutableListOf<RecordedCall>()
        val api = HttpTasksApi({ path, method, body, options ->
            recorded += RecordedCall(path, method, options)
            val rb = body?.toString()?.toRequestBody("application/json".toMediaType())
            val req = Request.Builder().url(server.url(path)).method(method, rb).apply {
                options.headers.forEach { (k, v) -> header(k, v) }
            }.build()
            http.newCall(req).execute()
        }, MutableStateFlow(features))
        return api to recorded
    }

    @Test
    fun todoRevision304SkipsTodosDownloadAndReloadsOnlyWhenRevisionChanges() = runBlocking {
        MockWebServer().use { server ->
            val (api, calls) = trackedApi(server, features = setOf("todo_revision"))

            // First fetch: read revision -> read paged todos -> verify revision (304) -> cache validator.
            server.enqueue(MockResponse()
                .addHeader("X-Persona-Todos-Revision", "W/\"tr1-hub-v1\"")
                .setBody("""{"revision":"W/\"tr1-hub-v1\""}"""))
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"t1","title":"One"}],"has_more":false}"""))
            server.enqueue(MockResponse().setResponseCode(304).addHeader("X-Persona-Todos-Revision", "W/\"tr1-hub-v1\""))

            val first = api.todos()
            assertEquals(listOf("t1"), first.map { it.id })
            assertEquals("/api/v1/todos/revision", server.takeRequest().path)
            assertEquals("/api/v1/todos?limit=200", server.takeRequest().path)
            val verify = server.takeRequest()
            assertEquals("/api/v1/todos/revision", verify.path)
            assertEquals("W/\"tr1-hub-v1\"", verify.getHeader("If-None-Match"))

            // Second fetch with unchanged revision: single 304 skips /api/v1/todos completely.
            server.enqueue(MockResponse().setResponseCode(304).addHeader("X-Persona-Todos-Revision", "W/\"tr1-hub-v1\""))
            val second = api.todos()
            assertEquals(first, second)
            val recheck = server.takeRequest()
            assertEquals("/api/v1/todos/revision", recheck.path)
            assertEquals("W/\"tr1-hub-v1\"", recheck.getHeader("If-None-Match"))
            assertEquals(4, server.requestCount)

            // Third fetch after revision changes: downloads updated page and caches the new validator.
            server.enqueue(MockResponse()
                .addHeader("X-Persona-Todos-Revision", "W/\"tr1-hub-v2\"")
                .setBody("""{"revision":"W/\"tr1-hub-v2\""}"""))
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"t1","title":"One"},{"id":"t2","title":"Two"}],"has_more":false}"""))
            server.enqueue(MockResponse().setResponseCode(304).addHeader("X-Persona-Todos-Revision", "W/\"tr1-hub-v2\""))

            val third = api.todos()
            assertEquals(listOf("t1", "t2"), third.map { it.id })
            assertTrue("Every todos/revision call must use buffered=false", calls.all { !it.options.buffered })
        }
    }

    @Test
    fun cursorPaginationConcatenatesPagesAndRejectsRepeatedCursor() = runBlocking {
        MockWebServer().use { server ->
            val (api, calls) = trackedApi(server, features = emptySet())

            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"t1","title":"Page 1"}],"has_more":true,"next_cursor":"cur-1"}"""))
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"t2","title":"Page 2"}],"has_more":true,"next_cursor":"cur-2"}"""))
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"t3","title":"Page 3"}],"has_more":false}"""))

            val list = api.todos()
            assertEquals(listOf("t1", "t2", "t3"), list.map { it.id })
            assertEquals("/api/v1/todos?limit=200", server.takeRequest().path)
            assertEquals("/api/v1/todos?limit=200&cursor=cur-1", server.takeRequest().path)
            assertEquals("/api/v1/todos?limit=200&cursor=cur-2", server.takeRequest().path)
            assertEquals(3, calls.size)
            assertTrue("Every page read must stream with buffered=false", calls.all { !it.options.buffered })

            // Repeated cursor must fail fast instead of looping infinitely.
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"a"}],"has_more":true,"next_cursor":"loop"}"""))
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"b"}],"has_more":true,"next_cursor":"loop"}"""))
            try {
                api.todos()
                fail("Repeated todos cursor was not rejected")
            } catch (error: IllegalStateException) {
                assertTrue(error.message.orEmpty().contains("Repeated todos cursor"))
            }
        }
    }

    @Test
    fun fallsBackGracefullyOnOlderHubAndMultiManagerHubAndRevisionConflict() = runBlocking {
        MockWebServer().use { server ->
            // 1. Older hub without todo_revision that ignores ?limit and omits has_more.
            val (legacyApi, legacyCalls) = trackedApi(server, features = emptySet())
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"old-1","title":"Legacy"}]}"""))
            assertEquals(listOf("old-1"), legacyApi.todos().map { it.id })
            assertEquals(1, server.requestCount)
            assertEquals("/api/v1/todos?limit=200", server.takeRequest().path)
            assertFalse(legacyCalls.single().options.buffered)

            // 2. Hub answering 501 todos_pagination_unsupported falls back to unpaginated /api/v1/todos.
            server.enqueue(MockResponse().setResponseCode(501)
                .setBody("""{"error":"pagination unsupported","code":"todos_pagination_unsupported"}"""))
            server.enqueue(MockResponse().setBody("""{"todos":[{"id":"fallback-501","title":"Unpaginated"}]}"""))
            assertEquals(listOf("fallback-501"), legacyApi.todos().map { it.id })
            assertEquals("/api/v1/todos?limit=200", server.takeRequest().path)
            assertEquals("/api/v1/todos", server.takeRequest().path)

            // 3. Multi-manager hub answering 400 todos_pagination_manager_required falls back to unpaginated /api/v1/todos
            //    and caches X-Persona-Todos-Revision from the unpaginated header for subsequent 304s.
            val (multiApi, multiCalls) = trackedApi(server, features = setOf("todo_revision"))
            server.enqueue(MockResponse().setResponseCode(409)
                .setBody("""{"error":"unavailable","code":"todo_revision_unavailable"}"""))
            server.enqueue(MockResponse().setResponseCode(400)
                .setBody("""{"error":"manager required","code":"todos_pagination_manager_required"}"""))
            server.enqueue(MockResponse()
                .addHeader("X-Persona-Todos-Revision", "W/\"tr1-hub-mm1\"")
                .setBody("""{"todos":[{"id":"mm-1","title":"Multi","manager":"assistant-2"}]}"""))

            val mm = multiApi.todos()
            assertEquals(listOf("mm-1"), mm.map { it.id })
            assertEquals("/api/v1/todos/revision", server.takeRequest().path)
            assertEquals("/api/v1/todos?limit=200", server.takeRequest().path)
            assertEquals("/api/v1/todos", server.takeRequest().path)

            // Next call uses the cached header revision and short-circuits on 304.
            server.enqueue(MockResponse().setResponseCode(304))
            assertEquals(mm, multiApi.todos())
            val nextReq = server.takeRequest()
            assertEquals("/api/v1/todos/revision", nextReq.path)
            assertEquals("W/\"tr1-hub-mm1\"", nextReq.getHeader("If-None-Match"))
            assertTrue(multiCalls.all { !it.options.buffered })
        }
    }

    @Test
    fun feedReadsStreamWithoutBuffering() = runBlocking {
        MockWebServer().use { server ->
            val (api, calls) = trackedApi(server)
            server.enqueue(MockResponse().setBody("""{"items":[{"id":"f1","conversation":"c1","kind":"notice"}],"next_cursor":""}"""))
            val page = api.feedPages("c1").read(null, null)
            assertEquals(listOf("f1"), page.items.map { it.id })
            assertEquals("", page.nextCursor)
            assertEquals(1, calls.size)
            assertFalse(calls.single().options.buffered)
            assertNull(server.takeRequest().getHeader("If-None-Match"))
        }
    }

    @Test
    fun streams25MiBTodosPayloadInside64MiBHeapJvm() {
        val javaBin = File(System.getProperty("java.home"), "bin/java").absolutePath
        val classpath = System.getProperty("java.class.path")
        val process = ProcessBuilder(
            javaBin,
            "-Xmx64m",
            "-XX:+ExitOnOutOfMemoryError",
            "-cp",
            classpath,
            TodosLowHeapStreamRunner::class.java.name,
        ).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        assertTrue("Low-heap child JVM timed out:\n$output", process.waitFor(60, TimeUnit.SECONDS))
        assertEquals("64 MiB low-heap streaming runner failed:\n$output", 0, process.exitValue())
        assertTrue("Expected >= 25 MiB wire payload and 1250 decoded todos:\n$output",
            output.contains("COUNT=1250") && output.contains("WIRE_OK=true"))
        println(output.trim())
    }
}

/**
 * Child-JVM entry point run under `-Xmx64m` to prove [HttpTasksApi.todos] streams a 25+ MiB
 * `/api/v1/todos` payload without materializing a whole-body `ByteArray`, `String`, or `JsonObject` DOM.
 */
object TodosLowHeapStreamRunner {
    private const val TARGET_COUNT = 1250
    private const val MIN_WIRE_BYTES = 25L * 1024 * 1024

    @JvmStatic
    fun main(args: Array<String>) {
        // Build realistic ~21 KiB JSON rows on the fly (CJK UTF-8 text + structured server-only brief.log/view
        // entries + real Todo fields) so the generator never buffers 25 MiB in heap while a non-streaming
        // String + JsonObject DOM decoder immediately blows past -Xmx64m.
        val logEntry = """{"at":"2026-11-26T00:00:00Z","actor":"supervisor","kind":"progress","summary":"检查进展与日志上下文记录验证结果"}"""
        val briefLog = (0 until 170).joinToString(",") { logEntry }
        val viewBlock = """{"phase":"working","section":"active","reason":"running","steps":{"done":3,"total":5},"detail":"等待验证结果确认"}"""
        var wireBytes = 0L

        val streamingBody = object : ResponseBody() {
            override fun contentType() = "application/json".toMediaType()
            override fun contentLength() = -1L
            override fun source(): BufferedSource {
                var index = -1
                var pending = ByteArray(0)
                var offset = 0
                return object : Source {
                    override fun timeout() = Timeout.NONE
                    override fun close() = Unit
                    override fun read(sink: Buffer, byteCount: Long): Long {
                        if (offset >= pending.size) {
                            val chunk = when {
                                index == -1 -> {
                                    index = 0
                                    """{"todos":["""
                                }
                                index < TARGET_COUNT -> {
                                    val i = index++
                                    val sep = if (i == 0) "" else ","
                                    """$sep{"id":"todo-$i","title":"Task $i","notes":"Note $i","created_at":"2026-11-26T00:00:00Z","agents":[{"machine_id":"m1","session_id":"s-$i"}],"brief":{"log":[$briefLog]},"view":$viewBlock}"""
                                }
                                index == TARGET_COUNT -> {
                                    index++
                                    """],"has_more":false}"""
                                }
                                else -> return -1L
                            }
                            pending = chunk.toByteArray()
                            offset = 0
                        }
                        val n = minOf(byteCount, (pending.size - offset).toLong()).toInt()
                        sink.write(pending, offset, n)
                        offset += n
                        wireBytes += n
                        return n.toLong()
                    }
                }.buffer()
            }
        }

        val api = HttpTasksApi({ path, method, _, options ->
            check(!options.buffered) { "Expected buffered=false for streaming todos read" }
            Response.Builder()
                .request(Request.Builder().url("https://example.com$path").method(method, null).build())
                .protocol(okhttp3.Protocol.HTTP_1_1)
                .code(200)
                .message("OK")
                .body(streamingBody)
                .build()
        })

        val todos = runBlocking { api.todos() }
        val rt = Runtime.getRuntime()
        val usedMiB = (rt.totalMemory() - rt.freeMemory()) / (1024 * 1024)
        val maxMiB = rt.maxMemory() / (1024 * 1024)
        check(todos.size == TARGET_COUNT) { "Expected $TARGET_COUNT todos, got ${todos.size}" }
        check(todos.first().id == "todo-0" && todos.last().id == "todo-${TARGET_COUNT - 1}")
        check(wireBytes >= MIN_WIRE_BYTES) { "Wire bytes $wireBytes below $MIN_WIRE_BYTES" }
        println("COUNT=${todos.size} WIRE_BYTES=$wireBytes WIRE_OK=${wireBytes >= MIN_WIRE_BYTES} USED_MIB=$usedMiB MAX_MIB=$maxMiB")
    }
}
