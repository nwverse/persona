package persona.platform.phonetools

import android.app.*
import android.content.*
import android.net.Uri
import android.os.Build
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/**
 * How a confirmation ended (#1875). Only [Denied] is the user saying no: the agent hears "declined" for that alone,
 * and for the others that nobody answered, so it neither tells the user they refused nor saves without asking.
 */
enum class PhoneConfirmation {
    Allowed,
    Denied,
    /** Persona was in the background and could not post the notification (notifications or the channel off). */
    NotShown,
    /** The phone tools closed while it was open or queued (unpaired, disconnected, turned off). */
    Withdrawn,
    /** Its notification was swiped away (or cleared with "Clear all") without an answer: not a refusal (#3861). */
    Dismissed,
}

/**
 * One confirmation card at a time, the rest queue behind it (#4456, parity with iOS `PhoneConfirmations`).
 *
 * The hub owns how long a confirmed write may wait (`phoneConfirmWait`); there is no app-side timeout that
 * withdraws the card. A second write arriving while a card is on screen waits behind it instead of taking its
 * place, and every answer is routed by the token it names: a dialog dismissal that arrives after its button
 * already answered names a card that is answered already, so it does nothing to the next one.
 *
 * `close()` finishes the current card and every queued one as [PhoneConfirmation.Withdrawn]. A cancelled
 * awaiter (the call was dropped because the connection died, a permission was revoked, the runtime is closing)
 * removes its own entry: if it was current, the next one is promoted; if it was still queued, it just leaves.
 *
 * Tokens are valid only while their entry is current or queued. Nothing is persisted.
 */
class PhoneConfirmations(context: Context) : AutoCloseable {
    private data class Entry(
        val token: String,
        val summary: String,
        val deferred: CompletableDeferred<PhoneConfirmation>,
    )

    private val context = context.applicationContext
    private val manager = context.getSystemService(NotificationManager::class.java)
    private val keyguard = context.getSystemService(KeyguardManager::class.java)

    private val lock = Any()
    private val queue = ArrayDeque<Entry>()
    private var current: Entry? = null
    private var closed = false

    private val _pending = MutableStateFlow<PendingPhoneConfirmation?>(null)
    val pending = _pending.asStateFlow()

    @Volatile private var foreground = false

    suspend fun confirm(text: PhoneConfirmationText): PhoneConfirmation {
        val summary = text(context.resources)
        val token = UUID.randomUUID().toString()
        val entry = Entry(token, summary, CompletableDeferred())

        synchronized(lock) {
            if (closed) return PhoneConfirmation.Withdrawn
            callbacks[token] = { resolve(token, it) }
            dismissals[token] = { dismiss(token) }
            queue.addLast(entry)
            if (current == null) promoteLocked()
        }
        // Post the notification outside the lock: `NotificationManager.notify` may block on a system call and we
        // must not hold the gate while it does. A concurrent resolve()/dismiss() between promote and this call is
        // fine because by then `callbacks`/`dismissals` no longer carry this token and the cancel below clears it.
        maybeNotify(entry)

        return try {
            entry.deferred.await()
        } catch (cancelled: CancellationException) {
            synchronized(lock) {
                callbacks.remove(token); dismissals.remove(token)
                if (current == entry) {
                    current = null
                    _pending.value = null
                    promoteLocked()
                } else {
                    queue.remove(entry)
                }
            }
            manager.cancel(token, 0)
            throw cancelled
        }
    }

    /** The user answered [token]. Allow needs an unlocked device; Deny works from the lock screen. */
    fun resolve(token: String, allowed: Boolean) {
        val entry = synchronized(lock) {
            val e = current ?: return
            if (e.token != token) return
            if (allowed && keyguard.isDeviceLocked) return
            current = null
            _pending.value = null
            callbacks.remove(token); dismissals.remove(token)
            promoteLocked()
            e
        }
        manager.cancel(token, 0)
        entry.deferred.complete(if (allowed) PhoneConfirmation.Allowed else PhoneConfirmation.Denied)
    }

