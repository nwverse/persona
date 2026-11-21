package dev.persona.watch.pet

import dev.persona.watch.data.HubConfig
import dev.persona.watch.HubGroupRead
import dev.persona.watch.data.HubGroupSnapshot
import dev.persona.watch.data.HubGroups
import dev.persona.watch.data.WatchSnapshot
import java.time.LocalTime

/** Unlike immutable art, task-derived state belongs to the exact authenticated connection. */
internal fun petStatusScope(config: HubConfig): String = PetPack.hash(
    listOf(config.baseUrl, config.token, config.fingerprint.orEmpty(), config.deviceId.orEmpty())
        .joinToString("\u0000").toByteArray()).take(32)

internal fun petStatusPreferencesName(config: HubConfig) = "pet_state_" + petStatusScope(config)

internal data class PetReachability(val allFailed: Boolean, val partlyFailed: Boolean) {
    companion object {
        fun from(read: HubGroupRead?, groupCount: Int) = PetReachability(
            read == null || groupCount <= 0 || read.failed.size == groupCount,
            read?.failed?.isNotEmpty() == true)
    }
}

/** Shared by the page and MainActivity's bridge, without advancing freshness on a failed read. */
internal object PetFreshness {
    fun next(previous: PetStoredStatus, snapshot: WatchSnapshot?, unreachable: Boolean,
             partlyUnreachable: Boolean = false, now: Long = System.currentTimeMillis(),
             hour: Int = LocalTime.now().hour): PetStoredStatus {
        // A successful read replaces a previous future date after clock rollback. Failed reads
        // may retain a date, but cannot inject a future value into subsequent successful polls.
        fun safe(date: Long) = date.coerceIn(0, now.coerceAtLeast(0))
        val date = if (!unreachable && snapshot != null && snapshot.updatedAt > 0) safe(snapshot.updatedAt)
            else maxOf(safe(previous.updatedAt), safe(snapshot?.updatedAt ?: 0))
        val next = PetStoredStatus(PetStatus.from(snapshot, unreachable, hour, partlyUnreachable), date)
        return next.copy(status = next.visible(now))
    }

    /** Filter each Hub at its own deadline, then bound the published aggregate by its oldest input. */
    fun groups(previous: PetStoredStatus, read: HubGroupRead?, groupCount: Int,
               now: Long = System.currentTimeMillis(), hour: Int = LocalTime.now().hour): PetStoredStatus {
        val reachability = PetReachability.from(read, groupCount)
        if (read == null) return next(previous, null, true, now = now, hour = hour)
        // Never rebase retained future-dated group data on every timer wake. A new successful
        // fetch has its own current-clock timestamp and can replace it on the next poll.
        val usable = read.cache.filterValues { it.updatedAt <= now &&
            PetStoredStatus(PetStatus(PetState.IDLE), it.updatedAt).freshUntil(now) != null }
        if (usable.isEmpty()) return next(previous, null, true, now = now, hour = hour)
        val oldest = usable.values.minOf { it.updatedAt }
        val merged = HubGroups.merge(usable.map { (id, snapshot) ->
            HubGroupSnapshot(id, "", snapshot, id !in read.failed)
        }, oldest)
        return next(previous, merged, reachability.allFailed, reachability.partlyFailed, now, hour)
    }
}

/** A synchronous caller may notify or remove a restore backup only after this commit succeeds. */
internal fun commitPetPublication(stored: PetStoredStatus, commit: (PetStoredStatus) -> Boolean,
                                  requestUpdate: () -> Unit): PetStoredStatus {
    check(commit(stored)) { "Could not save pet state" }
    requestUpdate()
    return stored
}

internal fun petPublicationNeedsUpdate(previous: PetStoredStatus, next: PetStoredStatus,
                                       lastRequest: Long, now: Long): Boolean =
    previous.status != next.status ||
        previous.freshUntil(now) == null && next.freshUntil(now) != null ||
        now - lastRequest > 20 * 60 * 1000

/** Connection-owned page data. A paused/replaced connection cannot accept an old worker completion. */
internal class PetPageState {
    var connection: HubConfig? = null
        private set
    @Volatile var generation = 0
        private set
    var manifest: PetManifest? = null
        private set
    var groups = emptyMap<String, WatchSnapshot>()
        private set
    var stored = PetStoredStatus(PetStatus(PetState.OFFLINE), 0)
        private set
    private var groupRead: HubGroupRead? = null
    private var groupCount = 0

    fun resume(next: HubConfig?, saved: PetStoredStatus, now: Long): Boolean {
        val changed = next != connection
        if (changed) {
            invalidate()
            connection = next
            manifest = null
            groups = emptyMap()
        }
        // Another foreground page may have published fresher state while this one was paused.
        groupRead = null
        stored = if (next == null) PetStoredStatus(PetStatus(PetState.OFFLINE), 0) else saved
        revalidate(now)
        return changed
    }

    fun invalidate() { generation++ }
    fun accepts(ticket: Int) = ticket == generation && connection != null
    fun acceptManifest(ticket: Int, next: PetManifest): Boolean {
        if (!accepts(ticket)) return false
        manifest = next
        return true
    }

    fun acceptRefresh(ticket: Int, nextManifest: PetManifest?, read: HubGroupRead?, count: Int, now: Long): Boolean {
        if (!accepts(ticket)) return false
        manifest = nextManifest
        groupRead = read.takeIf { nextManifest != null }
        groupCount = count
        if (read != null) groups = read.cache
        stored = if (nextManifest == null) stored.copy(status = stored.visible(now))
            else PetFreshness.groups(stored, read, count, now)
        return true
    }

    fun publication(): PetStoredStatus? = stored.takeIf { connection != null && manifest != null }

    fun revalidate(now: Long): Boolean {
        val next = groupRead?.let { PetFreshness.groups(stored, it, groupCount, now) }
            ?: stored.copy(status = stored.visible(now))
        val changed = stored.status != next.status
        stored = next
        return changed
    }

    fun expiryDelay(now: Long): Long? = stored.freshUntil(now)?.let { it - now }
}

/** Static mode never calls the clip decoder, including a tap requested while motion is disabled. */
internal data class PetPlayback(val state: String, val clip: String, val animated: Boolean) {
    data class Artwork<T>(val still: T?, val frames: List<T>)
    fun <T> load(still: (String) -> T?, frames: (String) -> List<T>): Artwork<T> =
        Artwork(still(state), if (animated) frames(clip) else emptyList())

    companion object {
        fun plan(state: PetState, requested: String, motion: Boolean, interactive: Boolean): PetPlayback {
            val animated = motion && interactive
            return PetPlayback(state.wire, if (animated) requested else state.wire, animated)
        }
    }
}

/** Transfer output ownership only on success, including Error/OOM failures after earlier frames. */
internal fun <T> collectPetFrames(capacity: Int, recycle: (T) -> Unit, decode: (MutableList<T>) -> Unit): List<T> {
    val output = ArrayList<T>(capacity)
    try {
        decode(output)
        return output
    } catch (failure: Throwable) {
        output.forEach { frame ->
            try { recycle(frame) } catch (cleanup: Throwable) { failure.addSuppressed(cleanup) }
        }
        throw failure
    }
}
