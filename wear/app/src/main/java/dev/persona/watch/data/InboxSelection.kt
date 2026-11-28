package dev.persona.watch.data

/** Only an authoritative primary Inbox snapshot may confirm that a selected item is current or gone. */
internal object InboxSelection {
    enum class State { CURRENT, CHANGED, REMOVED, UNAVAILABLE }
    data class Review(val item: InboxItem?, val state: State) {
        fun shouldRedraw(composerFocused: Boolean) = state != State.CURRENT || !composerFocused
    }

    fun review(expected: InboxItem, snapshot: WatchSnapshot?, primaryAvailable: Boolean): Review {
        if (!primaryAvailable || snapshot == null || snapshot.inboxUnavailable) return Review(expected, State.UNAVAILABLE)
        val current = snapshot.inbox.firstOrNull { it.id == expected.id && it.manager == expected.manager }
        return Review(current, when { current == null -> State.REMOVED; current != expected -> State.CHANGED; else -> State.CURRENT })
    }

    /** Recheck at the final action/confirmation tap, before creating any reply, draft or done mutation. */
    fun act(expected: InboxItem, snapshot: WatchSnapshot?, primaryAvailable: Boolean,
            rejected: (Review) -> Unit, action: () -> Unit) {
        val current = review(expected, snapshot, primaryAvailable)
        if (current.state == State.CURRENT) action() else rejected(current)
    }
}
