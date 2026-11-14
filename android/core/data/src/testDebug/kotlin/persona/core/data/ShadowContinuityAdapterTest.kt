package persona.core.data

import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Item
import persona.core.contracts.JsonCodec
import persona.core.domain.ContinuityReducer.Gap
import persona.core.domain.ContinuitySync.Action
import persona.core.rustcore.CoreHost
import persona.core.rustcore.RustCore
import persona.core.rustcore.wire.Config
import persona.core.rustcore.wire.Effect

class ShadowContinuityAdapterTest {
    @Test fun shadowReturnsExactlyKotlinAppliedForEveryInputKind() = runBlocking {
        val reports = ConcurrentLinkedQueue<ContinuityMismatch>()
        val plain = KotlinContinuityAdapter("private-chat")
        val adapter = ShadowContinuityAdapter("private-chat", sink = { reports.add(it) }, afterFirstFrame = { it() })
        val inputs = listOf(
            ContinuityAdapter.Input.Restore(ContinuityAdapter.State(listOf(item(1)), "private-history", 1, true, true)),
            ContinuityAdapter.Input.Latest(batch(2)),
            ContinuityAdapter.Input.Push(batch(4)),
            ContinuityAdapter.Input.After(batch(3)),
            ContinuityAdapter.Input.ContinueFill,
            ContinuityAdapter.Input.FillFailed,
            ContinuityAdapter.Input.HubChanged,
            ContinuityAdapter.Input.FillFailed,
            ContinuityAdapter.Input.Reconnect,
            ContinuityAdapter.Input.Reload(listOf(item(4)), "private-history", 4, true),
            ContinuityAdapter.Input.LoadOlder,
            ContinuityAdapter.Input.Older(listOf(item(1)), false),
            ContinuityAdapter.Input.OlderFailed,
            ContinuityAdapter.Input.Latest(ContinuityAdapter.Batch(listOf(item(4).copy(id = "private-fold", kind = "fold",
                fold = persona.core.contracts.Fold(items = 2, throughSeq = 5, throughId = "5#0"))), "private-history", toSeq = 5)),
            ContinuityAdapter.Input.OpenFold("private-fold"),
            ContinuityAdapter.Input.Range(listOf(item(4), item(5))),
            ContinuityAdapter.Input.RangeFailed,
        )
        try {
            assertFalse(adapter.asynchronous)
            inputs.forEach { input ->
                assertEquals(plain.accept(input), adapter.accept(input))
                assertEquals(plain.state, adapter.state)
                assertEquals(plain.behind, adapter.behind)
            }
        } finally {
            adapter.close()
            ShadowContinuityDiagnostics.awaitIdle()
        }
        assertEquals(emptyList<ContinuityMismatch>(), reports.toList())
    }

    @Test fun everyComparedFieldReportsOnlyRuleAndShape() = runBlocking {
        val reports = ConcurrentLinkedQueue<ContinuityMismatch>()
        val adapter = ShadowContinuityAdapter("secret-chat", sink = { reports.add(it) }, createShadow = { _, _ ->
            AlteredAdapter { value -> value.copy(state = value.state.copy(
                items = listOf(item(91).copy(text = "secret-shadow-text")), history = "secret-shadow-history",
                covered = 918273, hasMore = false, loadedOnce = false), gap = Gap.Reload,
                requests = listOf(Action.FetchAfter(918273))) }
        })
        val input = ContinuityAdapter.Input.Latest(ContinuityAdapter.Batch(listOf(item(1).copy(
            id = "secret-item-id", text = "secret-text")), "secret-history", toSeq = 1, hasMore = true, backlog = true))
        adapter.accept(input)
        adapter.close()
        ShadowContinuityDiagnostics.awaitIdle()
        assertEquals(setOf("item-ids-order", "items", "covered", "history", "hasMore", "loadedOnce", "gap", "requests"),
            reports.map { it.rule }.toSet())
        assertEquals(8, adapter.mismatchCount)
        reports.forEach { report ->
            assertEquals("continuity", report.domain)
            assertEquals("latest items=1 history=set fromSeq=unset toSeq=set backlog=true hasMore=true moreAfter=false", report.inputShape)
            val serialized = JsonCodec.encodeToString(ContinuityMismatch.serializer(), report)
            assertTrue(serialized.contains("\"domain\":\"continuity\""))
            listOf("secret-chat", "secret-item-id", "secret-text", "secret-history", "secret-shadow-text",
                "secret-shadow-history", "918273", "91#0", "\"seq\"", "\"text\"").forEach {
                assertFalse("Diagnostics contain content: $it", serialized.contains(it))
            }
        }
    }

    @Test fun callerReturnsBeforeLatchHeldShadowAndComparisonIsOffCallerThread() = runBlocking {
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val thread = AtomicReference<Thread>()
        val adapter = ShadowContinuityAdapter("chat", createShadow = { _, _ ->
            AlteredAdapter { value ->
                thread.set(Thread.currentThread())
                entered.countDown()
                check(release.await(5, TimeUnit.SECONDS))
                value
            }
        })
        try {
            val result = adapter.accept(ContinuityAdapter.Input.Push(batch(1)))
            assertEquals(listOf(item(1)), result.state.items)
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            assertEquals(1, release.count)
            assertNotSame(Thread.currentThread(), thread.get())
        } finally {
            release.countDown()
            adapter.close()
            ShadowContinuityDiagnostics.awaitIdle()
        }
        assertEquals(0, adapter.mismatchCount)
    }

