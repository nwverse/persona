package persona.feature.chat

import androidx.compose.runtime.Stable
import androidx.compose.runtime.mutableStateMapOf
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import persona.core.contracts.api.SessionPathStats

/**
 * Which paths a conversation names are there on its machine (web `lib/path-presence.ts`, iOS `FilePresence`): only
 * those are linked, in the text and as chips. Asked in batches a moment after rows show, all in the session's working
 * directory, and remembered: a path that is there for 30 minutes, a missing one for 2 (the agent may be about to write
 * it), no answer for 1. Until the answer comes, and when none can (an old node, no network), a path is plain text.
 * [ask] runs on the main thread, with a main-thread [scope] (the chat page's); [kind] and [linkable] read snapshot state
 * and work from any thread (the page's prewarm calls them on a background dispatcher).
 */
@Stable
internal class PathPresence(
    private val scope: CoroutineScope,
    /** `GET …/stat` for the session's machine and directory. */
    private val stat: suspend (List<String>) -> SessionPathStats,
    private val now: () -> Long = System::currentTimeMillis,
) {
    private class Entry(val kind: String?, val at: Long)

    // Snapshot state: rows that read a path redraw when its answer comes.
    private val known = mutableStateMapOf<String, Entry>()
    private val waiting = LinkedHashSet<String>()
    private val asking = HashSet<String>()
    private var flushing: Job? = null

    private fun fresh(e: Entry?, at: Long) = e != null && at - e.at < lifetime(e.kind)

    /** Asks about the paths not known (or known too long ago), with whatever else rows ask for in the same moment. */
    fun ask(paths: List<String>) {
        val at = now()
        for (p in paths) if (p !in asking && !fresh(known[p], at)) waiting += p
        if (waiting.isNotEmpty() && flushing?.isActive != true) flushing = scope.launch {
            delay(WAIT_MS)
            val batch = waiting.toList()
            waiting.clear()
            batch.chunked(BATCH).forEach(::query)
        }
    }

    // On the presence's own scope, not the flush's: the flush ends once it has asked, so paths that rows ask for
    // while an answer is on its way get a flush of their own.
    private fun query(paths: List<String>) {
        asking += paths
        scope.launch {
            val got = try { stat(paths).stats.associate { it.path to it.kind } }
                catch (e: CancellationException) { asking -= paths.toSet(); throw e }
                catch (_: Exception) { null }
            val at = now()
            for (p in paths) { asking -= p; known[p] = Entry(got?.let { it[p] ?: "missing" }, at) }
        }
    }

    /** What is known of a path: file, dir, denied, missing; null while unknown or unanswered. */
    fun kind(path: String): String? = known[path]?.kind

    /** A path to link: there as a file or a folder, or in one macOS keeps from the node (opening it says what to do). */
    fun linkable(path: String): Boolean = kind(path).let { it == "file" || it == "dir" || it == "denied" }

    companion object {
        const val WAIT_MS = 60L
        const val BATCH = 200 // the node answers at most this many at a time

        fun lifetime(kind: String?): Long = when (kind) {
            "file", "dir", "denied" -> 30 * 60_000L
            "missing" -> 2 * 60_000L
            else -> 60_000L
        }
    }
}
