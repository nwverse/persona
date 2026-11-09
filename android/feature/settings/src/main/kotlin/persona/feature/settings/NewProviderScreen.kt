package persona.feature.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import android.content.res.Resources
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import persona.core.contracts.api.AssistantReadinessSource
import persona.core.contracts.api.ProviderSetup
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/** Debug builds open "New model provider" directly for captures (`debug.newProvider`: manual | preset); release never sets it. */
object NewProviderPreview {
    @Volatile var open: String? = null

    /** Taken once: the page it opens is shown, then the setting is gone. */
    fun take(): String? = open.also { open = null }
}

/**
 * "New model provider". On a hub with built-in presets (docs/assistant/onboarding.md 4.3 "Three paths" B) it is a wizard: (1) pick a provider (the
 * hub's providers, then "Custom compatible gateway"), (2) paste the key (tested by itself; "Use it for the main conversation and new Agents" follows the main
 * conversation's readiness; the name and another model under "More options") and (3) what now runs on it. A gateway typed in is
 * the form, with "Test connection" and "Fetch models". An older hub opens on the form: "Fill in" - the name, the kind, the gateway and
 * key, the models and flags - or "From extension": a preset, then the name, the key and maybe another of its models.
 * [onSaved] gets the new provider's "Model configuration" id.
 */
@Composable
internal fun NewProviderPage(
    providers: ProviderSetup, onBack: () -> Unit, onSaved: (String) -> Unit, preferPreset: Boolean = false,
    readiness: AssistantReadinessSource? = null,
    manager: String? = null,
) {
    val model = remember(providers, readiness) {
        NewProviderModel(providers, readiness?.let { source -> suspend { if (source.available.first()) source.readiness(false) else null } })
    }
    val state by model.state.collectAsState()
    val scope = rememberCoroutineScope()
    val feedback = LocalAppFeedback.current
    val res = LocalResources.current
    var choosingModel by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    LaunchedEffect(model) {
        val hubNames = providers.builtinPresets.first()
        if (hubNames) launch { model.loadReadiness() }
        model.load(preferPreset, hubNames)
    }
    val draft = state.draft
    // ②'s key is tested once it stops changing.
    LaunchedEffect(model, state.step, draft.apiKey, draft.presetModel, draft.preset) {
        if (state.step == WizardStep.Key && testableKey(draft.apiKey)) { delay(AutoTestMillis); model.testKey() }
    }
    val finish = { state.added?.let { onSaved(providerId(it.result.name)) } ?: onBack() }
    BackHandler {
        when {
            state.step == WizardStep.Done -> finish()
            state.hubNames && state.step != WizardStep.Pick -> model.backToPick()
            else -> onBack()
        }
    }
    val editable = !state.busy
    val preset = state.selected
    val title = when (state.step) {
        WizardStep.Key -> preset?.title ?: stringResource(R.string.settings_src_provider_new)
        WizardStep.Done -> stringResource(R.string.settings_onb_step_done)
        else -> stringResource(R.string.settings_src_provider_new)
    }
    val pageBack: () -> Unit = when {
        state.step == WizardStep.Done -> ({ finish() })
        state.hubNames && state.step != WizardStep.Pick -> model::backToPick
        else -> onBack
    }
    JListPage(title = title, onBack = pageBack, largeTitle = false, bottomBar = {
        when (state.step) {
            WizardStep.Pick -> {}
            WizardStep.Done -> Row(Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s)) {
                JButton(stringResource(R.string.settings_onb_finish), { finish() }, Modifier.fillMaxWidth())
            }
            WizardStep.Key -> Row(Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s)) {
                JButton(stringResource(R.string.settings_src_save), { scope.launch { model.saveKey() } },
                    Modifier.fillMaxWidth(), loading = state.busy, enabled = state.canSaveKey)
            }
            WizardStep.Form -> Row(Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s)) {
                JButton(stringResource(R.string.settings_src_save), { scope.launch { model.save()?.let { id -> feedback?.notice(res.getString(R.string.settings_src_provider_added, draft.name.trim())); onSaved(id) } } },
                    Modifier.fillMaxWidth(), loading = state.busy, enabled = state.canSave)
            }
        }
    }) {
        manager?.let { owner -> item("manager") { ModelConfigurationOwner(owner) } }
        if (!state.loaded) item("loading") { JLoading() }
        when (state.step) {
            WizardStep.Pick -> if (state.loaded) pickStep(res, state, model::choose)
            WizardStep.Key -> if (preset != null) keyStep(state, preset, editable, model::editKey, model::setAssign,
                onRetest = { scope.launch { model.testKey() } }, onModel = { choosingModel = true }, more = more, onMore = { more = !more })
            WizardStep.Done -> state.added?.let { added -> doneStep(state, added) }
            WizardStep.Form -> {
                if (!state.hubNames && state.presets.isNotEmpty()) item("source") {
                    JSegmented(listOf(JSegment(stringResource(R.string.settings_src_provider_manual)), JSegment(stringResource(R.string.settings_src_provider_from_extension))), if (draft.fromPreset) 1 else 0,
                        { model.edit(draft.copy(fromPreset = it == 1)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.s))
                }
                if (state.loaded) {
                    if (state.usesPreset) presetForm(res, state, editable, model::edit, model::pick, onModel = { choosingModel = true })
                    else manualForm(state, editable, model::edit, onProbe = { scope.launch { model.probeForm() } },
                        onDiscover = { scope.launch { model.discoverForm() } }, onPickFound = { choosingModel = true })
                }
            }
        }
        state.error?.let { error -> item("error") { JInlineStatus(error.text(), Modifier.padding(top = Tokens.Space.m), tone = InlineTone.Failed) } }
    }
    val found = state.found?.models.orEmpty()
    if (choosingModel && state.step == WizardStep.Form && !state.usesPreset && found.isNotEmpty()) JActionSheet(found.map { choice ->
        JAction(choice.label, checked = choice.id == draft.model, onClick = { model.edit(draft.copy(model = choice.id)) })
    }, { choosingModel = false }, title = stringResource(R.string.settings_src_provider_default_model))
    else if (choosingModel && preset != null) JActionSheet(preset.choices.map { choice ->
        JAction(choice.label, checked = choice.id == draft.presetModel, onClick = {
            if (state.step == WizardStep.Key) model.editKey(draft.copy(presetModel = choice.id)) else model.edit(draft.copy(presetModel = choice.id))
        })
    }, { choosingModel = false }, title = stringResource(R.string.settings_src_provider_default_model),
        note = preset.modelsError?.let { stringResource(R.string.settings_src_provider_models_error, it) })
}