    /** The notification of [token] was swiped away: end only that one (#3861). */
    fun dismiss(token: String) {
        val entry = synchronized(lock) {
            val e = current ?: return
            if (e.token != token) return
            current = null
            _pending.value = null
            callbacks.remove(token); dismissals.remove(token)
            promoteLocked()
            e
        }
        manager.cancel(token, 0)
        entry.deferred.complete(PhoneConfirmation.Dismissed)
    }

    fun setForeground(value: Boolean) {
        foreground = value
        val entry = synchronized(lock) { current } ?: return
        if (value) {
            // Back in the foreground: the dialog takes over from the notification.
            manager.cancel(entry.token, 0)
        } else {
            // Going to background: try the notification for the current card only. Queued entries wait their turn.
            if (!showNotification(entry)) {
                // No notifications: end just this one as NotShown. Others queue behind it and try when promoted.
                failEntry(entry, PhoneConfirmation.NotShown)
            }
        }
    }

    /** Finish [entry] with [outcome], promoting the next queued one if it was current. */
    private fun failEntry(entry: Entry, outcome: PhoneConfirmation) {
        val accepted = synchronized(lock) {
            if (current != entry) return
            current = null
            _pending.value = null
            callbacks.remove(entry.token); dismissals.remove(entry.token)
            promoteLocked()
            true
        }
        if (accepted) {
            manager.cancel(entry.token, 0)
            entry.deferred.complete(outcome)
        }
    }

    /** Promote the next queued entry to current if the slot is empty. Caller holds [lock]. */
    private fun promoteLocked() {
        if (current != null || closed) return
        val next = queue.removeFirstOrNull() ?: return
        current = next
        _pending.value = PendingPhoneConfirmation(next.token, next.summary)
    }

    /** Post the background notification for [entry] if we are not in the foreground. */
    private fun maybeNotify(entry: Entry) {
        if (foreground) return
        // If the entry is no longer current by the time we get here (a fast resolve/dismiss/close), skip.
        val isCurrent = synchronized(lock) { current == entry }
        if (!isCurrent) return
        if (!showNotification(entry)) failEntry(entry, PhoneConfirmation.NotShown)
    }

