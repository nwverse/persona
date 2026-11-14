package persona.core.domain.sort

import persona.core.contracts.Interaction
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.parseTime

/** A child is hidden only while this list holds its parent on the same machine (web foldRows). */
fun foldRows(rows: List<AgentSession>): List<AgentSession> {
    val listed = rows.mapTo(HashSet()) { it.session.key }
    return rows.filterNot { row -> row.session.foldedUnder?.takeIf(String::isNotBlank)?.let {
        SessionKey(row.session.machineId, it) in listed
    } == true }.map { row ->
        if (row.session.agentState == row.state) row else row.copy(session = row.session.copy(state = row.state.wire))
    }
}

/** List and dock copies carry the folded state without changing the repository's own session rows. */
fun foldedSessions(rows: List<Session>): List<Session> = foldRows(rows.map(::AgentSession)).map { row ->
    row.session.let { if (it.agentState == row.state) it else it.copy(state = row.state.wire) }
}

/** Every depth is already assigned to its topmost ancestor by the hub. */
fun childSessions(rows: List<Session>, parent: SessionKey): List<Session> = rows
    .filter { it.machineId == parent.machineId && it.foldedUnder == parent.sessionId && it.key != parent }
    .sortedWith(compareBy<Session> { parseTime(it.startedAt ?: it.updatedAt) }.thenBy { it.sessionId })

/** Own requests take precedence in the parent's preview; a child's question stays answerable on that page. */
fun sessionCard(cards: List<Interaction>, parent: SessionKey): Interaction? =
    cards.firstOrNull { it.sessionKey == parent } ?: cards.firstOrNull { it.homeKey == parent }
