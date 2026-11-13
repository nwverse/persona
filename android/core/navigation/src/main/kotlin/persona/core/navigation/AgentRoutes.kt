package persona.core.navigation

import androidx.navigation3.runtime.NavKey
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The new agent page, optionally prefilled: [machineId] and [cwd] are picked when that machine is online (both can
 * still be changed), [prompt] is the first message to start from (the knowledge base's "Open Agent").
 */
@Serializable @SerialName("persona.core.navigation.NewAgentRoute") data class NewAgentRoute(
    val machineId: String? = null,
    val cwd: String? = null,
    val prompt: String? = null,
) : NavKey
