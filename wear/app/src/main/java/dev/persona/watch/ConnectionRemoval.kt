package dev.persona.watch

/** Re-read persisted state even when removal failed after changing the directory. */
internal fun completeConnectionRemoval(result: Result<Unit>, showError: () -> Unit, reload: () -> Unit) {
    if (result.isFailure) {
        showError()
    }
    reload()
}

internal enum class ConnectionReloadNotice { NONE, FAILURE, PENDING, RECOVERED }
internal data class ConnectionReloadFeedback(val notice: ConnectionReloadNotice, val retry: Boolean)

/** The removal's failure survives its automatic reload, even if that reload repairs pending cleanup. */
internal fun connectionReloadFeedback(removalFailed: Boolean, recovery: Result<Boolean>, readFailed: Boolean): ConnectionReloadFeedback = when {
    removalFailed || readFailed -> ConnectionReloadFeedback(ConnectionReloadNotice.FAILURE, true)
    recovery.isFailure -> ConnectionReloadFeedback(ConnectionReloadNotice.PENDING, true)
    recovery.getOrDefault(false) -> ConnectionReloadFeedback(ConnectionReloadNotice.RECOVERED, false)
    else -> ConnectionReloadFeedback(ConnectionReloadNotice.NONE, false)
}
