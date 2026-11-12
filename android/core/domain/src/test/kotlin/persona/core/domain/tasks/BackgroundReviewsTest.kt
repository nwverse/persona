package persona.core.domain.tasks

import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.AssistantConfig
import persona.core.domain.lines.ChatLine
import java.time.Instant

/** #5084: a review the background supervisor wrote is one line in the main conversation, not a record behind the trays. */
class BackgroundReviewsTest {
    private fun config(json: String) = JsonCodec.decodeFromString(AssistantConfig.serializer(), json)
    private fun review(id: String, todo: String, at: String, verdict: String = "accepted") =
        FeedItem(id, "main", at, "review", todo, buildJsonObject { put("title", "Fix the login"); put("verdict", verdict) })
    private fun todo(id: String, supervisor: String = "main") =
        Todo(id, "Fix the login", origin = "main", supervision = Supervision(supervisor = supervisor))
    private val reviews = BackgroundReviews("main", Instant.parse("2026-11-12T08:00:00Z"))
    private fun feedIds(lines: List<ChatLine>) =
        lines.filterIsInstance<ChatLine.Extra>().mapNotNull { (it.payload as? AssistantExtra.Feed)?.item?.id }

    @Test fun theHubSaysWhetherABackgroundSupervisorIsInEffect() {
        val on = config("""{"machine_id":"m","main":"main","main_supervises_own_tasks":false,
            "background_supervisor":"bg","background_supervisor_at":"2026-11-12T08:00:00Z"}""")
        assertEquals(reviews, BackgroundReviews.of(on))
        // the main conversation supervises its own tasks: its reviews are its own turns, as before
        assertNull(BackgroundReviews.of(on.copy(mainSupervisesOwnTasks = true)))
        // not made yet, no start time, no main conversation
        assertNull(BackgroundReviews.of(on.copy(backgroundSupervisor = null)))
        assertNull(BackgroundReviews.of(on.copy(backgroundSupervisorAt = null)))
        assertNull(BackgroundReviews.of(on.copy(main = null)))
    }

    @Test fun anOlderHubHasNoneOfIt() {
        val old = config("""{"machine_id":"m","launcher":"claude","cwd":"","main":"main"}""")
        assertNull(old.mainSupervisesOwnTasks)
        assertNull(old.backgroundSupervisor)
        assertNull(BackgroundReviews.of(old))
    }

    @Test fun onlyReviewsOfTheMainConversationWrittenSinceItStartedAreShown() {
        val feed = listOf(
            review("before", "t", "2026-11-12T07:59:59Z"),
            review("since", "t", "2026-11-12T08:00:00Z"),
            review("rework", "t", "2026-11-12T09:00:00Z", verdict = "rework"),
            review("leader", "led", "2026-11-12T09:30:00Z"),
            review("gone", "unknown", "2026-11-12T09:40:00Z"),
            review("undated", "t", ""),
            FeedItem("receipt", "main", "2026-11-12T09:50:00Z", "receipt", "t"),
            FeedItem("no-task", "main", "2026-11-12T09:55:00Z", "review"),
        )
        val todos = listOf(todo("t"), todo("led", supervisor = "leader-chat"))
        // a task whose ledger names a leader was reviewed by that leader; one the list no longer has is still shown
        assertEquals(setOf("since", "rework", "gone"), reviews.shown("main", feed, todos))
        assertEquals(emptySet<String>(), reviews.shown("side", feed, todos))
    }

    @Test fun aShownReviewIsALineOfTheConversationAndLeavesTheRecord() {
        val feed = listOf(review("old", "t", "2026-11-11T10:00:00Z"), review("new", "t", "2026-11-12T10:00:00Z"))
        val todos = listOf(todo("t"))
        val shown = reviews.shown("main", feed, todos)
        assertEquals(listOf("new"), feedIds(AssistantTaskCards.lines(emptyList(), feed, emptyMap(), todos, shown = shown)))
        assertEquals(listOf("old"), AssistantTaskCards.backgroundFeed(feed, shown).map { it.id })
        // without a background supervisor every review stays behind the trays, as before
        assertEquals(emptyList<String>(), feedIds(AssistantTaskCards.lines(emptyList(), feed, emptyMap(), todos)))
        assertEquals(listOf("old", "new"), AssistantTaskCards.backgroundFeed(feed).map { it.id })
    }

    @Test fun aShownReviewAlreadyAmongTheLinesStaysInTheForeground() {
        val item = review("new", "t", "2026-11-12T10:00:00Z")
        val lines = listOf<ChatLine>(ChatLine.Extra("feed-new", AssistantExtra.Feed(item)))
        assertEquals(lines, AssistantConversation.project(lines, emptyList(), shownFeed = setOf("new")).foreground)
        assertEquals(lines, AssistantConversation.project(lines, emptyList()).background)
    }
}
