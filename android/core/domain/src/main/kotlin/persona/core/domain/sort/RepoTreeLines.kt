package persona.core.domain.sort

/**
 * What a working tree's line in By Repository says beside its name (iOS `RepoWorktreeLine` with `treeDetail` and its
 * tally, #4338 A7): the Primary chip on the main checkout, small after the name what the name does not say (the branch,
 * or a detached HEAD as a caution), how many agents at the end (Idle when nothing goes on), and dimmed while its
 * computer is offline.
 *
 * iOS reads the branch, the detached HEAD and the offline state from its host's workspace catalog; Android has no
 * catalog (phase 3 F7) and reads them from where the node placed the sessions in git (#1805) and from the computer.
 */
data class WorktreeLine(
    /** The main checkout: ⌂ and the Primary chip. */
    val main: Boolean,
    /** The branch checked out there, when its name is not that branch (a tree its host named, #4497). */
    val branch: String? = null,
    /** In git with no branch checked out: "Detached HEAD", in the caution color. Never beside [branch]. */
    val detached: Boolean = false,
    /** Idle or how many agents; null for none of its own while something goes on under it (iOS leaves it out). */
    val tally: WorktreeTally? = null,
    /** Its computer is out of reach: the name in the secondary color. */
    val offline: Boolean = false,
)

/** The end of a working tree's line: "Idle" while nothing goes on in it, else "N agents". */
sealed interface WorktreeTally {
    data object Idle : WorktreeTally
    data class Agents(val count: Int) : WorktreeTally
}

/**
 * The line of [tree] (iOS `treeDetail`, `RepoWorktreeLine.tally`): [quiet] when nothing goes on in it (every agent
 * idle, nothing pinned or in the background), [agents] how many of its agents the list shows, [online] its computer.
 */
fun worktreeLine(tree: AgentWorktree, quiet: Boolean, agents: Int, online: Boolean): WorktreeLine = WorktreeLine(
    main = tree.main,
    branch = tree.branch?.takeIf { it != tree.name },
    detached = tree.branch == null && tree.detached,
    tally = when {
        quiet -> WorktreeTally.Idle
        agents > 0 -> WorktreeTally.Agents(agents)
        else -> null
    },
    offline = !online,
)
