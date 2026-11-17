package persona.android.navigation

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.navigation3.runtime.NavKey
import persona.core.navigation.AssistantRoute

/**
 * One back stack per sidebar root. Each visited root stays the top of its own stack so Navigation 3
 * keeps composing it (a single stack only composes the top entry, and removing an entry clears its
 * saveable state). The shell places [current] and leaves the others unplaced.
 *
 * The conversation list is the one `rememberNavBackStack` owns. Place lists are not saved: after
 * process death only the conversation stack comes back, and the shell adopts that visible root
 * (#9667). iOS keeps each root's path and does not animate a switch (`SidebarPageRoots.select`,
 * SidebarPageRoots.swift:88–97; `UnifiedSidebar.apply`, UnifiedSidebar.swift:67–74).
 */
internal class SidebarRootHosts(val conversation: MutableList<NavKey>) {
    private val places = mutableMapOf<String, SnapshotStateList<NavKey>>()

    /** Host ids that have been shown at regular width, conversation first. */
    val visited = mutableStateListOf(CONVERSATION)

    var current: MutableList<NavKey> by mutableStateOf(conversation)
        private set

    fun stack(id: String): MutableList<NavKey>? = if (id == CONVERSATION) conversation else places[id]

    fun allStacks(): List<MutableList<NavKey>> = listOf(conversation) + places.values

    /** Show [root] with [next] (the root plus its drill-downs). Does not remove any other host. */
    fun activate(root: NavKey, next: List<NavKey>) {
        if (next.isEmpty()) return
        if (sidebarHostId(root) == CONVERSATION) {
            replaceBackStack(conversation, next)
            current = conversation
            if (CONVERSATION !in visited) visited.add(0, CONVERSATION)
            return
        }
        val id = sidebarHostId(root)
        val list = places.getOrPut(id) { mutableStateListOf(root) }
        replaceBackStack(list, next)
        if (id !in visited) visited.add(id)
        current = list
    }

    /**
     * Folding puts the visible place back on the conversation stack (Back still reaches it) and
     * drops place hosts from [visited] first, so a place key is not composed twice.
     */
    fun foldOntoConversation() {
        if (current !== conversation) {
            val visible = current.toList()
            val prefixed = if (visible.firstOrNull() == AssistantRoute) visible else listOf(AssistantRoute) + visible
            visited.clear()
            visited.add(CONVERSATION)
            replaceBackStack(conversation, prefixed)
            current = conversation
        } else {
            compactPrefix(conversation.toList())?.let { replaceBackStack(conversation, it) }
        }
    }

    /** Another assistant: drop every place host. The caller resets the conversation stack. */
    fun forgetPlaces() {
        places.clear()
        visited.clear()
        visited.add(CONVERSATION)
        current = conversation
    }

    companion object {
        const val CONVERSATION = "conversation"
    }
}
