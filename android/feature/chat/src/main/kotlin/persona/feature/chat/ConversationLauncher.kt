package persona.feature.chat

import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.ModelPick
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.JAction
import persona.core.designsystem.JActionSheet
import persona.core.designsystem.JChip
import persona.core.designsystem.LocalHubFeatures
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.operationFeedback
import persona.core.domain.models.PersonaModelPanel

/** A saved id names its canonical non-default row first, then a folded alias or the model that row resolves to. */
internal fun listedAgentModel(models: List<AgentModel>, id: String): AgentModel? {
    if (id.isEmpty()) return null
    val nonDefault = models.filterNot(AgentModel::isDefault)
    return nonDefault.firstOrNull { it.id.equals(id, ignoreCase = true) }
        ?: nonDefault.firstOrNull { persona.core.domain.models.ModelPanel.isCurrentModel(it.id, it.resolved, id, it.aliases) }
        ?: models.firstOrNull { it.id.equals(id, ignoreCase = true) }
        ?: models.firstOrNull { persona.core.domain.models.ModelPanel.isCurrentModel(it.id, it.resolved, id, it.aliases) }
}

/** An optimistic pick written from Brief/Board while waiting for the hub's authoritative session row. */
internal data class DrawnConversationPick(
    val pick: ModelPick?,
    val beforePick: ModelPick?,
    val beforeRev: Long?,
    val saving: Boolean = false,
)

/** Resolved state driving Brief/Board's launcher chip and its "Following messages use" sheet. */
internal data class ConversationLauncherState(
    val kept: Boolean,
    val activeLauncher: String?,
    val activeModel: String?,
    val defaultLauncher: String,
    val effectiveLauncher: String,
    val overridden: Boolean,
    val saving: Boolean,
)

/** What a sheet tap does when the hub persists per-conversation picks (`assistant_model_pick`). */
internal sealed interface ConversationLauncherAction {
    data object NoOp : ConversationLauncherAction
    data object ClearPick : ConversationLauncherAction
    data class SetPick(val pick: ModelPick) : ConversationLauncherAction
}

internal fun normalizeModelPick(pick: ModelPick?): ModelPick? {
    val launcher = pick?.launcher?.trim()?.takeIf(String::isNotEmpty) ?: return null
    return ModelPick(
        launcher = launcher,
        model = pick.model?.trim()?.takeIf(String::isNotEmpty),
        effort = pick.effort?.trim()?.takeIf(String::isNotEmpty),
    )
}

internal fun resolveConversationLauncherState(
    session: Session?,
    kept: Boolean,
    drawn: DrawnConversationPick?,
    followsDefault: Boolean,
    catalogDefaultLauncher: String?,
    configLauncher: String?,
    sessionLauncher: String?,
    pageLauncher: String?,
    pageModel: String?,
): ConversationLauncherState {
    val configured = catalogDefaultLauncher?.trim()?.takeIf(String::isNotEmpty)
        ?: configLauncher?.trim()?.takeIf(String::isNotEmpty)
    val own = sessionLauncher?.trim()?.takeIf(String::isNotEmpty)
    val defaultLauncher = (if (followsDefault) configured ?: own else own ?: configured) ?: "claude"
    if (kept && session != null) {
        val serverPick = normalizeModelPick(session.modelPick)
        val useDrawn = drawn != null &&
            (drawn.saving || (drawn.beforeRev == session.rev && drawn.beforePick == serverPick))
        val activePick = if (useDrawn) normalizeModelPick(drawn.pick) else serverPick
        val activeLauncher = activePick?.launcher
        val activeModel = activePick?.model
        return ConversationLauncherState(
            kept = true,
            activeLauncher = activeLauncher,
            activeModel = activeModel,
            defaultLauncher = defaultLauncher,
            effectiveLauncher = activeLauncher ?: defaultLauncher,
            overridden = activePick != null,
            saving = useDrawn && drawn.saving,
        )
    }
    val activeLauncher = pageLauncher?.trim()?.takeIf(String::isNotEmpty)
    val activeModel = pageModel?.trim()?.takeIf(String::isNotEmpty)
    return ConversationLauncherState(
        kept = false,
        activeLauncher = activeLauncher,
        activeModel = activeModel,
        defaultLauncher = defaultLauncher,
        effectiveLauncher = activeLauncher ?: defaultLauncher,
        overridden = activeLauncher != null || activeModel != null,
        saving = false,
    )
}

