package persona.core.contracts

import kotlinx.serialization.Serializable

/** A node-reported plan count; absent on older hubs and sessions without a task list. */
@Serializable
data class PlanInfo(
    val done: Int = 0,
    val total: Int = 0,
    val current: String = "",
    @WireTime val at: String? = null,
)
