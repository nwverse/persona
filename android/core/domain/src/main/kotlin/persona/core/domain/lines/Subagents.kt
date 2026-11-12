package persona.core.domain.lines

import persona.core.contracts.CoreText
import persona.core.contracts.CoreText.Key
import persona.core.contracts.Subagent
import java.time.Duration
import java.time.Instant

/** How a subagent's run stands on its card (iOS `SubagentCardStatus`). */
enum class SubagentStatus { Running, Done, Failed, Stopped }

/**
 * A subagent the agent launched, read from its Agent / Task call and its deliveries (docs/protocol/protocol.md, "Subagent
 * deliveries as tool results"; iOS `SubagentCardStatus`, `SubagentResult`, `SubagentRecent`). [agent] is the session's
 * listing of it (`subagents`, by the call's `tool_use_id`), null when the session does not list it.
 */
object Subagents {
    /** An Agent / Task call: drawn as its subagent's card. */
    fun isAgent(call: ToolCall): Boolean = call.name == "Agent" || call.name == "Task"

    /** The call's last word on its task from a delivery of its own (a `tool_result` carrying the agent task and what came after it); null before one. */
    fun delivered(call: ToolCall): String? {
        val first = call.results.indexOfFirst { it.kind == "tool_result" && it.task?.kind == "agent" }
        if (first < 0) return null
        return call.results.drop(first).lastOrNull { it.task?.kind == "agent" && it.task.status != "event" }?.task?.status
            ?: call.results[first].task?.status.orEmpty()
    }

    /** Sent to the background: its result is the launch receipt, how it went comes later. */
    fun backgrounded(call: ToolCall): Boolean = call.results.any { TaskIndex.isLaunchReceipt(it.text.orEmpty()) }

    /** Its deliveries' word first (an unknown one is not taken for an end), then an older hub's notices, then its result. */
    fun status(call: ToolCall, agent: Subagent?): SubagentStatus {
        delivered(call)?.let { return end(it) ?: SubagentStatus.Running }
        if (backgrounded(call)) {
            // An older hub's notices: the receipt is the result, its notice says how it went.
            val last = call.results.lastOrNull { it.kind == "notice" && it.task != null && it.task.status != "event" }?.task
                ?: return if (agent?.running == false) SubagentStatus.Stopped else SubagentStatus.Running
            return end(last.status) ?: SubagentStatus.Running
        }
        val result = call.result ?: return SubagentStatus.Running
        return if (result.item.isError == true) SubagentStatus.Failed else SubagentStatus.Done
    }

    private fun end(status: String?): SubagentStatus? = when (status) {
        "completed", "ended" -> SubagentStatus.Done
        "failed", "error" -> SubagentStatus.Failed
        "killed", "stopped" -> SubagentStatus.Stopped
        else -> null
    }

    /** What it reported: each delivery with words that is not its launch receipt, oldest first. */
    fun reports(call: ToolCall): List<LineItem> = call.results.filter { report ->
        val body = if (report.kind == "tool_result") report.text?.takeIf(String::isNotBlank) ?: report.task?.detail else report.task?.detail
        !body.isNullOrBlank() && !TaskIndex.isLaunchReceipt(body)
    }

    /**
     * Over and with what it reported: drawn as the tool's own row (iOS `SubagentResult.call`). While it runs, and when it
     * ended with nothing to show (a background one an older hub only gave a notice for), its card.
     */
    fun drawsAsRow(call: ToolCall, agent: Subagent?): Boolean {
        if (status(call, agent) == SubagentStatus.Running) return false
        if (reports(call).isNotEmpty()) return true
        if (backgrounded(call) && delivered(call) == null) return false
        return call.result != null
    }

    /** How long it ran, from its start to its result (a background one: to its last line); null while it runs. */
    fun took(call: ToolCall, agent: Subagent?): Long? {
        if (status(call, agent) == SubagentStatus.Running) return null
        val start = time(agent?.startedAt) ?: call.use.time ?: return null
        val end = (if (backgrounded(call)) time(agent?.updatedAt) else call.result?.time) ?: return null
        return Duration.between(start, end).seconds.takeIf { it > 0 }
    }

