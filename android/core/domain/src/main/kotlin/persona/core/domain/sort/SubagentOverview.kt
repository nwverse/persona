package persona.core.domain.sort

import persona.core.contracts.AgentState
import persona.core.contracts.FoldedAs
import persona.core.contracts.GroupKeys
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Subagent
import persona.core.contracts.parseTime
import java.time.Instant

/** Status of an entry in Session Overview's Subagents section. */
enum class OverviewSubagentStatus {
    NeedsYou,
    Running,
    Failed,
    Stopped,
    Ended,
    ;

    val isActive: Boolean get() = this == NeedsYou || this == Running
}

/**
 * One unified subagent row for Session Overview, aggregating embedded `session.subagents`, same-machine folded
 * children, and cross-machine A2A / dispatch subagents.
 */
data class OverviewSubagent(
    val id: String,
    val targetKey: SessionKey,
    val title: String?,
    val status: OverviewSubagentStatus,
    val startedAt: Instant?,
    val updatedAt: Instant?,
    /** Second-line live tool/activity summary; populated only for active (Running / NeedsYou) items. */
    val activityLine: String?,
    /** Progressive-disclosure origin chip: shown only when machine or harness differs from parent. */
    val originChip: String?,
    /** Nesting depth within the overview section, capped at 2 (deeper descendants flatten to 2). */
    val depth: Int,
    /** Source session for standalone/folded children; null for embedded `<parent>@<agent>` entries. */
    val session: Session?,
    /** Embedded subagent record when sourced from `parent.subagents`. */
    val embedded: Subagent?,
    /** Harness identity metadata for brand-mark resolution (`agent`, `launcher`, `agentLabelHint`, `agentSurface`, `agentLabel`). */
    val agent: String?,
    val launcher: String?,
    val agentLabelHint: String?,
    val agentSurface: String?,
    val agentLabel: String,
    val canSend: Boolean,
)

/** Grouped Session Overview subagents: active items (NeedsYou pinned first) and collapsed-by-default ended items. */
data class OverviewSubagents(
    val running: List<OverviewSubagent> = emptyList(),
    val ended: List<OverviewSubagent> = emptyList(),
) {
    val total: Int get() = running.size + ended.size
    val isEmpty: Boolean get() = running.isEmpty() && ended.isEmpty()
}

private const val MAX_NESTING_DEPTH = 2

/**
 * Aggregates embedded [parent.subagents], same-machine folded children, and cross-machine A2A children into
 * Session Overview's Running (NeedsYou pinned first, then newest start) and Ended groups.
 */
