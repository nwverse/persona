package dev.persona.watch.pet

import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit

/** One decoder and a bounded backlog; overload never moves disk/bitmap work onto the caller. */
internal class PetTimelineWorker(queueCapacity: Int = 8) : AutoCloseable {
    private val worker = ThreadPoolExecutor(1, 1, 0, TimeUnit.MILLISECONDS,
        ArrayBlockingQueue<Runnable>(queueCapacity), { task -> Thread(task, "persona-pet-complication") },
        ThreadPoolExecutor.AbortPolicy())

    fun <T> submit(build: () -> T?, deliver: (T?) -> Unit) {
        try {
            worker.execute {
                val result = runCatching(build).getOrNull()
                deliver(result)
            }
        } catch (_: RejectedExecutionException) {
            // The service returns NoData; never perform a synchronous fallback decode.
            deliver(null)
        }
    }

    /** A queued read or slow decode must not publish for a replaced/removed credential. */
    fun <I : Any, T> submitForIdentity(currentIdentity: () -> I?, build: (I) -> T?, deliver: (T?) -> Unit) {
        submit(build = {
            val identity = currentIdentity() ?: return@submit null
            val result = build(identity)
            result.takeIf { currentIdentity() == identity }
        }, deliver = deliver)
    }

    /** Complete accepted callbacks while refusing new work, without blocking service destruction. */
    override fun close() { worker.shutdown() }
}
