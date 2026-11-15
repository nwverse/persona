package persona.feature.agents

import persona.core.contracts.AgentState
import persona.core.contracts.Interaction

// What the Agents list shows of the hub while someone reads it (docs/android/android.md "Reading position"): the page as
// it was when they began to scroll, for a bounded time, and a quiet note of what came meanwhile. Pure rules live here;
// AgentsFeedHold.kt drives them from the list's scroll state. The rows' own position is kept by AgentReadingPosition.kt.

/**
 * How long the page waits, as configured: neither is a measured latency.
 * A scroll that rests for [SETTLE_MILLIS] lets what waited land, and nothing waits longer than [CAP_MILLIS] however long
 * the list keeps moving (iOS `Hold.settle` / `Hold.cap`).
 */
internal object AgentsFeedTiming {
    const val SETTLE_MILLIS = 500L
    const val CAP_MILLIS = 5_000L
}

/** What the list drew when the hold began, and the controls it was drawn under ([scope], as the reading position's). */
internal class AgentsFeedFrozen(val page: AgentsPageState, val scope: List<Any?>)

/** The ids of the rows the list draws, in list order. */
internal fun AgentsPageState.rowIds(): List<String> = list.groups.flatMap { group -> group.rows.map { it.id } }

/**
 * Whether the page [held] still stands for [latest]. False lets the hold go for good, so the list shows [latest] and the
 * reading position starts over from it:
 * - a control changed (grouping, sort, filter, search, folds, window): [scope] differs;
 * - the first page is not in yet, or the held list had no rows, or the list emptied, or none of the rows held is left
 *   (nothing to keep still);
 * - a paged list grew: a fetched page is not a live insertion, and it must not leave a gap under the finger.
 * Everything else waits: reorders, new rows, state words and last lines.
 */
internal fun holdsAgainst(frozen: AgentsFeedFrozen, latest: AgentsPageState, scope: List<Any?>): Boolean {
    if (frozen.scope != scope || !latest.loaded) return false
    val held = frozen.page.rowIds()
    val rows = latest.rowIds()
    if (held.isEmpty() || rows.isEmpty() || rows.none(held.toSet()::contains)) return false
    if (frozen.page.pagination.paged != latest.pagination.paged) return false
    return !(latest.pagination.paged && growsPage(frozen.page, latest))
}

/** The hub's loaded window got longer with every session still in it, which a fetched page does and a push does not. */
internal fun growsPage(held: AgentsPageState, latest: AgentsPageState): Boolean {
    if (latest.sessions.size <= held.sessions.size) return false
    val keys = latest.sessions.mapTo(HashSet()) { it.key }
    return held.sessions.all { it.key in keys }
}

/** What the user has been told about, for [agentsFeedNotice] to tell what is new from. */
internal data class AgentsFeedMark(
    /** The rows the list draws. */
    val rows: Set<String>,
    /** Every session the hub's window holds, drawn or not. */
    val known: Set<String>,
    val needsYou: Set<String>,
    /** Sessions that are not running and computers that are down (their id after [MACHINE_DOWN]). */
    val offline: Set<String>,
    val paged: Boolean,
)

private const val MACHINE_DOWN = "machine:"

/** What [page] says now; [requests] are the session ids of the requests waiting on the user. */
internal fun agentsFeedMark(page: AgentsPageState, requests: Set<String>): AgentsFeedMark = AgentsFeedMark(
    rows = page.rowIds().toSet(),
    known = page.sessions.mapTo(HashSet()) { it.key.path },
    needsYou = page.sessions.filter { it.agentState == AgentState.NeedsYou }.mapTo(HashSet()) { it.key.path } + requests,
    offline = page.sessions.filter { it.agentState == AgentState.Offline }.mapTo(HashSet()) { it.key.path } +
        page.machines.filter { !it.online || !it.error.isNullOrEmpty() }.map { MACHINE_DOWN + it.id },
    paged = page.pagination.paged,
)

/** The session ids of the requests that wait on the user. */
internal fun requestSessions(requests: List<Interaction>): Set<String> = requests.mapTo(HashSet()) { it.sessionKey.path }

/** What came while the list was read further down: the counts say what, and [urgent] ones need the user or are down. */
internal data class AgentsFeedNotice(val needsYou: Int = 0, val offline: Int = 0, val fresh: Int = 0) {
    val isEmpty: Boolean get() = needsYou == 0 && offline == 0 && fresh == 0
    val urgent: Boolean get() = needsYou > 0 || offline > 0
}

/**
 * What [latest] has that [baseline] (the mark when the user left the top) did not: sessions that now need the user, that
 * stopped running or lost their computer, and rows that are new. A paged list counts no new rows, since a fetched page
 * is older history and a pushed session cannot be told from it by id alone. Nothing before the user has left the top.
 */
internal fun agentsFeedNotice(baseline: AgentsFeedMark?, latest: AgentsFeedMark): AgentsFeedNotice {
    if (baseline == null) return AgentsFeedNotice()
    val needsYou = latest.needsYou - baseline.needsYou
    return AgentsFeedNotice(
        needsYou = needsYou.size,
        // A session counts only if the baseline saw it running: history that a page brings in is not news.
        offline = (latest.offline - baseline.offline).count { it.startsWith(MACHINE_DOWN) || it in baseline.known },
        fresh = if (latest.paged || baseline.paged) 0 else (latest.rows - baseline.rows - needsYou).size,
    )
}