    @Test fun unusedShadowNeverCreatesNativeAndClosePreservesQueuedInputsInOrder() = runBlocking {
        val creates = AtomicInteger()
        val inputs = ConcurrentLinkedQueue<ContinuityAdapter.Input>()
        val unused = ShadowContinuityAdapter("unused", createShadow = { _, _ -> creates.incrementAndGet(); error("secret-error") })
        unused.close()
        ShadowContinuityDiagnostics.awaitIdle()
        assertEquals(0, creates.get())
        val adapter = ShadowContinuityAdapter("chat", createShadow = { _, _ ->
            creates.incrementAndGet()
            object : ContinuityAdapter by KotlinContinuityAdapter("chat") {
                private val kotlin = KotlinContinuityAdapter("chat")
                override suspend fun accept(input: ContinuityAdapter.Input): ContinuityAdapter.Applied {
                    inputs.add(input)
                    return kotlin.accept(input)
                }
            }
        })
        val sent = (1..10).map { ContinuityAdapter.Input.Push(batch(it)) }
        sent.forEach { adapter.accept(it) }
        adapter.close()
        ShadowContinuityDiagnostics.awaitIdle()
        assertEquals(1, creates.get())
        assertEquals(sent, inputs.toList())
        assertEquals(0, adapter.mismatchCount)
    }

    @Test fun nativeCreationWaitsForFirstFrameWithoutBlockingKotlin() = runBlocking {
        val frameScheduled = CountDownLatch(1)
        val frame = AtomicReference<() -> Unit>()
        val creates = AtomicInteger()
        val creationThread = AtomicReference<Thread>()
        val adapter = ShadowContinuityAdapter("chat", afterFirstFrame = { frame.set(it); frameScheduled.countDown() },
            createShadow = { scheduler, rejected ->
                val driver = RustContinuityDriver(scheduler, rejected = rejected) {
                    creates.incrementAndGet()
                    creationThread.set(Thread.currentThread())
                    RustCore(Config(), it)
                }
                object : ContinuityAdapter by RustContinuityAdapter("chat", driver) {
                    override fun close() { driver.close() }
                }
            })
        try {
            assertEquals(listOf(item(1)), adapter.accept(ContinuityAdapter.Input.Push(batch(1))).state.items)
            assertTrue(frameScheduled.await(5, TimeUnit.SECONDS))
            assertEquals(0, creates.get())
            frame.get().invoke()
            ShadowContinuityDiagnostics.awaitIdle()
            assertEquals(1, creates.get())
            assertNotSame(Thread.currentThread(), creationThread.get())
            assertEquals(0, adapter.mismatchCount)
        } finally {
            frame.get()?.invoke()
            adapter.close()
            ShadowContinuityDiagnostics.awaitIdle()
        }
    }

    @Test fun loadCreationAcceptAndRejectedFailuresDisableOnlyShadowWithoutContent() = runBlocking {
        for (failure in listOf("load", "creation", "accept", "rejected")) {
            val reports = ConcurrentLinkedQueue<ContinuityMismatch>()
            val attempts = AtomicInteger()
            val adapter = ShadowContinuityAdapter("secret-chat", sink = { reports.add(it) }, afterFirstFrame = { it() },
                createShadow = { scheduler, rejected ->
                    attempts.incrementAndGet()
                    when (failure) {
                        "load" -> throw UnsatisfiedLinkError("secret-token")
                        "creation" -> error("secret-token")
                        "rejected" -> {
                            val driver = RustContinuityDriver(scheduler, rejected = rejected) { host ->
                                host.run(Effect.Rejected("secret-token"))
                                RustCore(Config(), host)
                            }
                            object : ContinuityAdapter by RustContinuityAdapter("secret-chat", driver) {
                                override fun close() { driver.close() }
                            }
                        }
                        else -> AlteredAdapter { error("secret-token") }
                    }
                })
            val plain = KotlinContinuityAdapter("secret-chat")
            repeat(3) {
                val input = ContinuityAdapter.Input.Push(batch(it + 1))
                assertEquals(plain.accept(input), adapter.accept(input))
            }
            adapter.close()
            ShadowContinuityDiagnostics.awaitIdle()
            assertEquals(1, attempts.get())
            val report = reports.single()
            assertEquals(when (failure) { "load", "creation" -> "rust-creation"; "rejected" -> "rust-rejected"; else -> "rust-failure" }, report.rule)
            assertFalse(JsonCodec.encodeToString(ContinuityMismatch.serializer(), report).contains("secret"))
        }
    }

    private class AlteredAdapter(private val alter: (ContinuityAdapter.Applied) -> ContinuityAdapter.Applied) : ContinuityAdapter {
        private val kotlin = KotlinContinuityAdapter("fake")
        override val state get() = kotlin.state
        override val behind get() = kotlin.behind
        override suspend fun accept(input: ContinuityAdapter.Input) = alter(kotlin.accept(input))
    }
    private fun item(seq: Int) = Item("$seq#0", seq, role = "assistant", kind = "text", text = "private line")
    private fun batch(seq: Int) = ContinuityAdapter.Batch(listOf(item(seq)), "private-history", seq - 1, seq)
}
