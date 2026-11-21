package dev.persona.watch.pet

import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

class PetTimelineWorkerTest {
    @Test fun credentialReplacementOrRemovalDuringDecodeCannotPublishTheOldTimeline() {
        for (replacement in listOf("new-credential", null, "old-credential")) {
            val identity = AtomicReference<String?>("old-credential")
            val result = AtomicReference<String?>("not delivered")
            val done = CountDownLatch(1)
            PetTimelineWorker().use { worker ->
                worker.submitForIdentity(currentIdentity = { identity.get() }, build = { captured ->
                    assertEquals("old-credential", captured)
                    identity.set(replacement)
                    "old timeline"
                }) { result.set(it); done.countDown() }
                assertTrue(done.await(5, TimeUnit.SECONDS))
                assertEquals(if (replacement == "old-credential") "old timeline" else null, result.get())
            }
        }
    }

    @Test fun missingIdentityPreventsPrivateCacheReadOnWorker() {
        PetTimelineWorker().use { worker ->
            val done = CountDownLatch(1)
            val builds = AtomicInteger()
            val result = AtomicReference<String?>("not delivered")
            worker.submitForIdentity<String, String>(currentIdentity = { null }, build = {
                builds.incrementAndGet(); "must not load"
            }) { result.set(it); done.countDown() }
            assertTrue(done.await(5, TimeUnit.SECONDS))
            assertEquals(0, builds.get())
            assertNull(result.get())
        }
    }

    @Test fun constructionAndDeliveryRunOffTheRequestThreadExactlyOnce() {
        val caller = Thread.currentThread()
        val builtOn = AtomicReference<Thread>()
        val deliveredOn = AtomicReference<Thread>()
        val result = AtomicReference<String>()
        val deliveries = AtomicInteger()
        val done = CountDownLatch(1)
        PetTimelineWorker().use { worker ->
            worker.submit(build = { builtOn.set(Thread.currentThread()); "timeline" }) {
                deliveredOn.set(Thread.currentThread()); result.set(it); deliveries.incrementAndGet(); done.countDown()
            }
            assertTrue(done.await(5, TimeUnit.SECONDS))
            assertNotSame(caller, builtOn.get())
            assertSame(builtOn.get(), deliveredOn.get())
            assertEquals("timeline", result.get())
            assertEquals(1, deliveries.get())
        }
    }

    @Test fun failedOrMissingTimelineCompletesWithNoDataOnce() {
        PetTimelineWorker().use { worker ->
            for (build in listOf<() -> String?>({ throw IllegalStateException("Unavailable pet cache") }, { null })) {
                val result = AtomicReference<String?>("not delivered")
                val deliveries = AtomicInteger()
                val done = CountDownLatch(1)
                worker.submit(build) { result.set(it); deliveries.incrementAndGet(); done.countDown() }
                assertTrue(done.await(5, TimeUnit.SECONDS))
                assertNull(result.get())
                assertEquals(1, deliveries.get())
            }
        }
    }

    @Test fun saturatedQueueNeverDecodesOnCallerAndShutdownDrainsAcceptedCallbacks() {
        val worker = PetTimelineWorker(queueCapacity = 1)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val completed = CountDownLatch(2)
        val accepted = ConcurrentHashMap<String, String>()
        val builds = AtomicInteger()
        val rejected = AtomicInteger()
        try {
            worker.submit(build = {
                builds.incrementAndGet(); entered.countDown()
                check(release.await(5, TimeUnit.SECONDS))
                "first"
            }) { accepted["first"] = it ?: "missing"; completed.countDown() }
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            worker.submit(build = { builds.incrementAndGet(); "second" }) {
                accepted["second"] = it ?: "missing"; completed.countDown()
            }
            worker.submit<String>(build = { builds.incrementAndGet(); "must not decode" }) {
                assertNull(it); rejected.incrementAndGet()
            }
            assertEquals(1, builds.get())
            assertEquals(1, rejected.get())
            worker.close()
            worker.submit<String>(build = { builds.incrementAndGet(); "must not decode after close" }) {
                assertNull(it); rejected.incrementAndGet()
            }
            assertEquals(2, rejected.get())
            release.countDown()
            assertTrue(completed.await(5, TimeUnit.SECONDS))
            assertEquals(mapOf("first" to "first", "second" to "second"), accepted)
            assertEquals(2, builds.get())
        } finally {
            release.countDown()
            worker.close()
        }
    }

    @Test fun aClosedWorkerDoesNotStartAnyDiskOrDecodeWork() {
        val worker = PetTimelineWorker()
        worker.close()
        var delivered = 0
        worker.submit<String>(build = { throw AssertionError("No synchronous decode after destruction") }) {
            assertNull(it); delivered++
        }
        assertEquals(1, delivered)
    }
}