    /** "12 s", "4 min", "2 hr", "2 hr 5 min": how long a task ran, as its notice says it ([TaskIndex.note]). */
    fun ran(seconds: Long): CoreText = when {
        seconds < 60 -> CoreText.of(Key.DurationSeconds, seconds)
        seconds < 3600 -> CoreText.of(Key.DurationMinutes, seconds / 60)
        seconds % 3600 / 60 == 0L -> CoreText.of(Key.DurationHours, seconds / 3600)
        else -> CoreText.of(Key.DurationHoursMinutes, seconds / 3600, seconds % 3600 / 60)
    }

    /** What it is doing now while it runs: its latest call's words, else the tool's name; null before its first call. */
    fun doing(agent: Subagent?): String? = agent?.lastTool?.let { tool ->
        tool.summary?.trim()?.takeIf(String::isNotEmpty) ?: tool.name.trim().takeIf(String::isNotEmpty)
    }

    /** How many steps it took, when the hub says ([Subagent.steps]); null for none. */
    fun steps(agent: Subagent?): Int? = agent?.steps?.takeIf { it > 0 }

    /** How many of [calls] run, as their cards say. */
    fun running(calls: List<ToolCall>, agents: Map<String, Subagent>): Int =
        calls.count { status(it, it.use.item.toolUseId?.let(agents::get)) == SubagentStatus.Running }

    /** The steps [calls]' subagents took in all. */
    fun steps(calls: List<ToolCall>, agents: Map<String, Subagent>): Int =
        calls.sumOf { call -> call.use.item.toolUseId?.let(agents::get)?.steps ?: 0 }

    /** The latest calls an open card shows of its subagent's own conversation ([items]), oldest first, each with its result once in. */
    const val RecentCount = 3

    fun recent(items: List<LineItem>, count: Int = RecentCount): List<ToolCall> {
        val results = items.filter { it.kind == "tool_result" && it.item.toolUseId != null }.groupBy { it.item.toolUseId }
        return items.filter { it.kind == "tool_use" }.takeLast(count).map { use ->
            val delivered = use.item.toolUseId?.let(results::get).orEmpty()
            ToolCall(use, delivered.lastOrNull(), emptyList(), delivered)
        }
    }

    /** The most calls a fold may hold for the card to open it ([foldToOpen]). */
    const val FoldLimit = 40

    /**
     * The fold to open for [recent]: the hub folds the work it replays, and a subagent's run is one stretch of work. Its
     * newest fold when fewer than [RecentCount] calls follow it, and not a long one (it is fetched whole for three rows).
     */
    fun foldToOpen(items: List<LineItem>): LineItem? {
        val i = items.indexOfLast { it.kind == "fold" }
        if (i < 0) return null
        val tools = items[i].fold?.tools ?: return null
        if (tools !in 1..FoldLimit) return null
        return items[i].takeIf { items.drop(i + 1).count { it.kind == "tool_use" } < RecentCount }
    }

    private fun time(value: String?): Instant? = value?.let { runCatching { Instant.parse(it) }.getOrNull() }
}

/**
 * An open work line's subagents launched one after another share one line, their cards in a grid under "Running 3
 * agents" (iOS `ChatLines.gridAgents`, as the web); one alone stays a step of its own.
 */
fun gridAgents(lines: List<ChatLine>): List<ChatLine> {
    val out = ArrayList<ChatLine>(lines.size)
    val run = mutableListOf<ChatLine.Step>()
    fun flush() {
        if (run.size > 1) out += ChatLine.Agents(run[0].groupId, run.map { (it.step as WorkItem.Tool).call }, run[0].first)
        else out += run
        run.clear()
    }
    for (line in lines) {
        val agent = line is ChatLine.Step && (line.step as? WorkItem.Tool)?.call?.let(Subagents::isAgent) == true
        if (agent && (run.isEmpty() || run[0].groupId == (line as ChatLine.Step).groupId)) { run += line as ChatLine.Step; continue }
        flush()
        if (agent) run += line as ChatLine.Step else out += line
    }
    flush()
    return out
}
