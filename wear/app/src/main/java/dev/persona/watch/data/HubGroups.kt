package dev.persona.watch.data

/** Every cached task retains its connection identity, even when server-side IDs collide. */
data class HubGroupSnapshot(val id: String, val name: String, val snapshot: WatchSnapshot?, val reachable: Boolean)
object HubGroups {
    fun merge(groups: List<HubGroupSnapshot>, now: Long): WatchSnapshot {
        require(groups.map { it.id }.distinct().size == groups.size)
        val sessions = groups.flatMap { group -> group.snapshot?.sessions.orEmpty().map { session ->
            session.copy(groupId = group.id, groupName = group.name,
                canSend = session.canSend && group.reachable, canInterrupt = session.canInterrupt && group.reachable,
                canApprove = session.canApprove && group.reachable, state = if (group.reachable) session.state else "offline")
        } }
        val interactions = groups.flatMap { group -> group.snapshot?.interactions.orEmpty().map { interaction ->
            interaction.copy(groupId = group.id, groupName = group.name,
                canAnswer = interaction.canAnswer && group.reachable, canCancel = interaction.canCancel && group.reachable)
        } }
        val primary = groups.firstOrNull { it.id.isEmpty() }
        return WatchSnapshot(sessions, interactions,
            groups.flatMap { group -> group.snapshot?.machines.orEmpty().map { it.copy(id = scopedKey(group.id, it.id), online = it.online && group.reachable) } },
            primary?.snapshot?.inbox.orEmpty(), now,
            primary?.reachable != true || primary.snapshot?.inboxUnavailable == true)
    }
}
