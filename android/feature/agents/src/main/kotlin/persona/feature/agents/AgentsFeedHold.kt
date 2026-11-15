package persona.feature.agents

import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import persona.core.designsystem.JLatestPill
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status

/** The quiet note over the list: what came while it was read further down. The most urgent kind speaks; a tap goes to the latest. */
@Composable
internal fun AgentsFeedNote(feed: AgentsFeedView, modifier: Modifier = Modifier) {
    val notice = feed.notice
    if (notice.isEmpty) return
    val (text, status) = when {
        notice.needsYou > 0 -> pluralStringResource(R.plurals.agents_feed_needs_you, notice.needsYou, notice.needsYou) to Status.NeedsYou
        notice.offline > 0 -> pluralStringResource(R.plurals.agents_feed_offline, notice.offline, notice.offline) to Status.Offline
        else -> pluralStringResource(R.plurals.agents_feed_new, notice.fresh, notice.fresh) to null
    }
    JLatestPill(text, stringResource(persona.core.designsystem.R.string.designsystem_jump_latest), feed.showLatest, modifier, status)
}

/** What the Agents list holds: the page to draw, what came meanwhile, and the way back to the latest. */
internal class AgentsFeedView(val page: AgentsPageState, val notice: AgentsFeedNotice, val showLatest: () -> Unit)

@Stable
internal class AgentsFeed {
    /** Set while the list is scrolled: the page as it stood when that began. */
    var frozen by mutableStateOf<AgentsFeedFrozen?>(null)
        private set
    /** What the page said when the user left the top; null at the top. */
    var baseline by mutableStateOf<AgentsFeedMark?>(null)
        private set

    fun freeze(page: AgentsPageState, scope: List<Any?>) { if (frozen == null) frozen = AgentsFeedFrozen(page, scope) }
    fun release() { frozen = null }
    fun rebase(mark: AgentsFeedMark?) { baseline = mark }
}

/**
 * Holds the page still while the list scrolls, for at most [AgentsFeedTiming.CAP_MILLIS], and lands it once the list has
 * rested [AgentsFeedTiming.SETTLE_MILLIS], when it reaches the top, or when a control changes ([holdsAgainst]). Nothing is
 * dropped: what came meanwhile is the next page, and the reading position keeps the row being read where it was as it lands.
 * A new Needs You, a session or computer gone down and new rows do not wait for that: [AgentsFeedView.notice] says so at
 * once, away from the top, and [AgentsFeedView.showLatest] goes there and lets the hold go.
 *
 * [requests] are the session ids of the requests waiting on the user; [scope] the controls the list is drawn under.
 */
@Composable
internal fun rememberAgentsFeed(latest: AgentsPageState, requests: Set<String>, scope: List<Any?>, list: LazyListState): AgentsFeedView {
    val feed = remember { AgentsFeed() }
    val held = feed.frozen?.takeIf { holdsAgainst(it, latest, scope) }
    val shown = held?.page ?: latest
    // A hold that no longer applies lets go for good, or a later scroll would find it again.
    if (feed.frozen != null && held == null) SideEffect { feed.release() }

    val shownNow by rememberUpdatedState(shown)
    val latestNow by rememberUpdatedState(latest)
    val requestsNow by rememberUpdatedState(requests)
    val scopeNow by rememberUpdatedState(scope)

    LaunchedEffect(feed, list) {
        snapshotFlow { list.isScrollInProgress }.collectLatest { scrolling ->
            if (scrolling) feed.freeze(shownNow, scopeNow)
            else {
                delay(AgentsFeedTiming.SETTLE_MILLIS)
                feed.release()
            }
        }
    }
    // However long the list keeps moving, what waits lands after the cap.
    val waiting = held != null && latest !== held.page
    LaunchedEffect(feed.frozen, waiting) {
        if (waiting) {
            delay(AgentsFeedTiming.CAP_MILLIS)
            feed.release()
        }
    }
    val away by remember(list) { derivedStateOf { list.canScrollBackward } }
    // Back at the latest: nothing waits and nothing is new. Further down, what is new is what comes after this.
    LaunchedEffect(feed, away) { if (!away) feed.release() }
    LaunchedEffect(feed, away, scope) { feed.rebase(if (away) agentsFeedMark(latestNow, requestsNow) else null) }

    val baseline = feed.baseline
    val notice = remember(baseline, latest, requests) {
        if (baseline == null) AgentsFeedNotice() else agentsFeedNotice(baseline, agentsFeedMark(latest, requests))
    }
    val scroll = rememberCoroutineScope()
    val reduceMotion = PersonaTheme.reduceMotion
    return AgentsFeedView(shown, notice) {
        feed.release()
        scroll.launch { if (reduceMotion) list.scrollToItem(0) else list.animateScrollToItem(0) }
    }
}
