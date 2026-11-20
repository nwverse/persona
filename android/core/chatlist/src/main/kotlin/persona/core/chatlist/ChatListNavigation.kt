package persona.core.chatlist

import androidx.compose.runtime.withFrameNanos

/** Capture the visible key and pixel offset before opening a reading tool. */
fun ChatListState.readingPosition(): ChatReadingPosition {
    capture()
    return position
}

/** Keep the currently visible message for Back, including from a chat that was following new messages. */
fun ChatListState.saveHandoffPosition(): Boolean {
    if (!restored || readingPosition().rowId == null) return false
    holdPosition()
    persist()
    return true
}

/** The owner freezes its transcript alongside this position. */
fun ChatListState.holdPosition() {
    epoch++
    dragged = false
    restored = true
    position = position.copy(followsEnd = false)
}

/** Resolve against current keys so prepended history and pending messages cannot shift the target. */
suspend fun ChatListState.scrollToEntry(entry: ChatListEntry, alignTop: Boolean = false): Boolean {
    val index = rowIds.indexOf(entry.rowId)
    if (index < 0) return false
    holdPosition()
    programmaticScroll = true
    try {
        list.scrollToItem(index, entry.offsetPx)
        withFrameNanos { }
        if (alignTop) {
            val visible = list.layoutInfo.visibleItemsInfo.firstOrNull { it.key == entry.rowId } ?: return false
            val currentIndex = rowIds.indexOf(entry.rowId)
            if (currentIndex < 0) return false
            list.scrollToItem(currentIndex, visible.size - list.layoutInfo.viewportEndOffset)
        }
        return true
    } finally {
        programmaticScroll = false
        capture()
    }
}
