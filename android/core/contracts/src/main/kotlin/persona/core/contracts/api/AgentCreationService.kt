package persona.core.contracts.api

import persona.core.contracts.CoreText
import persona.core.contracts.Session
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Bound to one hub and pairing generation; never reuse it after pairing changes. */
interface AgentCreationService {
    val scope: String
    suspend fun launchers(machineId: String): List<AgentLauncher>
    suspend fun directories(machineId: String, path: String = ""): AgentDirectories
    suspend fun start(request: AgentStartRequest, operationId: String): AgentStartResult
    suspend fun operation(operationId: String): AgentStartResult

    /** The receipt of a start on [machineId]: a service over several hubs asks the hub that owns the machine. */
    suspend fun operation(operationId: String, machineId: String): AgentStartResult = operation(operationId)

    /**
     * The hub's "New Agent" model use (`agent.new`, docs/assistant/model-config.md): when it has one, a new agent follows it unless
     * a launcher is picked for this start (the request then carries an empty launcher). Null from hubs without
     * unified model configuration.
     */
    suspend fun newAgentModel(): ModelPurposeDetail? = null

    /**
     * The models [launcher]'s agent offers in its own /model menu, for "New Agent"'s model picker: the node keeps the
     * list a while, [refresh] asks the CLI again. Null from hubs without the list (404).
     */
    suspend fun launcherModels(machineId: String, launcher: String, refresh: Boolean = false): LauncherModels? = null
}

/** What a launcher's agent lists in its /model menu (`GET …/launchers/{name}/models`). No [models]: it has no list. */
@Serializable
data class LauncherModels(
    /** What the launcher runs: claude, codex, traex, a runtime's. */
    val agent: String = "",
    val models: List<AgentModel> = emptyList(),
    /** Why a model picked here would not reach the agent (the launcher passes its own --model); null: it would. */
    val locked: String? = null,
)

@Serializable
data class AgentModel(
    /** What a start sends as `model`. */
    val id: String,
    val name: String = "",
    val description: String? = null,
    /** The concrete model an alias stands for with this launcher's settings (opus → its model id). */
    val resolved: String? = null,
    /** What the agent runs when it is given no model: picking it is the same as picking none. */
    @SerialName("default") val isDefault: Boolean = false,
    /** The thinking efforts it takes, weakest first; empty: no effort setting. */
    val efforts: List<String> = emptyList(),
    @SerialName("default_effort") val defaultEffort: String? = null,
) {
    val label: String get() = modelName(id, name)
}

private val gptModelId = Regex("^gpt-([a-z0-9]+(?:\\.[a-z0-9]+)*(?:-[a-z0-9]+(?:\\.[a-z0-9]+)*)*)$", RegexOption.IGNORE_CASE)

/** Display only: explicit names win, and GPT versions and variants retain every component of their id. */
fun modelName(id: String?, name: String? = null): String {
    val raw = id.orEmpty()
    if (!name.isNullOrBlank() && !name.equals(raw, ignoreCase = true)) return name
    val label = name?.takeIf { it.isNotBlank() } ?: raw
    val model = gptModelId.matchEntire(label)?.groupValues?.get(1) ?: return label
    return model.split('-').mapIndexed { index, part ->
        if (index == 0) part else part.replaceFirstChar { it.titlecase(java.util.Locale.ROOT) }
    }.joinToString(" ")
}

@Serializable
data class AgentLauncher(
    val name: String,
    @SerialName("base_url") val baseUrl: String? = null,
    val model: String? = null,
    @SerialName("agent_type") val agentType: String = "claude",
    /** Its health on this computer (docs/server/launcher-health.md); null when never checked or from an older hub. */
    val health: LauncherHealth? = null,
)

/**
 * A launcher's health on one computer as the hub records it (docs/server/launcher-health.md): [state] working, failing,
 * repairing or blocked; [kind] the failure's class (auth, model_route, cli_outdated, timeout, not_in_terminal…);
 * [error] the hub's redacted text.
 */
@Serializable
data class LauncherHealth(
    val state: String = "",
    val kind: String? = null,
    val error: String? = null,
    @SerialName("checked_at") val checkedAt: String? = null,
    @SerialName("resets_at") val resetsAt: String? = null,
)

@Serializable
data class AgentDirectory(val name: String, val path: String, @SerialName("is_git") val isGit: Boolean = false)

@Serializable
data class AgentRecentDirectory(val path: String, val name: String = "", @SerialName("last_used") val lastUsed: String? = null)

@Serializable
data class AgentDirectories(
    val path: String,
    val parent: String = "",
    val entries: List<AgentDirectory> = emptyList(),
    val recent: List<AgentRecentDirectory> = emptyList(),
)

@Serializable
data class AgentStartRequest(
    @SerialName("machine_id") val machineId: String,
    val cwd: String,
    /** Empty: the hub picks the launcher from the "New Agent" model configuration. */
    val launcher: String,
    val prompt: String? = null,
    @SerialName("permission_mode") val permissionMode: String? = null,
    val effort: String? = null,
    /** Opens a new tab in this herdr workspace instead of a new space. */
    @SerialName("workspace_id") val workspaceId: String? = null,
    @SerialName("agent_type") val agentType: String? = null,
    @SerialName("resume_session_id") val resumeSessionId: String? = null,
    /** The hub answers at once with a `starting` session and starts the agent in the background. */
    val async: Boolean? = null,
    /** What the tab is named after when the first message goes as an ordinary message rather than [prompt]. */
    @SerialName("name_hint") val nameHint: String? = null,
    /** The model picked for this start (an id of [AgentCreationService.launcherModels]); null: the default one. */
    val model: String? = null,
    @SerialName("todo_id") val todoId: String? = null,
)

sealed interface AgentStartResult {
    data class Created(val session: Session) : AgentStartResult
    data object Pending : AgentStartResult
    data object Unknown : AgentStartResult
    /** Only an authoritative failed receipt permits editing and a new operation. */
    data class Rejected(val message: CoreText) : AgentStartResult
}
