package persona.feature.chat

import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.LiveWorkId

/**
 * Which work lines are open (#1614). [open] holds the lines the user opened, as in every chat. Where running lines open
 * on their own (Persona's own chat, [ChatRenderStyle.openLiveWork]) a tap on a running line goes to [liveFolded]
 * instead: the running lines the user folded. Neither set gets a running line's id there, so when the turn ends its
 * line folds back to its summary whatever was tapped during the run; [settleLiveFolds] then forgets those folds.
 */
internal data class WorkOpenState(val open: Set<String> = emptySet(), val liveFolded: Set<String> = emptySet())

/** The running work lines among [lines], those in a wake drawer too. */
internal fun liveWorkIds(lines: List<ChatLine>): Set<String> = buildSet {
    fun visit(list: List<ChatLine>) {
        for (line in list) when (line) {
            is ChatLine.Work -> if (line.live) add(line.id)
            is ChatLine.Wakes -> visit(line.drawer)
            else -> {}
        }
    }
    visit(lines)
}

/** A tap on work line [id], running ([live]) or not: the new state, and whether the line is open now. */
internal fun toggleWork(state: WorkOpenState, id: String, live: Boolean, openLive: Boolean): Pair<WorkOpenState, Boolean> =
    if (live && openLive) {
        val folding = id !in state.liveFolded
        state.copy(liveFolded = if (folding) state.liveFolded + id else state.liveFolded - id) to !folding
    } else {
        val opening = id !in state.open
        state.copy(open = if (opening) state.open + id else state.open - id) to opening
    }

/**
 * The folds of running lines that still run ([live]): a turn that ended drops its fold, so the next turn's line opens
 * again. A fold of the placeholder line drawn before the turn has items of its own ([LiveWorkId]) moves to the line
 * that takes its place.
 */
internal fun settleLiveFolds(folded: Set<String>, live: Set<String>): Set<String> {
    if (folded.isEmpty()) return folded
    val kept = folded intersect live
    return if (LiveWorkId in folded && LiveWorkId !in live) kept + live else kept
}