fun buildOverviewSubagents(
    parent: Session,
    childRows: List<Session>,
    machineNames: Map<String, String> = emptyMap(),
): OverviewSubagents {
    val parentHarness = harnessDisplayName(parent)
    val embeddedList = parent.subagents.orEmpty()
    val rawEntries = mutableListOf<RawEntry>()
    val seenKeys = LinkedHashSet<SessionKey>()

    for (sub in embeddedList) {
        val sid = sub.sessionId.takeIf(String::isNotBlank) ?: "${parent.sessionId}@${sub.id}"
        val target = SessionKey(parent.machineId, sid)
        if (!seenKeys.add(target)) continue
        val status = if (sub.running) OverviewSubagentStatus.Running else OverviewSubagentStatus.Ended
        val activity = if (status.isActive) {
            sub.lastTool?.summary?.takeIf(String::isNotBlank)
                ?: sub.lastTool?.name?.takeIf(String::isNotBlank)
        } else null
        rawEntries += RawEntry(
            id = "embedded:${sub.id}",
            linkId = sub.id,
            parentLinkId = sub.parentId?.takeIf { it.isNotBlank() && it != sub.id },
            targetKey = target,
            title = sub.description?.trim()?.takeIf(String::isNotEmpty) ?: sub.type.trim().takeIf(String::isNotEmpty),
            status = status,
            startedAt = parseTime(sub.startedAt),
            updatedAt = parseTime(sub.updatedAt) ?: parseTime(sub.startedAt),
            activityLine = activity,
            originChip = null,
            session = null,
            embedded = sub,
            agent = parent.agent,
            launcher = parent.launcher,
            agentLabelHint = parent.agentLabelHint,
            agentSurface = parent.agentSurface,
            agentLabel = parentHarness,
            canSend = false,
        )
    }

    val eligibleChildren = childRows.filter { row ->
        row.key != parent.key &&
            row.sideHome == null &&
            row.foldedAs != FoldedAs.WORKER &&
            (row.belongsToParent(parent.key) ||
                (!parent.sourceThreadId.isNullOrBlank() && row.machineId == parent.machineId && row.parentThreadId == parent.sourceThreadId))
    }

    for (child in eligibleChildren) {
        if (!seenKeys.add(child.key)) continue
        val status = childOverviewStatus(child)
        val childHarness = harnessDisplayName(child)
        val originChip = progressiveOriginChip(
            parentMachineId = parent.machineId,
            parentHarness = parentHarness,
            childMachineId = child.machineId,
            childHarness = childHarness,
            machineNames = machineNames,
        )
        val activity = if (status.isActive) {
            when (status) {
                OverviewSubagentStatus.NeedsYou -> child.attentionReason?.trim()?.takeIf(String::isNotEmpty)
                    ?: child.activity?.trim()?.takeIf(String::isNotEmpty)
                else -> child.activity?.trim()?.takeIf(String::isNotEmpty)
            }
        } else null
        val directParent = child.parentSessionId?.takeIf { it.isNotBlank() && it != parent.sessionId && it != child.sessionId }
        rawEntries += RawEntry(
            id = "session:${child.key.path}",
            linkId = child.sessionId,
            parentLinkId = directParent,
            targetKey = child.key,
            title = child.title.trim().takeIf { it.isNotEmpty() && it != "Claude Code" }
                ?: child.folderName.takeIf(String::isNotBlank),
            status = status,
            startedAt = parseTime(child.startedAt),
            updatedAt = parseTime(child.updatedAt) ?: parseTime(child.startedAt),
            activityLine = activity,
            originChip = originChip,
            session = child,
            embedded = null,
            agent = child.agent,
            launcher = child.launcher,
            agentLabelHint = child.agentLabelHint,
            agentSurface = child.agentSurface,
            agentLabel = childHarness,
            canSend = child.canSend,
        )
    }

    val byLinkId = rawEntries.associateBy { it.linkId }
    fun depthOf(entry: RawEntry): Int {
        var depth = 0
        var cursor = entry.parentLinkId
        val visited = HashSet<String>()
        visited += entry.linkId
        while (cursor != null && visited.add(cursor)) {
            val ancestor = byLinkId[cursor] ?: break
            depth++
            if (depth >= MAX_NESTING_DEPTH) return MAX_NESTING_DEPTH
            cursor = ancestor.parentLinkId
        }
        return depth.coerceAtMost(MAX_NESTING_DEPTH)
    }

    val all = rawEntries.map { entry ->
        OverviewSubagent(
            id = entry.id,
            targetKey = entry.targetKey,
            title = entry.title,
            status = entry.status,
            startedAt = entry.startedAt,
            updatedAt = entry.updatedAt,
            activityLine = entry.activityLine,
            originChip = entry.originChip,
            depth = depthOf(entry),
            session = entry.session,
            embedded = entry.embedded,
            agent = entry.agent,
            launcher = entry.launcher,
            agentLabelHint = entry.agentLabelHint,
            agentSurface = entry.agentSurface,
            agentLabel = entry.agentLabel,
            canSend = entry.canSend,
        )
    }

    val runningComparator = compareBy<OverviewSubagent>(
        { if (it.status == OverviewSubagentStatus.NeedsYou) 0 else 1 },
    ).thenByDescending { it.startedAt ?: it.updatedAt ?: Instant.EPOCH }
        .thenBy { it.targetKey.path }

    val endedComparator = compareByDescending<OverviewSubagent> { it.updatedAt ?: it.startedAt ?: Instant.EPOCH }
        .thenBy { it.targetKey.path }

    val (active, finished) = all.partition { it.status.isActive }
    return OverviewSubagents(
        running = orderWithNesting(active, byLinkId, runningComparator),
        ended = orderWithNesting(finished, byLinkId, endedComparator),
    )
}

