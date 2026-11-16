package persona.feature.inbox

import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import persona.core.domain.inbox.InboxBar

/**
 * Where one stack's Inbox list goes when an item is opened from outside it (#9053, docs/assistant/inbox.md "Section
 * bar"): a link, the Persona home's Needs-you line or, beside the list, another item's chain. The shell keeps one per
 * stack, so a list composed in a hidden tab never takes another stack's landing. The item's link sets it once it knows
 * the item; the list takes it, now if it is drawn (beside the item on a wide screen) or when Back shows it. A landing the
 * list never takes is dropped once its stack lets go of the list ([clear]), so a later entry still opens on Needs you.
 */
@Stable
class InboxLanding {
    var pending by mutableStateOf<InboxBar.Landing?>(null)
        private set

    /** How many of this stack's Inbox lists are drawn now: the one beside an item on a wide screen; none on a phone. */
    internal var drawn = 0

    /** [record] was opened from outside the list: Done for a done item, All with Collapsed open for a collapsed one. */
    fun land(record: InboxRecord) = land(InboxBar.landing(record.item, working = record.agentWorking))

    /** The list goes [to] a choice the next time it shows. */
    fun land(to: InboxBar.Landing) { pending = to }

    /**
     * [record] was opened from an item (its chain, its thread): the list moves only while it is drawn beside that item.
     * On a phone the list waits under the item as the user left it, so Back returns to where they were.
     */
    fun landBeside(record: InboxRecord) { if (drawn > 0) land(record) }

    /**
     * The Inbox entered afresh (a widget, a quick action, a link to it) while a list of it may still sit under other
     * pages: that list opens as on entry, on Needs you, rather than on the choice it was left on.
     */
    fun enter() { pending = ENTERED }

    /** The stack no longer holds an Inbox list: nothing will take what is pending. */
    fun clear() { pending = null }

    /** The landing not yet taken, once. */
    fun take(): InboxBar.Landing? = pending.also { pending = null }

    companion object {
        /** [enter]'s landing: no choice of its own, the list opens as on entry ("": [InboxBar.entered]). */
        internal val ENTERED = InboxBar.Landing("", false)
    }
}

/** This stack's [InboxLanding]; none: items open only from the list, which never moves it. */
val LocalInboxLanding = staticCompositionLocalOf<InboxLanding?> { null }
