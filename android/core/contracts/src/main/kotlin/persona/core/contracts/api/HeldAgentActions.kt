package persona.core.contracts.api

import kotlinx.coroutines.flow.StateFlow

const val AgentMessageHoldFeature = "agent_message_hold"

/** Actions on the manager that owns this Agent's session. Retry checks uncertain delivery at the manager. */
interface HeldAgentActions {
    val features: StateFlow<Set<String>>
    suspend fun retry(id: String)
    suspend fun cancel(id: String)
}