private data class RawEntry(
    val id: String,
    val linkId: String,
    val parentLinkId: String?,
    val targetKey: SessionKey,
    val title: String?,
    val status: OverviewSubagentStatus,
    val startedAt: Instant?,
    val updatedAt: Instant?,
    val activityLine: String?,
    val originChip: String?,
    val session: Session?,
    val embedded: Subagent?,
    val agent: String?,
    val launcher: String?,
    val agentLabelHint: String?,
    val agentSurface: String?,
    val agentLabel: String,
    val canSend: Boolean,
)

private fun orderWithNesting(
    items: List<OverviewSubagent>,
    byLinkId: Map<String, RawEntry>,
    comparator: Comparator<OverviewSubagent>,
): List<OverviewSubagent> {
    if (items.size <= 1) return items
    val itemByLink = items.associateBy { item -> item.embedded?.id ?: item.session?.sessionId ?: item.targetKey.sessionId }
    val childrenByParent = HashMap<String, MutableList<OverviewSubagent>>()
    val roots = mutableListOf<OverviewSubagent>()
    for (item in items) {
        val linkId = item.embedded?.id ?: item.session?.sessionId ?: item.targetKey.sessionId
        val parentLink = byLinkId[linkId]?.parentLinkId
        if (parentLink != null && parentLink in itemByLink && parentLink != linkId) {
            childrenByParent.getOrPut(parentLink) { mutableListOf() }.add(item)
        } else {
            roots += item
        }
    }
    val out = ArrayList<OverviewSubagent>(items.size)
    val visited = HashSet<String>()
    fun emit(item: OverviewSubagent) {
        val linkId = item.embedded?.id ?: item.session?.sessionId ?: item.targetKey.sessionId
        if (!visited.add(linkId)) return
        out += item
        childrenByParent[linkId].orEmpty().sortedWith(comparator).forEach(::emit)
    }
    roots.sortedWith(comparator).forEach(::emit)
    // Defensive fallback in case of a parent cycle.
    items.sortedWith(comparator).forEach(::emit)
    return out
}

internal fun childOverviewStatus(row: Session): OverviewSubagentStatus = when {
    row.agentState == AgentState.NeedsYou -> OverviewSubagentStatus.NeedsYou
    row.agentState == AgentState.Working || row.backgroundWorking == true -> OverviewSubagentStatus.Running
    !row.turnError.isNullOrBlank() || !row.failureReason.isNullOrBlank() || !row.startError.isNullOrBlank() -> OverviewSubagentStatus.Failed
    row.status == "interrupted" -> OverviewSubagentStatus.Stopped
    else -> OverviewSubagentStatus.Ended
}

internal fun harnessDisplayName(session: Session): String = when {
    session.isClaude -> "Claude Code"
    else -> session.agentLabel
}

/**
 * Progressive-disclosure origin chip: shown only when machine or harness differs from parent,
 * displaying only the differing parts (`Machine · Harness` / `Machine` / `Harness`).
 */
internal fun progressiveOriginChip(
    parentMachineId: String,
    parentHarness: String,
    childMachineId: String,
    childHarness: String,
    machineNames: Map<String, String>,
): String? {
    val diffMachine = if (childMachineId.isNotBlank() && childMachineId != parentMachineId) {
        machineNames[childMachineId]?.takeIf(String::isNotBlank) ?: GroupKeys.raw(childMachineId)
    } else null
    val diffHarness = if (childHarness.isNotBlank() && !childHarness.equals(parentHarness, ignoreCase = true)) {
        childHarness
    } else null
    return listOfNotNull(diffMachine, diffHarness).joinToString(" · ").takeIf { it.isNotEmpty() }
}
