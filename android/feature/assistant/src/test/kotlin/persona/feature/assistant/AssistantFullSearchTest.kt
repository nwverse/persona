package persona.feature.assistant

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Session
import persona.core.contracts.api.AssistantSearchAnswer
import persona.core.contracts.api.AssistantSearchHit
import persona.core.contracts.api.AssistantSearchResult

/** Filtering, opening and dismissing for the regular-window full search (#9171). */
class AssistantFullSearchTest {
    private val words = ConversationWords("Chat", "Hand to Persona", "Hand to Persona: %1\$s", "Background supervisor")
    private val newer = Session(machineId = "hub", sessionId = "new", title = "Weekend trip",
        lastText = "Pack the bags", updatedAt = "2026-11-16T12:00:00Z", assistant = true)
    private val older = Session(machineId = "hub", sessionId = "old", title = "Weekly report",
        lastText = "Draft ready", updatedAt = "2026-11-15T12:00:00Z", assistant = true)
    private val named = Session(machineId = "hub", sessionId = "lead", title = "Lead chat",
        updatedAt = "2026-11-14T12:00:00Z", assistant = true)

    @Test fun regularWindowOpensFullSearchAndCompactStaysInline() {
        assertTrue(sidebarOpensFullSearch(true))
        assertFalse(sidebarOpensFullSearch(false))
        assertEquals(FullSearchGate(open = true), FullSearchGate().onSearch(regularWindow = true))
        assertEquals(FullSearchGate(open = false), FullSearchGate().onSearch(regularWindow = false))
        assertFalse(FullSearchGate(open = true).dismiss().open)
        assertFalse(FullSearchGate(open = true).afterPick().open)
    }

    @Test fun blankQueryListsChatsLatestFirst() {
        val rows = fullSearchRows("", listOf(older, newer), listOf(older, newer), enabled = false, answer = null, words)
        assertEquals(listOf("new", "old"), rows.map { it.session.sessionId })
        assertTrue(rows.all { it.hit == null })
    }

    @Test fun localFilterMatchesTitleLastMessageAndDirectionName() {
        val byTitle = filterChatsForFullSearch(listOf(newer, older), "weekend", words)
        assertEquals(listOf("new"), byTitle.map { it.sessionId })
        val byLast = filterChatsForFullSearch(listOf(newer, older), "draft", words)
        assertEquals(listOf("old"), byLast.map { it.sessionId })
        val byName = filterChatsForFullSearch(listOf(named, newer), "Growth", words, names = mapOf("lead" to "Growth"))
        assertEquals(listOf("lead"), byName.map { it.sessionId })
    }

    @Test fun managerAnswerMergesAheadOfLocalMatches() {
        val hit = AssistantSearchHit(itemId = "m1", snippet = "said weekend", ranges = listOf(listOf(5, 12)))
        val archived = Session(machineId = "hub", sessionId = "arch", title = "Old trip", archived = true,
            updatedAt = "2026-10-01T00:00:00Z", assistant = true)
        val rows = fullSearchRows(
            "weekend", listOf(newer, older), listOf(newer, older, archived), enabled = true,
            answer = AssistantSearchAnswer(results = listOf(
                AssistantSearchResult(sessionId = "arch", machineId = "hub", title = "Old trip", archived = true,
                    hits = listOf(hit)),
                AssistantSearchResult(sessionId = "new", machineId = "hub", hits = listOf(hit)),
            )),
            words,
        )
        // Manager hits first (including an archived chat the local filter never listed), then any remaining local match.
        assertEquals(listOf("arch", "new"), rows.map { it.session.sessionId })
        assertEquals("m1", rows[0].hit?.itemId)
        assertEquals("m1", rows[1].hit?.itemId)
        assertFalse(rows.any { it.session.sessionId == "old" })
    }

    @Test fun featureOffKeepsLocalFilterOnly() {
        val rows = fullSearchRows(
            "weekend", listOf(newer, older), listOf(newer, older), enabled = false,
            answer = AssistantSearchAnswer(results = listOf(
                AssistantSearchResult(sessionId = "missing", machineId = "hub", hits = listOf(
                    AssistantSearchHit(itemId = "x", snippet = "x", ranges = emptyList()),
                )),
            )),
            words,
        )
        assertEquals(listOf("new"), rows.map { it.session.sessionId })
        assertTrue(rows.all { it.hit == null })
    }
}
