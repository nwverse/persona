package persona.core.domain.lines

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.CoreText
import persona.core.contracts.CoreText.Key
import persona.core.contracts.Item
import persona.core.contracts.Subagent
import persona.core.contracts.WorkflowTool

/** A subagent's card reads as iOS's (`SubagentCardStatus`, `SubagentResult`, `SubagentRecent`, `ChatLines.gridAgents`). */
class SubagentsTest {
    private fun use(seq: Int, id: String, name: String = "Agent", at: String = "2026-11-12T10:00:00Z") = LineItem(Item("u$seq", seq = seq,
        kind = "tool_use", role = "assistant", toolName = name, toolUseId = id, ts = at,
        toolInput = Json.parseToJsonElement("""{"description":"Check $id","prompt":"Look at $id"}""")))
    private fun result(seq: Int, id: String, text: String? = "ok", status: String? = null, error: Boolean = false,
        at: String = "2026-11-12T10:01:20Z") = LineItem(Item("r$seq", seq = seq, kind = "tool_result", role = "user",
        toolUseId = id, text = text, isError = error, ts = at), task = status?.let { TaskNote("agent", "Check $id", it, toolUseId = id) })
    private fun notice(seq: Int, id: String, status: String, detail: String? = null) = LineItem(Item("n$seq", seq = seq,
        kind = "notice", text = "x"), task = TaskNote("agent", "Check $id", status, toolUseId = id, detail = detail))
    private fun call(use: LineItem, vararg results: LineItem) = ToolCall(use, results.lastOrNull(), emptyList(), results.toList())
    private val receipt = "Async agent launched successfully.\nagentId: a1"

    @Test fun aRunningForegroundCallIsACard() {
        val running = call(use(1, "a"))
        assertEquals(SubagentStatus.Running, Subagents.status(running, null))
        assertFalse(Subagents.drawsAsRow(running, null))
        assertNull(Subagents.took(running, null))
    }

    @Test fun aFinishedOneIsTheToolsRowAndAFailedOneSaysSo() {
        val done = call(use(1, "a"), result(2, "a"))
        assertEquals(SubagentStatus.Done, Subagents.status(done, null))
        assertTrue(Subagents.drawsAsRow(done, null))
        assertEquals(80L, Subagents.took(done, null))
        val failed = call(use(1, "b"), result(2, "b", "boom", error = true))
        assertEquals(SubagentStatus.Failed, Subagents.status(failed, null))
        assertTrue(Subagents.drawsAsRow(failed, null))
    }

