package persona.feature.settings

import androidx.lifecycle.Lifecycle
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withTimeoutOrNull

/**
 * How long a code step (a QR code, a device or pairing code) goes on asking the hub after its page stopped: another tab,
 * the app in the background, the screen off. These steps send the user to another app (scan with WeChat, approve in a
 * browser), and the hub finishes the sign-in only when asked (a WeChat QR code lives 5 minutes on the hub, then it makes
 * a new one), so the asks go on long enough for that; after it, nothing until the page is back.
 */
internal const val SignInAwayMillis = 5 * 60_000L

/**
 * Asks about the code on screen while [shown]: [poll] as usual, and still for [awayMillis] once the page is not shown.
 * Back in front after being away, [catchUp] first (one ask at once, so a sign-in finished elsewhere shows without
 * waiting an interval), then [poll] again. One ask at a time: each change of [shown] cancels the one before.
 */
internal suspend fun pollWhileShown(
    shown: Flow<Boolean>, awayMillis: Long = SignInAwayMillis,
    catchUp: suspend () -> Unit, poll: suspend () -> Unit,
) {
    var away = false
    shown.distinctUntilChanged().collectLatest { front ->
        if (front) {
            if (away) catchUp()
            away = false
            poll()
        } else {
            away = true
            withTimeoutOrNull(awayMillis) { poll() }
        }
    }
}

/** A page is shown while its lifecycle is at least STARTED (a hidden shell tab is held at CREATED). */
internal fun Lifecycle.startedFlow(): Flow<Boolean> = currentStateFlow.map { it.isAtLeast(Lifecycle.State.STARTED) }
