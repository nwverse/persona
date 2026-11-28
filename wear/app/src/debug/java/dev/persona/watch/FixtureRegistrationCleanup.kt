package dev.persona.watch

/** A fixture abort is bounded and never turns an incomplete cleanup into a passing run. */
internal fun cleanupFixtureRegistrations(owned: Collection<PendingHubRegistration>, abort: (PendingHubRegistration) -> Unit) {
    var failure: Exception? = null
    for (entry in owned) {
        var last: Exception? = null
        for (attempt in 1..3) {
            try { abort(entry); last = null; break }
            catch (problem: Exception) {
                if (last == null) last = problem
                else if (last !== problem) last.addSuppressed(problem)
            }
        }
        last?.let { problem -> if (failure == null) failure = problem else failure!!.addSuppressed(problem) }
    }
    failure?.let { throw it }
}

/** Drain a changing ownership set even when observing the queued registration fails. */
internal fun drainFixtureRegistrations(
    owned: () -> Collection<PendingHubRegistration>,
    fence: (PendingHubRegistration) -> Unit,
    abort: (PendingHubRegistration, Long) -> Unit,
    awaitRecord: ((Long) -> Boolean)? = null,
    now: () -> Long = { System.nanoTime() / 1_000_000 },
    wait: (Long) -> Unit = Thread::sleep,
    timeoutMs: Long = 15_000,
) {
    require(timeoutMs > 0)
    val deadline = now() + timeoutMs
    val completed = mutableSetOf<String>()
    var lastCleanupFailure: Throwable? = null
    var observationFailure: Throwable? = null
    fun recordCleanupFailure(failure: Throwable) {
        if (lastCleanupFailure == null) lastCleanupFailure = failure
        else if (lastCleanupFailure !== failure) lastCleanupFailure?.addSuppressed(failure)
    }
    fun remaining() = (deadline - now()).coerceAtLeast(0)
    fun pending() = owned().filterNot { it.id in completed }
    fun cleanupPass() {
        val entries = pending()
        // Fence every known ID before any abort can block waiting for the registration lock.
        entries.forEach { entry ->
            runCatching { fence(entry) }.onFailure(::recordCleanupFailure)
        }
        runCatching {
            cleanupFixtureRegistrations(entries) { entry ->
                val budget = remaining()
                check(budget > 0) { "multihub_cleanup_deadline" }
                abort(entry, budget) // The caller bounds the blocking operation by this remaining budget.
                completed += entry.id
            }
        }.onFailure(::recordCleanupFailure)
    }

    cleanupPass() // A failed await must never prevent cleanup of already owned registrations.
    if (awaitRecord != null) {
        observationFailure = runCatching {
            check(awaitRecord(minOf(remaining(), 5_000))) { "multihub_registration_not_observed" }
        }.exceptionOrNull()
    }
    while (true) {
        cleanupPass() // Resnapshot after the wait and after every abort/retry; callbacks may add IDs.
        if (pending().isEmpty() || remaining() == 0L) break
        val waitFailure = runCatching { wait(minOf(remaining(), 50)) }.exceptionOrNull()
        if (waitFailure != null) {
            if (observationFailure == null) observationFailure = waitFailure
            else observationFailure?.addSuppressed(waitFailure)
            cleanupPass()
            break
        }
    }
    val residue = pending()
    if (residue.isNotEmpty()) {
        throw IllegalStateException("multihub_cleanup_incomplete_${residue.size}").also { failure ->
            lastCleanupFailure?.let(failure::addSuppressed)
            observationFailure?.let(failure::addSuppressed)
        }
    }
    observationFailure?.let { throw it }
}