    private fun showNotification(entry: Entry): Boolean {
        // A resolve/dismiss/close that landed between the promote and this call must not resurrect the card:
        // pretend success so the caller does not then fail an entry that is already finished.
        val isCurrent = synchronized(lock) { current == entry && !closed }
        if (!isCurrent) return true
        return try {
            manager.createNotificationChannel(NotificationChannel(CHANNEL, context.getString(R.string.phonetools_confirm_channel), NotificationManager.IMPORTANCE_HIGH))
            if (!manager.areNotificationsEnabled() || manager.getNotificationChannel(CHANNEL).importance == NotificationManager.IMPORTANCE_NONE) return false
            fun action(allowed: Boolean): Notification.Action {
                val intent = Intent(context, PhoneConfirmationReceiver::class.java)
                    .setAction("persona.phone.confirm").setData(Uri.parse("persona-phone://confirmation/${entry.token}/$allowed"))
                    .putExtra("token", entry.token).putExtra("allowed", allowed)
                val pendingIntent = PendingIntent.getBroadcast(context, 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
                val builder = Notification.Action.Builder(null, context.getString(if (allowed) R.string.phonetools_allow else R.string.phonetools_deny), pendingIntent)
                // setAuthenticationRequired is API 31; calling it on API 30 throws NoSuchMethodError (an Error, so the
                // catch below misses it) and no confirmation ever shows. On API 30 the system does not ask for an unlock
                // before sending the button, so allow can be tapped straight from the lock screen; resolve() and the
                // receiver still refuse it while the device is locked, so that tap is dropped (the notification stays):
                // unlock, then tap allow or open the app. Trade-off: one more step on API 30 instead of no confirmation.
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) builder.setAuthenticationRequired(allowed)
                return builder.build()
            }
            val builder = Notification.Builder(context, CHANNEL).setSmallIcon(persona.core.designsystem.R.drawable.ic_stat)
                .setContentTitle(context.getString(R.string.phonetools_confirm_notification)).setContentText(entry.summary)
                .setStyle(Notification.BigTextStyle().bigText(entry.summary)).setVisibility(Notification.VISIBILITY_PRIVATE)
                .setCategory(Notification.CATEGORY_RECOMMENDATION)
                .addAction(action(false)).addAction(action(true))
                // #3861: a swipe or "Clear all" ends the confirmation at once instead of waiting for an answer that
                // will never come. The hub's `phoneConfirmWait` decides how long the call may still be answered.
                .setDeleteIntent(PendingIntent.getBroadcast(context, 0, Intent(context, PhoneConfirmationReceiver::class.java)
                    .setAction("persona.phone.confirm").setData(Uri.parse("persona-phone://confirmation/${entry.token}/dismissed"))
                    .putExtra("token", entry.token).putExtra(DISMISSED, true),
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
            context.packageManager.getLaunchIntentForPackage(context.packageName)?.let { intent ->
                builder.setContentIntent(PendingIntent.getActivity(context, 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
            }
            manager.notify(entry.token, 0, builder.build()); true
        } catch (_: Exception) { false }
    }

    override fun close() {
        val all = synchronized(lock) {
            if (closed) return
            closed = true
            val list = buildList {
                current?.let(::add)
                addAll(queue)
            }
            queue.clear()
            current = null
            _pending.value = null
            for (e in list) {
                callbacks.remove(e.token); dismissals.remove(e.token)
            }
            list
        }
        for (e in all) {
            manager.cancel(e.token, 0)
            e.deferred.complete(PhoneConfirmation.Withdrawn)
        }
    }

    companion object {
        /** Extra on the notification's delete intent: it was dismissed, not answered. */
        internal const val DISMISSED = "dismissed"
        internal const val CHANNEL = "phone_tools_confirmation"
        internal val callbacks = ConcurrentHashMap<String, (Boolean) -> Unit>()
        internal val dismissals = ConcurrentHashMap<String, () -> Unit>()
    }
}

data class PendingPhoneConfirmation(val token: String, val summary: String)

class PhoneConfirmationReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action !in persona.core.contracts.ProtocolCompatibility.phoneConfirmationActions) return
        val token = intent.getStringExtra("token") ?: return
        if (intent.getBooleanExtra(PhoneConfirmations.DISMISSED, false)) { PhoneConfirmations.dismissals[token]?.invoke(); return }
        val allowed = intent.getBooleanExtra("allowed", false)
        if (!allowed) { PhoneConfirmations.callbacks[token]?.invoke(false); return }
        // "Allow" needs an unlock, and the lock screen reports it a moment after it let the button through.
        val keyguard = context.getSystemService(KeyguardManager::class.java)
        val pending = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.Main).launch {
            try { if (!lockedAfterUnlockSettles({ keyguard.isDeviceLocked })) PhoneConfirmations.callbacks[token]?.invoke(true) }
            finally { pending.finish() }
        }
    }
}

/**
 * Whether the phone is still locked once an unlock in progress has had [settleMillis] to land. SystemUI sends a
 * notification button that needs authentication as soon as the user's PIN is accepted, a moment before KeyguardManager
 * stops reporting the device as locked: read at once, "Allow" tapped on the lock screen was dropped and the call declined
 * when the confirmation timed out (docs/lessons/android.md; the same as :platform:notify's).
 */
internal suspend fun lockedAfterUnlockSettles(
    locked: () -> Boolean,
    settleMillis: Long = UNLOCK_SETTLE_MILLIS,
    pollMillis: Long = 100,
): Boolean {
    if (!locked()) return false
    return withTimeoutOrNull(settleMillis) {
        while (locked()) delay(pollMillis)
        false
    } ?: true
}

/** How long an unlock that let a button through may take to show (config value, not a measurement). */
internal const val UNLOCK_SETTLE_MILLIS = 3_000L
