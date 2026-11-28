package dev.persona.watch

/** Network results cross one UI dispatch boundary before any captured-state or visible completion. */
internal fun <T> postMutationResult(result: Result<T>, post: (() -> Unit) -> Unit,
                                   settled: () -> Unit, complete: (Result<T>) -> Unit) {
    post {
        if (result.isSuccess) settled()
        complete(result)
    }
}

/** Tickets separate connection identity from refresh/mutation lifetime. All access is on the UI thread. */
internal class ActivityWork {
    data class Ticket(val generation: Int, val serial: Long)
    var generation = 0
        private set
    private var serial = 0L
    private var mutation: Ticket? = null
    private var refresh: Ticket? = null
    val busy get() = mutation != null
    val refreshing get() = refresh != null
    fun beginMutation(): Ticket? = if (busy) null else Ticket(generation, ++serial).also { mutation = it }
    fun beginRefresh(): Ticket? = if (busy || refreshing) null else Ticket(generation, ++serial).also { refresh = it }
    fun finishMutation(ticket: Ticket): Boolean {
        if (mutation != ticket) return false
        mutation = null
        return ticket.generation == generation
    }
    fun finishRefresh(ticket: Ticket): Boolean {
        if (refresh != ticket) return false
        refresh = null
        return ticket.generation == generation
    }
    fun pause() { refresh = null }
    fun connectionsChanged() { generation++; mutation = null; refresh = null }
}