internal fun conversationLauncherChipModelText(
    state: ConversationLauncherState,
    models: List<AgentModel>,
    fallbackSelectedLabel: String,
): String? {
    val id = state.activeModel ?: return null
    listedAgentModel(models, id)?.label?.let { return it }
    return if (state.kept) id else fallbackSelectedLabel
}

internal fun planClearToDefault(state: ConversationLauncherState): ConversationLauncherAction =
    if (!state.overridden) ConversationLauncherAction.NoOp else ConversationLauncherAction.ClearPick

internal fun planSelectLauncher(state: ConversationLauncherState, launcherName: String): ConversationLauncherAction {
    val normalized = launcherName.trim()
    if (normalized.isEmpty()) return ConversationLauncherAction.NoOp
    if (state.activeLauncher == normalized && state.activeModel == null) return ConversationLauncherAction.NoOp
    return ConversationLauncherAction.SetPick(ModelPick(launcher = normalized, model = null))
}

internal fun planSelectDefaultModel(state: ConversationLauncherState): ConversationLauncherAction {
    if (state.activeModel == null) return ConversationLauncherAction.NoOp
    val launcher = state.activeLauncher
    return if (launcher == null || launcher == state.defaultLauncher) {
        ConversationLauncherAction.ClearPick
    } else {
        ConversationLauncherAction.SetPick(ModelPick(launcher = launcher, model = null))
    }
}

internal fun planSelectModel(
    state: ConversationLauncherState,
    model: AgentModel,
    matchedSelectableModel: AgentModel?,
): ConversationLauncherAction {
    if (matchedSelectableModel == model) return ConversationLauncherAction.NoOp
    return ConversationLauncherAction.SetPick(ModelPick(launcher = state.effectiveLauncher, model = model.id))
}

internal data class ConversationLauncherLabels(
    val launcherSection: String,
    val modelSection: String,
    val useDefault: String,
    val launchersLoading: String,
    val launchersFailed: String,
    val noLaunchers: String,
    val defaultModel: String,
    val modelsLoading: String,
    val modelsFailed: String,
    val modelsUnavailable: String,
)

internal fun buildConversationLauncherActions(
    state: ConversationLauncherState,
    launchers: List<AgentLauncher>?,
    launchersFailed: Boolean,
    currentModels: LauncherModels?,
    modelsLoading: Boolean,
    modelsFailed: Boolean,
    actionEnabled: Boolean,
    labels: ConversationLauncherLabels,
    onKeptAction: (ConversationLauncherAction) -> Unit,
    onLegacyPick: (launcher: String?, model: String?) -> Unit,
): List<JAction> {
    val selectableModels = currentModels?.models?.filterNot(AgentModel::isDefault).orEmpty()
    val matchedSelectableModel = state.activeModel?.let { listedAgentModel(selectableModels, it) }
    val showLockedLauncher = state.kept &&
        state.activeLauncher != null &&
        launchers.orEmpty().none { it.name == state.activeLauncher }
    val showLockedModel = state.kept &&
        state.activeModel != null &&
        matchedSelectableModel == null

    return buildList {
        add(JAction(labels.useDefault, section = labels.launcherSection, enabled = actionEnabled, checked = !state.overridden) {
            if (state.kept) onKeptAction(planClearToDefault(state)) else onLegacyPick(null, null)
        })
        if (showLockedLauncher) {
            add(JAction(state.activeLauncher!!, enabled = actionEnabled, checked = true) {})
        }
        when {
            launchers == null -> add(JAction(if (launchersFailed) labels.launchersFailed else labels.launchersLoading, enabled = false) {})
            launchers.isEmpty() -> add(JAction(labels.noLaunchers, enabled = false) {})
            else -> launchers.forEach { launcher ->
                val text = listOfNotNull(launcher.name, launcherModel(launcher)).joinToString(" · ")
                add(JAction(text, enabled = actionEnabled, checked = launcher.name == state.activeLauncher) {
                    if (state.kept) onKeptAction(planSelectLauncher(state, launcher.name))
                    else onLegacyPick(launcher.name, null)
                })
            }
        }
        add(JAction(labels.defaultModel, section = labels.modelSection, enabled = actionEnabled, checked = state.activeModel == null) {
            if (state.kept) onKeptAction(planSelectDefaultModel(state)) else onLegacyPick(state.activeLauncher, null)
        })
        if (showLockedModel) {
            add(JAction(state.activeModel!!, enabled = actionEnabled, checked = true) {})
        }
        when {
            currentModels != null -> selectableModels.forEach { model ->
                add(JAction(model.label, enabled = actionEnabled, checked = matchedSelectableModel == model) {
                    if (state.kept) onKeptAction(planSelectModel(state, model, matchedSelectableModel))
                    else onLegacyPick(state.activeLauncher, model.id)
                })
            }
            modelsLoading -> add(JAction(labels.modelsLoading, enabled = false) {})
            modelsFailed -> add(JAction(labels.modelsFailed, enabled = false) {})
            else -> add(JAction(labels.modelsUnavailable, enabled = false) {})
        }
    }
}

