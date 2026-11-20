package persona.feature.chat

import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.EngineLeg
import persona.core.contracts.Item
import persona.core.contracts.ItemsResponse
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.domain.lines.EngineHistoryWindows

class EarlierEngineHistoryTest {
    private val claude = SessionKey("computer", "claude-thread")
    private val codex = SessionKey("computer", "codex-thread")
    private val windows = EngineHistoryWindows.of(claude, listOf(
        EngineLeg(claude.sessionId, "claude", time(3)),
        EngineLeg(codex.sessionId, "codex", time(6)),
    ))

    private fun time(second: Int) = "2026-11-20T10:00:0" + second + "Z"

    private fun item(id: String, seq: Int, second: Int?, words: String = id) =
        Item(id, seq, second?.let(::time), role = "user", kind = "text", text = words)

    @Test fun theReusedSessionSplitsByExclusiveCutoffAndKeepsUntimedRowsWithTheirPredecessor() {
        val first = item("same", 2, 2)
        val boundary = item("boundary", 3, 6)
        val untimed = item("untimed", 4, null)
        val current = item("latest", 7, 7)
        val currentTail = item("tail", 8, null)
        val snapshot = ChatSnapshot(listOf(first, boundary, untimed, current, currentTail), loading = false, hasOlder = true)
        val displayed = currentEngineSnapshot(snapshot, windows)
        assertEquals(listOf(current, currentTail), displayed.items)
        assertFalse("the newest run is complete at the cutoff even when this session has older pages", displayed.hasOlder)

        val split = EngineHistoryWindows.split(snapshot.items, windows, claude)
        assertEquals(listOf(first, boundary, untimed), split[0])
        assertEquals(listOf(current, currentTail), split[2])
        val earlier = item("one-time-session", 1, 1)
        assertEquals(listOf(earlier), EngineHistoryWindows.split(listOf(earlier), windows, codex)[1])
        assertTrue(EngineHistoryWindows.of(claude, listOf(EngineLeg(codex.sessionId, "codex", "invalid"))).isEmpty())
    }

    @Test fun missingTimestampsDoNotSkipUnloadedPages() {
        val known = item("newest", 5, 9)
        val untimed = item("unknown", 4, null)
        val snapshot = ChatSnapshot(listOf(untimed, known), loading = false, hasOlder = true)
        assertTrue(currentEngineSnapshot(snapshot, windows).hasOlder)
        assertEquals(snapshot.items, currentEngineSnapshot(snapshot, windows).items)
        val unverified = snapshot.copy(items = listOf(item("older", 1, 4), known), olderBoundaryKnown = false)
        assertTrue(currentEngineSnapshot(unverified, windows).hasOlder)
    }

    private abstract class HistoryRepo : ChatRepository {
        override fun observe(session: SessionKey): Nothing = error("Previous history does not open a live subscription")
        override suspend fun loadOlder(session: SessionKey): Unit = error("Earlier history has its own cursor")
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): Nothing = error("No live fold")
    }

    @Test fun loadsOnePageFromTheNewestIncompleteRunAndKeepsSessionIdentity() = runBlocking {
        val requested = mutableListOf<Pair<SessionKey, Int?>>()
        val repo = object : HistoryRepo() {
            override suspend fun earlierEnginePage(session: SessionKey, beforeSeq: Int?): ItemsResponse {
                requested += session to beforeSeq
                return when (session to beforeSeq) {
                    codex to null -> ItemsResponse(listOf(item("same", 7, 5, "later Codex run")), hasMore = true)
                    codex to 7 -> ItemsResponse(listOf(item("prior", 6, 4)), hasMore = false)
                    claude to 2 -> ItemsResponse(listOf(item("oldest", 1, 1)), hasMore = false)
                    else -> error("Wrong session or cursor")
                }
            }
        }
        val live = ChatSnapshot(listOf(item("same", 2, 2, "earlier Claude run"), item("new", 3, 9)),
            loading = false, hasOlder = true)
        val pager = EarlierEngineHistory(repo)
        fun plan() = pager.plan(windows, claude, live, ready = true)
        assertNull(pager.plan(windows, claude, live, ready = false).next)
        assertTrue(requested.isEmpty())
        assertEquals(codex, plan().next?.session)
        pager.load(requireNotNull(plan().next), claude, live)
        assertEquals(listOf(codex to null), requested)
        assertEquals(listOf(codex), plan().sections.map { it.window.session })
        pager.load(requireNotNull(plan().next), claude, live)
        assertEquals(claude, plan().next?.session)
        assertEquals(listOf(codex, claude), plan().sections.map { it.window.session })
        pager.load(requireNotNull(plan().next), claude, live)
        assertNull(plan().next)
        assertEquals(listOf(codex to null, codex to 7, claude to 2), requested)
        val rows = earlierEngineLines(plan(), emptySet(), emptyMap(), showModelChanges = false)
        assertEquals(listOf("same", "prior", "same", "oldest"), rows.map { it.line.id })
        assertEquals(rows.size, rows.map(EarlierEngineLine::key).toSet().size)
    }

    @Test fun failuresStayRetryableButExplicitlyMissingHistoryDoesNotLoop() = runBlocking {
        var attempts = 0
        val repo = object : HistoryRepo() {
            override suspend fun earlierEnginePage(session: SessionKey, beforeSeq: Int?): ItemsResponse? {
                if (++attempts == 1) error("Temporary connection failure")
                return null
            }
        }
        val live = ChatSnapshot(listOf(item("earlier", 1, 2), item("current", 2, 9)), loading = false)
        val pager = EarlierEngineHistory(repo)
        val first = requireNotNull(pager.plan(windows, claude, live, ready = true).next)
        pager.load(first, claude, live)
        assertTrue(pager.failed)
        assertEquals(first, pager.plan(windows, claude, live, ready = true).next)
        pager.load(first, claude, live)
        val plan = pager.plan(windows, claude, live, ready = true)
        assertTrue(plan.missing)
        assertFalse(pager.failed)
        assertNull(plan.next)
        assertEquals(listOf(claude), plan.sections.map { it.window.session })
    }
}