    @Test fun deliveriesDecideAndAnUnknownWordIsNotAnEnd() {
        val launched = call(use(1, "a"), result(2, "a", receipt, "running"))
        assertEquals(SubagentStatus.Running, Subagents.status(launched, null))
        assertTrue(Subagents.reports(launched).isEmpty())
        val interim = call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", "Half way", "running"))
        assertEquals(SubagentStatus.Running, Subagents.status(interim, null))
        assertEquals(listOf("r3"), Subagents.reports(interim).map { it.id })
        assertEquals(SubagentStatus.Running, Subagents.status(call(use(1, "a"), result(2, "a", "?", "unknown")), null))
        // A status-only notification ends it; with no report the call's own row shows how it went.
        val ended = call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", null, "completed"))
        assertEquals(SubagentStatus.Done, Subagents.status(ended, null))
        assertTrue(Subagents.drawsAsRow(ended, null))
        assertEquals(SubagentStatus.Stopped, Subagents.status(call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", null, "killed")), null))
        assertEquals(SubagentStatus.Failed, Subagents.status(call(use(1, "a"), result(2, "a", receipt, "running"), result(3, "a", "x", "failed")), null))
    }

    @Test fun anOlderHubsBackgroundOneKeepsItsCardWithHowItWentAndHowLong() {
        val agent = Subagent("a1", "s@a1", "Explore", toolUseId = "a", running = false, background = true,
            startedAt = "2026-11-12T10:00:00Z", updatedAt = "2026-11-12T10:01:20Z", steps = 7)
        val waiting = call(use(1, "a"), result(2, "a", receipt))
        assertEquals(SubagentStatus.Stopped, Subagents.status(waiting, agent))
        assertEquals(SubagentStatus.Running, Subagents.status(waiting, agent.copy(running = true)))
        val done = call(use(1, "a"), result(2, "a", receipt), notice(3, "a", "completed"))
        assertEquals(SubagentStatus.Done, Subagents.status(done, agent))
        assertFalse(Subagents.drawsAsRow(done, agent))
        assertEquals(80L, Subagents.took(done, agent))
        // With a report in its notice, the report is the row's.
        val reported = call(use(1, "a"), result(2, "a", receipt), notice(3, "a", "failed", "It broke"))
        assertEquals(SubagentStatus.Failed, Subagents.status(reported, agent))
        assertTrue(Subagents.drawsAsRow(reported, agent))
    }

    @Test fun whatItDoesAndHowLongItRan() {
        val agent = Subagent("a1", toolUseId = "a", running = true, steps = 7, lastTool = WorkflowTool(name = "Bash", summary = " Running the tests "))
        assertEquals("Running the tests", Subagents.doing(agent))
        assertEquals("Bash", Subagents.doing(agent.copy(lastTool = WorkflowTool(name = "Bash"))))
        assertNull(Subagents.doing(agent.copy(lastTool = null)))
        assertEquals(7, Subagents.steps(agent))
        assertNull(Subagents.steps(agent.copy(steps = 0)))
        assertEquals(CoreText.of(Key.DurationSeconds, 42L), Subagents.ran(42))
        assertEquals(CoreText.of(Key.DurationMinutes, 1L), Subagents.ran(80))
        assertEquals(CoreText.of(Key.DurationHours, 2L), Subagents.ran(7200))
        assertEquals(CoreText.of(Key.DurationHoursMinutes, 2L, 5L), Subagents.ran(7500))
    }

    @Test fun aGridCountsItsRunningOnesAndTheirSteps() {
        val calls = listOf(call(use(1, "a")), call(use(2, "b"), result(3, "b")), call(use(4, "c")))
        val agents = mapOf("a" to Subagent("1", toolUseId = "a", steps = 4), "b" to Subagent("2", toolUseId = "b", steps = 8))
        assertEquals(2, Subagents.running(calls, agents))
        assertEquals(12, Subagents.steps(calls, agents))
    }

    @Test fun parallelSubagentsShareOneLineAndOneAloneStaysAStep() {
        val a = WorkItem.Tool(call(use(1, "a")))
        val b = WorkItem.Tool(call(use(2, "b", "Task")))
        val c = WorkItem.Tool(call(use(3, "c")))
        val read = WorkItem.Tool(call(use(4, "r", "Read")))
        val d = WorkItem.Tool(call(use(5, "d")))
        val lines = listOf(ChatLine.Work("w", listOf(a, b, c, read, d), true, null, null, true),
            ChatLine.Step("w", a, true), ChatLine.Step("w", b, false), ChatLine.Step("w", c, false),
            ChatLine.Step("w", read, false), ChatLine.Step("w", d, false), ChatLine.End("w", 5))
        val grid = gridAgents(lines)
        assertEquals(listOf("w", "w/agents-u1", "w/u4", "w/u5", "w-end"), grid.map { it.id })
        val agents = grid[1] as ChatLine.Agents
        assertEquals(listOf("u1", "u2", "u3"), agents.calls.map { it.id })
        assertTrue(agents.first)
        // Nothing to group: the same lines.
        val plain = listOf(ChatLine.Step("w", read, true), ChatLine.Step("w", a, false))
        assertEquals(plain, gridAgents(plain))
        // Two work lines' subagents never share a grid.
        val split = gridAgents(listOf(ChatLine.Step("w1", a, false), ChatLine.Step("w2", b, true)))
        assertEquals(listOf("w1/u1", "w2/u2"), split.map { it.id })
    }

    @Test fun anOpenCardShowsItsSubagentsLatestCallsAndOpensItsNewestShortFold() {
        val items = listOf(use(1, "x", "Read"), result(2, "x"), use(3, "y", "Grep"), use(4, "z", "Edit"), result(5, "z"), use(6, "w", "Bash"))
        val recent = Subagents.recent(items)
        assertEquals(listOf("u3", "u4", "u6"), recent.map { it.id })
        assertEquals(listOf(null, "r5", null), recent.map { it.result?.id })
        val fold = LineItem(Item("f1", seq = 1, kind = "fold"), fold = Fold(items = 6, tools = 3, thoughts = 0, throughSeq = 6, throughId = "6"))
        assertEquals("f1", Subagents.foldToOpen(listOf(fold, use(7, "v", "Read")))?.id)
        assertNull(Subagents.foldToOpen(listOf(fold, use(7, "v"), use(8, "u"), use(9, "t"))))
        assertNull(Subagents.foldToOpen(listOf(fold.copy(fold = fold.fold!!.copy(tools = 41)))))
        assertNull(Subagents.foldToOpen(listOf(use(1, "x"))))
    }
}