/** How long a pasted or typed key has to stay the same before it is tested by itself. */
private const val AutoTestMillis = 700L

private fun androidx.compose.foundation.lazy.LazyListScope.nameField(state: NewProviderState, editable: Boolean, onEdit: (NewProviderDraft) -> Unit, example: String) {
    item("name") {
        JTextField(state.draft.name, { onEdit(state.draft.copy(name = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m), label = stringResource(R.string.settings_src_provider_name),
            placeholder = stringResource(R.string.settings_src_provider_name_example, example), singleLine = true, enabled = editable, isError = state.nameProblem != null,
            supportingText = state.nameProblem?.text() ?: stringResource(R.string.settings_src_provider_name_hint))
    }
}

private fun androidx.compose.foundation.lazy.LazyListScope.keyField(state: NewProviderState, editable: Boolean, onEdit: (NewProviderDraft) -> Unit, hint: String?) {
    item("key") {
        JTextField(state.draft.apiKey, { onEdit(state.draft.copy(apiKey = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m), label = "API Key",
            singleLine = true, enabled = editable, visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            supportingText = listOfNotNull(hint, stringResource(R.string.settings_src_provider_key_hint)).joinToString("\n"))
    }
}

/** "From extension": the presets to pick from, the name, the key and the model. */
private fun androidx.compose.foundation.lazy.LazyListScope.presetForm(
    res: Resources, state: NewProviderState, editable: Boolean, onEdit: (NewProviderDraft) -> Unit, onPick: (String) -> Unit, onModel: () -> Unit,
) {
    val preset = state.selected ?: return
    item("presets-title") { JSectionHeader(stringResource(R.string.settings_src_provider_presets)) }
    itemsIndexed(state.presets, key = { _, it -> "preset-${it.id}" }) { index, p ->
        JGroupItem(GroupPosition.of(index, state.presets.size)) {
            JRow(p.name.ifBlank { p.id }, subtitle = listOfNotNull(p.description?.takeIf { it.isNotBlank() }, presetSubtitle(res, p)).joinToString("\n"),
                subtitleLines = 3, enabled = editable, selected = p.id == preset.id, onClick = { onPick(p.id) },
                trailing = if (p.id == preset.id) ({ JIcon(PhIcons.Check, stringResource(R.string.settings_src_provider_selected), tint = PersonaTheme.palette.accent) }) else ({}))
        }
    }
    item("presets-note") {
        SettingsCaption(stringResource(R.string.settings_src_provider_preset_note, preset.extension),
            Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
    }
    nameField(state, editable, onEdit, "jv_team")
    keyField(state, editable, onEdit, preset.keyHint?.takeIf { it.isNotBlank() })
    item("preset-model") {
        if (preset.choices.isEmpty()) {
            JTextField(state.draft.presetModel, { onEdit(state.draft.copy(presetModel = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m),
                label = stringResource(R.string.settings_src_provider_default_model), singleLine = true, enabled = editable)
        } else {
            val picked = preset.choices.firstOrNull { it.id == state.draft.presetModel }
            JGroupItem(GroupPosition.Single, Modifier.padding(top = Tokens.Space.m)) {
                JRow(stringResource(R.string.settings_src_provider_default_model), value = picked?.label ?: state.draft.presetModel.ifEmpty { stringResource(R.string.settings_src_provider_extension_default) }, enabled = editable, onClick = onModel)
            }
        }
    }
    item("preset-model-note") {
        SettingsCaption(preset.modelsError?.takeIf { it.isNotBlank() }?.let { stringResource(R.string.settings_src_provider_models_error, it) }
            ?: stringResource(R.string.settings_src_provider_model_note),
            Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
    }
}

/** "Fill in": the name, the kind, the gateway, its auth and key ("Test connection", "Fetch models"), the models, the flags. */
private fun androidx.compose.foundation.lazy.LazyListScope.manualForm(
    state: NewProviderState, editable: Boolean, onEdit: (NewProviderDraft) -> Unit,
    onProbe: () -> Unit, onDiscover: () -> Unit, onPickFound: () -> Unit,
) {
    val draft = state.draft
    val api = draft.kind == "api"
    nameField(state, editable, onEdit, "jv_strong")
    item("kind") {
        Column(Modifier.padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JSegmented(listOf(JSegment(stringResource(R.string.settings_src_provider_kind_launcher)), JSegment(stringResource(R.string.settings_src_provider_kind_api))), if (api) 1 else 0,
                { if (editable) onEdit(draft.copy(kind = if (it == 1) "api" else "launcher")) }, Modifier.fillMaxWidth())
            if (api) JSegmented(listOf(JSegment("Messages"), JSegment("Responses")), if (draft.api == "responses") 1 else 0,
                { if (editable) onEdit(draft.copy(api = if (it == 1) "responses" else "messages")) }, Modifier.fillMaxWidth())
            SettingsCaption(stringResource(if (api) R.string.settings_src_provider_api_note else R.string.settings_src_provider_launcher_note),
                Modifier.padding(horizontal = rowPadding))
        }
    }
    item("base-url") {
        JTextField(draft.baseUrl, { onEdit(draft.copy(baseUrl = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m), label = "Base URL",
            placeholder = "https://…", singleLine = true, enabled = editable, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            supportingText = stringResource(R.string.settings_src_provider_base_url_hint))
    }
    // The labels are the header names a gateway's docs use, so they are not translated.
    if (!api) item("auth") {
        Column(Modifier.padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JSegmented(listOf(JSegment("x-api-key"), JSegment("Bearer")), if (draft.auth == "bearer") 1 else 0,
                { if (editable) onEdit(draft.copy(auth = if (it == 1) "bearer" else "api_key")) }, Modifier.fillMaxWidth())
            SettingsCaption(stringResource(if (draft.auth == "bearer") R.string.settings_onb_auth_bearer_hint else R.string.settings_onb_auth_key_hint),
                Modifier.padding(horizontal = rowPadding))
        }
    }
    keyField(state, editable, onEdit, null)
    // The hub's test speaks Messages: a Responses API has none (its models are typed in).
    if (!(api && draft.api == "responses")) item("probe") { FormProbe(state, onProbe, onDiscover, onPickFound) }
    item("model") {
        JTextField(draft.model, { onEdit(draft.copy(model = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m), label = stringResource(R.string.settings_src_provider_default_model),
            singleLine = true, enabled = editable, supportingText = stringResource(R.string.settings_src_provider_model_note))
    }
    if (!api) {
        item("small-model") {
            JTextField(draft.smallModel, { onEdit(draft.copy(smallModel = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m),
                label = stringResource(R.string.settings_src_provider_small_model), placeholder = stringResource(R.string.settings_src_provider_same_as_above), singleLine = true, enabled = editable)
        }
        item("subagent-model") {
            JTextField(draft.subagentModel, { onEdit(draft.copy(subagentModel = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m),
                label = stringResource(R.string.settings_src_provider_subagent_model), placeholder = stringResource(R.string.settings_src_provider_same_as_above), singleLine = true, enabled = editable,
                supportingText = stringResource(R.string.settings_src_provider_subagent_hint))
        }
        item("args") {
            JTextField(draft.args, { onEdit(draft.copy(args = it)) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m), label = stringResource(R.string.settings_src_provider_args),
                placeholder = stringResource(R.string.settings_src_provider_args_example), singleLine = true, enabled = editable,
                supportingText = stringResource(R.string.settings_src_provider_args_hint))
        }
    }
}

/** "Test connection" and "Fetch models" of a gateway typed in, with what each answered. */
@OptIn(ExperimentalLayoutApi::class)
@androidx.compose.runtime.Composable
private fun FormProbe(state: NewProviderState, onProbe: () -> Unit, onDiscover: () -> Unit, onPickFound: () -> Unit) {
    val ready = state.formProbe != null && !state.busy
    Column(Modifier.padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JButton(stringResource(R.string.settings_onb_test), onProbe, kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.Play,
                loading = state.probing, enabled = ready)
            JButton(stringResource(R.string.settings_onb_discover), onDiscover, kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.ArrowDown,
                loading = state.discovering, enabled = ready)
        }
        SettingsCaption(stringResource(R.string.settings_onb_test_cost), Modifier.padding(horizontal = rowPadding))
        val probe = state.probe
        when {
            state.probeFailure != null -> JInlineStatus(state.probeFailure.text(), tone = InlineTone.Failed)
            probe?.ok == true -> JInlineStatus(stringResource(R.string.settings_onb_key_test_ok, probe.model.orEmpty(), probe.ms), tone = InlineTone.Done)
            probe != null -> JInlineStatus(probe.error?.takeIf { it.isNotBlank() } ?: stringResource(R.string.settings_onb_key_test_failed), tone = InlineTone.Failed)
        }
        val found = state.found
        when {
            state.foundFailure != null -> JInlineStatus(stringResource(R.string.settings_onb_discover_failed, state.foundFailure.text()), tone = InlineTone.Caution)
            !found?.error.isNullOrBlank() -> JInlineStatus(stringResource(R.string.settings_onb_discover_failed, found?.error.orEmpty()), tone = InlineTone.Caution)
            found != null && found.models.isEmpty() -> JInlineStatus(stringResource(R.string.settings_onb_discover_none), tone = InlineTone.Caution)
            found != null -> JInlineStatus(pluralStringResource(R.plurals.settings_onb_discover_found, found.models.size, found.models.size), tone = InlineTone.Done,
                action = { JTextButton(stringResource(R.string.settings_onb_discover_pick), onPickFound) })
        }
    }
}