/**
 * An existing Persona conversation's launcher/model chip in Brief and Board. On a hub with `assistant_model_pick`
 * (and once the live [session] is present), reads `session.modelPick` and writes via `PUT`/`DELETE …/model`,
 * matching Glass's [PersonaModelPill]; on older hubs or unsynced chats, falls back to page-local [onPick].
 */
@Composable
internal fun ConversationLauncherChip(
    api: AssistantManagement,
    machineId: String,
    sessionId: String,
    sessionLauncher: String?,
    pickedLauncher: String?,
    pickedModel: String?,
    openRequested: Boolean = false,
    onOpenHandled: () -> Unit = {},
    session: Session? = null,
    enabled: Boolean = true,
    clearRequested: Boolean = false,
    onClearHandled: () -> Unit = {},
    leaderConversations: Set<String> = emptySet(),
    onOverlay: (Boolean) -> Unit = {},
    onNotice: (String) -> Unit = {},
    onSynced: () -> Unit = {},
    onPick: (launcher: String?, model: String?) -> Unit,
) {
    val features = LocalHubFeatures.current
    val kept = PersonaModelPanel.MODEL_PICK in features && session != null
    val scope = rememberCoroutineScope()
    val res = LocalResources.current
    val latestSession by rememberUpdatedState(session)

    var open by remember(machineId, sessionId) { mutableStateOf(false) }
    var config by remember(api) { mutableStateOf<AssistantConfig?>(null) }
    var catalogDefaultLauncher by remember(api, machineId, sessionId) { mutableStateOf<String?>(null) }
    var launchers by remember(api, machineId) { mutableStateOf<List<AgentLauncher>?>(null) }
    var launchersFailed by remember(api, machineId) { mutableStateOf(false) }
    var models by remember(api, machineId) { mutableStateOf<LauncherModels?>(null) }
    var modelsForLauncher by remember(api, machineId) { mutableStateOf<String?>(null) }
    var modelsLoading by remember(api, machineId) { mutableStateOf(false) }
    var modelsFailed by remember(api, machineId) { mutableStateOf(false) }
    var drawn by remember(session?.key) { mutableStateOf<DrawnConversationPick?>(null) }
    var saveToken by remember(session?.key) { mutableLongStateOf(0L) }

    LaunchedEffect(open) { onOverlay(open) }
    DisposableEffect(Unit) { onDispose { onOverlay(false) } }
    LaunchedEffect(openRequested) {
        if (openRequested) { open = true; onOpenHandled() }
    }

    fun keep(pick: ModelPick?) {
        val target = latestSession
        if (target == null || !kept) {
            onPick(pick?.launcher, pick?.model)
            return
        }
        if (drawn?.saving == true) return
        val before = drawn
        val targetKey = target.key
        val normalized = normalizeModelPick(pick)
        val token = ++saveToken
        drawn = DrawnConversationPick(
            pick = normalized,
            beforePick = normalizeModelPick(target.modelPick),
            beforeRev = target.rev,
            saving = true,
        )
        scope.launch {
            try {
                val persisted = if (normalized == null) {
                    api.clearModelPick(targetKey)
                    null
                } else {
                    normalizeModelPick(api.setModelPick(targetKey, normalized))
                }
                val current = latestSession
                if (current?.key == targetKey && saveToken == token) {
                    val serverNow = normalizeModelPick(current.modelPick)
                    drawn = if (serverNow == persisted) null else DrawnConversationPick(
                        pick = persisted,
                        beforePick = serverNow,
                        beforeRev = current.rev,
                        saving = false,
                    )
                }
                onSynced()
            } catch (cancelled: CancellationException) {
                if (latestSession?.key == targetKey && saveToken == token) {
                    drawn = before?.copy(saving = false)
                }
                throw cancelled
            } catch (error: Exception) {
                if (latestSession?.key == targetKey && saveToken == token) {
                    drawn = before?.copy(saving = false)
                    onNotice(operationFeedback(error).text.resolve(res))
                }
            }
        }
    }

    LaunchedEffect(clearRequested) {
        if (clearRequested) {
            open = false
            keep(null)
            onClearHandled()
        }
    }

    val main = config?.main == sessionId
    val followsDefault = main ||
        config?.backgroundSupervisor == sessionId ||
        sessionId in config?.leaders.orEmpty() ||
        sessionId in leaderConversations ||
        session?.origin == "task"
    val state = resolveConversationLauncherState(
        session = session,
        kept = kept,
        drawn = drawn,
        followsDefault = followsDefault,
        catalogDefaultLauncher = catalogDefaultLauncher,
        configLauncher = config?.launcher,
        sessionLauncher = sessionLauncher,
        pageLauncher = pickedLauncher,
        pageModel = pickedModel,
    )

    LaunchedEffect(open, api, machineId, sessionId) {
        val loadedConfig = try {
            api.config().also { config = it }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            config
        }
        if (!open) return@LaunchedEffect
        val isMainNow = loadedConfig?.main == sessionId
        launchers = null
        launchersFailed = false
        try {
            val catalog = if (isMainNow) api.mainAssistantLauncherList(machineId) else api.assistantLauncherList(machineId)
            launchers = catalog.launchers
            catalogDefaultLauncher = catalog.default?.provider?.takeIf(String::isNotBlank)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            launchersFailed = true
        }
    }

    val needModels = open || state.activeModel != null
    LaunchedEffect(open, state.activeModel != null, api, machineId, state.effectiveLauncher) {
        if (!needModels) return@LaunchedEffect
        val targetLauncher = state.effectiveLauncher
        val sameLauncher = modelsForLauncher == targetLauncher
        if (!open && sameLauncher && !modelsFailed) return@LaunchedEffect
        if (!sameLauncher) {
            models = null
            modelsForLauncher = null
        }
        modelsLoading = true
        modelsFailed = false
        try {
            models = api.launcherModels(machineId, targetLauncher)
            modelsForLauncher = targetLauncher
        } catch (cancelled: CancellationException) {
            modelsLoading = false
            throw cancelled
        } catch (_: Exception) {
            if (!sameLauncher) models = null
            modelsFailed = true
        }
        modelsLoading = false
    }

    // Only the main conversation draws an unpicked chip (#6911); a non-main conversation also surfaces the chip when
    // it has an active server/local pick or when opened by a model-route remedy. All state stays declared unconditionally.
    if (!main && !open && !state.overridden) return

    val currentModels = models.takeIf { modelsForLauncher == state.effectiveLauncher }
    val defaultLabel = stringResource(R.string.chat_conversation_launcher_default_named, state.effectiveLauncher)
    val pickedModelLabel = conversationLauncherChipModelText(
        state = state,
        models = currentModels?.models.orEmpty(),
        fallbackSelectedLabel = stringResource(R.string.chat_conversation_model_selected),
    )
    val label = if (state.overridden) listOfNotNull(state.effectiveLauncher, pickedModelLabel).joinToString(" · ") else defaultLabel
    JChip(
        label,
        onClick = { if (enabled && !state.saving) open = true },
        icon = PersonaIcons.model,
        selected = state.overridden,
        muted = !enabled || state.saving,
    )
    if (!open) return

    val labels = ConversationLauncherLabels(
        launcherSection = stringResource(R.string.chat_conversation_launcher_section),
        modelSection = stringResource(R.string.chat_conversation_model_section),
        useDefault = stringResource(R.string.chat_conversation_launcher_default),
        launchersLoading = stringResource(R.string.chat_conversation_launchers_loading),
        launchersFailed = stringResource(R.string.chat_conversation_launchers_failed),
        noLaunchers = stringResource(R.string.chat_conversation_launchers_none),
        defaultModel = stringResource(R.string.chat_conversation_model_default),
        modelsLoading = stringResource(R.string.chat_conversation_models_loading),
        modelsFailed = stringResource(R.string.chat_conversation_models_failed),
        modelsUnavailable = stringResource(R.string.chat_conversation_models_unavailable),
    )

    val actions = buildConversationLauncherActions(
        state = state,
        launchers = launchers,
        launchersFailed = launchersFailed,
        currentModels = currentModels,
        modelsLoading = modelsLoading,
        modelsFailed = modelsFailed,
        actionEnabled = enabled && !state.saving,
        labels = labels,
        onKeptAction = { action ->
            when (action) {
                ConversationLauncherAction.NoOp -> Unit
                ConversationLauncherAction.ClearPick -> keep(null)
                is ConversationLauncherAction.SetPick -> keep(action.pick)
            }
        },
        onLegacyPick = onPick,
    )
    JActionSheet(actions, onDismiss = { open = false }, title = stringResource(R.string.chat_conversation_launcher_title),
        note = stringResource(R.string.chat_conversation_launcher_note))
}
