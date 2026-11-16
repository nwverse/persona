package persona.feature.inbox

import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import persona.core.domain.inbox.InboxBar

/**
 * Where the Inbox list goes when an item is opened from outside it (#9053, docs/assistant/inbox.md "Section bar"): a
 * link, the Persona home's Needs-you line or another item's chain. The item's link sets it once it knows the item; the
 * list takes it, now if it is on screen (beside the item on a wide screen) or when Back shows it.
 */
@Stable
class InboxLanding {
    var pending by mutableStateOf<InboxBar.Landing?>(null)
        private set

    /** [record] was opened from outside the list: Done for a done item, All with Collapsed open for a collapsed one. */
    fun land(record: InboxRecord) = land(InboxBar.landing(record.item, working = record.agentWorking))

    /** The list goes [to] a choice the next time it shows. */
    fun land(to: InboxBar.Landing) { pending = to }

    /** The landing not yet taken, once. */
    fun take(): InboxBar.Landing? = pending.also { pending = null }
}

/** The shell's [InboxLanding]; none: items open only from the list, which never moves it. */
val LocalInboxLanding = staticCompositionLocalOf<InboxLanding?> { null }
