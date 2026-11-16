package persona.feature.inbox

import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.navigation3.runtime.NavKey
import persona.core.domain.inbox.InboxBar

/**
 * Where one stack's Inbox list goes when an item is opened from outside it (#9053, docs/assistant/inbox.md "Section
 * bar"): a link, the Persona home's Needs-you line or, beside the list, another item's chain. The shell keeps one per
 * stack, so a list composed in a hidden tab never takes another stack's landing. The item's link sets it once it knows
 * the item; the list takes it, now if it is drawn (beside the item on a wide screen) or when Back shows it. A landing the
 * list never takes is dropped once its stack lets go of the list ([clear]), so a later entry still opens on Needs you.
 * A list moved beside an item it opened goes back to the choice it had when that page closes ([closed]).
 */
@Stable
class InboxLanding {
    var pending by mutableStateOf<InboxBar.Landing?>(null)
        private set

    /** The page [pending] was set for beside the list ([landBeside]); null for an opening from outside the Inbox. */
    internal var pendingPage: NavKey? = null
        private set

    /** The choice to put back, once, when a page that moved the list beside it closed ([closed]). */
    var restore by mutableStateOf<InboxBar.State?>(null)
        private set

    /** The pages opened beside the list that moved it, oldest first, each with the bar as it was before. */
    private val returns = mutableListOf<Pair<NavKey, InboxBar.State>>()

    /** How many of this stack's Inbox lists are drawn now: the one beside an item on a wide screen; none on a phone. */
    internal var drawn = 0

    /** [record] was opened from outside the list: Done for a done item, All with Collapsed open for a collapsed one. */
    fun land(record: InboxRecord) = land(InboxBar.landing(record.item, working = record.agentWorking))

    /** The list goes [to] a choice the next time it shows. */
    fun land(to: InboxBar.Landing) { pending = to; pendingPage = null }

    /**
     * [record] was opened as [page] from an item (its chain, its thread): the list moves only while it is drawn beside
     * that item, and goes back when [page] closes. On a phone the list waits under the item as the user left it, so
     * Back returns to where they were.
     */
    fun landBeside(record: InboxRecord, page: NavKey) {
        if (drawn > 0) { land(record); pendingPage = page }
    }

    /** The list took a landing made beside it for [page]: [before] is the bar to put back when that page closes. */
    internal fun moved(page: NavKey, before: InboxBar.State) { returns += page to before }

    /** The user chose a section: nothing closing later puts an older choice back over theirs. */
    internal fun picked() { returns.clear() }

    /**
     * This stack now holds [stack]: when a page that moved the list beside it is gone (Back, closing it, another item
     * in its place), the list goes back to what it showed before the first of those that left.
     */
    fun closed(stack: List<NavKey>) {
        val gone = returns.indexOfFirst { (page, _) -> page !in stack }
        if (gone < 0) return
        restore = returns[gone].second
        returns.subList(gone, returns.size).clear()
    }

    /**
     * The Inbox entered afresh (a widget, a quick action, a link to it) while a list of it may still sit under other
     * pages: that list opens as on entry, on Needs you, rather than on the choice it was left on.
     */
    fun enter() { pending = ENTERED; pendingPage = null; returns.clear(); restore = null }

    /** The stack no longer holds an Inbox list: nothing will take what is pending or put back. */
    fun clear() { pending = null; pendingPage = null; returns.clear(); restore = null }

    /** The landing not yet taken, once. */
    fun take(): InboxBar.Landing? = pending.also { pending = null; pendingPage = null }

    /** The choice to put back, once. */
    internal fun takeRestore(): InboxBar.State? = restore.also { restore = null }

    companion object {
        /** [enter]'s landing: no choice of its own, the list opens as on entry ("": [InboxBar.entered]). */
        internal val ENTERED = InboxBar.Landing("", false)
    }
}

/** This stack's [InboxLanding]; none: items open only from the list, which never moves it. */
val LocalInboxLanding = staticCompositionLocalOf<InboxLanding?> { null }
