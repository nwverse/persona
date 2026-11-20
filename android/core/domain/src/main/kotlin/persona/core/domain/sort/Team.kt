package persona.core.domain.sort

import persona.core.contracts.AgentState
import persona.core.contracts.FoldedAs
import persona.core.contracts.Interaction
import persona.core.contracts.Session
import persona.core.contracts.SessionKey

/** Only the manager declares a worker. In a shell-first tab every Agent remains a peer. */
val Session.isTeamWorker: Boolean get() = foldedAs == FoldedAs.WORKER && !foldedUnder.isNullOrBlank()
val Interaction.fromWorker: Boolean get() = foldedAs == FoldedAs.WORKER && !foldedUnder.isNullOrBlank()

/** A card is counted under its home, but a link may ask to show the side or worker that actually owns it. */
val Interaction.memberTarget: SessionKey? get() = sideHome ?: sessionKey.takeIf { fromWorker }

fun teamWorkers(rows: List<Session>, lead: SessionKey): List<Session> = rows.filter {
    it.isTeamWorker && it.foldedUnder == lead.sessionId && it.machineId == lead.machineId && it.key != lead &&
        it.foldedSide.isNullOrBlank() && it.agentState != AgentState.Offline
}.distinctBy { it.key }.sortedWith(PaneOrder)

fun shownWorker(workers: List<Session>, asked: String?, last: String?): Session? =
    workers.firstOrNull { it.sessionId == asked } ?: workers.firstOrNull { it.sessionId == last }
        ?: workers.firstOrNull { it.agentState == AgentState.NeedsYou } ?: workers.firstOrNull()
