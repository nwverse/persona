package dev.persona.watch.extensions

internal data class RecoveredWidgetSelection(
    val widgets: List<ExtensionWidget>, val selected: ExtensionWidget?, val selectedKey: String?,
    val projection: CachedWidget?,
)

/** Resume can hide private content while checking credentials without losing a key-only selection. */
internal fun recoverWidgetSelection(
    mayKeep: Boolean, sameConnection: Boolean, requestedKey: String?,
    previousWidgets: List<ExtensionWidget>, previousSelectedKey: String?, previousProjection: CachedWidget?,
    savedWidgets: List<ExtensionWidget>, savedWidget: ExtensionWidget?, savedView: CachedWidget?,
    now: Long = System.currentTimeMillis(),
): RecoveredWidgetSelection {
    if (!mayKeep) return RecoveredWidgetSelection(emptyList(), null, null, null)
    val widgets = savedWidgets.ifEmpty { if (sameConnection) previousWidgets else emptyList() }
    val selected = savedWidget ?: widgets.firstOrNull { it.key == requestedKey }
        ?: previousWidgets.firstOrNull { sameConnection && it.key == requestedKey }
    val projection = retainWidgetProjection(selected, sameConnection,
        previousProjection.takeIf { previousSelectedKey == selected?.key },
        savedView.takeIf { savedWidget?.key == selected?.key }, now)
    return RecoveredWidgetSelection(widgets, selected, selected?.key ?: requestedKey, projection)
}

/** Caller checks the saved credential before publishing; cache reuse never changes its fetch/expiry time. */
internal fun retainWidgetProjection(widget: ExtensionWidget?, sameConnection: Boolean,
                                   previous: CachedWidget?, saved: CachedWidget?, now: Long): CachedWidget? {
    val candidates = listOfNotNull(previous.takeIf { sameConnection }, saved)
        .filter { it.widget == widget && it.family == widget?.family }
    // Prefer still-valid content; an expired snapshot can remain visible only with its stale label.
    return candidates.firstOrNull { it.fresh(now) } ?: candidates.firstOrNull()
}

/** A bounded wake checks the original absolute deadline, never a newly extended expiry. */
internal object WidgetExpiry {
    const val MAX_WAIT_MS = 86_400_000L
    fun wake(deadline: Long, now: Long, schedule: (Long) -> Unit, publish: () -> Unit, render: () -> Unit) {
        val delay = nextDelay(deadline, now)
        if (delay != null) schedule(delay) else {
            publish()
            render()
        }
    }
    fun nextDelay(deadline: Long, now: Long): Long? {
        if (deadline <= now) return null
        // The positive difference can overflow only across widely separated signed timestamps.
        val remaining = deadline - now
        return if (remaining <= 0) MAX_WAIT_MS else minOf(remaining, MAX_WAIT_MS)
    }
}
