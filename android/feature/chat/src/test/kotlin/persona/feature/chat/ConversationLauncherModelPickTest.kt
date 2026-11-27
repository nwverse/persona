package persona.feature.chat

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.ModelPick
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionCapabilities
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalHubFeatures
import persona.core.designsystem.PersonaTheme
import persona.core.domain.models.PersonaModelPanel
import persona.core.hub.HubError
import persona.core.hub.HubFailure

/**
 * Verifies Brief and Board `ConversationLauncherChip` synchronization with hub-persisted `assistant_model_pick`
 * (`session.modelPick` reflection, `PUT`/`DELETE …/conversations/{id}/model`, unlisted/loading locked model display
 * and one-tap reset, optimistic rollback on failure, legacy hub/unsynced fallback, and Glass non-regression).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ConversationLauncherModelPickTest {
    @get:Rule val rule = createComposeRule()

    private val defaultLabels = ConversationLauncherLabels(
        launcherSection = "Launcher",
        modelSection = "Model",
        useDefault = "Use the default",
        launchersLoading = "Loading launchers…",
        launchersFailed = "Couldn't load launchers",
        noLaunchers = "No launcher on this computer can run Persona",
        defaultModel = "Default model",
        modelsLoading = "Loading models…",
        modelsFailed = "Couldn't load models",
        modelsUnavailable = "This hub has no model list",
    )

    private fun assistantSession(
        sessionId: String = "main",
        launcher: String = "agy",
        modelPick: ModelPick? = null,
        rev: Long = 3L,
    ) = Session(
        machineId = "box",
        sessionId = sessionId,
        assistant = true,
        status = "idle",
        state = "idle",
        launcher = launcher,
        modelPick = modelPick,
        rev = rev,
        capabilities = SessionCapabilities(read = true, send = true, modes = true),
    )

    private open class RecordingHub(
        var configLauncher: String = "agy",
        var launchers: List<AgentLauncher> = listOf(AgentLauncher("agy"), AgentLauncher("claude")),
        var modelsByLauncher: Map<String, LauncherModels?> = mapOf(
            "agy" to LauncherModels(
                agent = "agy",
                models = listOf(
                    AgentModel("default", name = "Default", isDefault = true),
                    AgentModel("claude-opus-4-6-thinking", name = "Opus 4.6"),
                    AgentModel("gemini-3-pro", name = "Gemini 3 Pro"),
                ),
            ),
            "claude" to LauncherModels(
                agent = "claude",
                models = listOf(
                    AgentModel("default", name = "Default", isDefault = true),
                    AgentModel("sonnet", name = "Sonnet 4.5"),
                ),
            ),
        ),
    ) : AssistantManagement {
        val requests = mutableListOf<String>()

        override suspend fun config() = AssistantConfig(machineId = "box", launcher = configLauncher, main = "main")
        override suspend fun assistantLaunchers(machineId: String) = launchers
        override suspend fun launcherModels(machineId: String, launcher: String) = modelsByLauncher[launcher]
        override suspend fun setModelPick(session: SessionKey, pick: ModelPick): ModelPick {
            requests += "PUT ${session.sessionId} ${pick.launcher} ${pick.model.orEmpty()}"
            return pick
        }
        override suspend fun clearModelPick(session: SessionKey) {
            requests += "DELETE ${session.sessionId}"
        }
        override suspend fun warm() = Unit
        override suspend fun endConversation(session: SessionKey) = Unit
        override suspend fun tasks() = emptyList<ScheduledTask>()
        override suspend fun saveTask(task: ScheduledTask) = task
        override suspend fun deleteTask(id: String) = Unit
        override suspend fun runTask(id: String) = SessionKey("box", "main")
        override suspend fun freshTask(id: String) = ScheduledTask(id)
        override suspend fun runs() = emptyList<Run>()
        override suspend fun notes() = AssistantNotes()
        override suspend fun setNotes(text: String) = Unit
        override suspend fun memory() = emptyList<MemoryFile>()
        override suspend fun saveMemory(name: String, content: String) = Unit
        override suspend fun deleteMemory(name: String) = Unit
    }

    private fun hasDialogText(text: String) = hasText(text) and hasAnyAncestor(isDialog())

    // --- Domain / pure state tests ---

    @Test fun keptHubReadsServerModelPickInsteadOfPageStateAndHandlesOptimisticSync() {
        val locked = assistantSession(modelPick = ModelPick("agy", "claude-opus-4-6-thinking"), rev = 5L)
        val initial = resolveConversationLauncherState(
            session = locked,
            kept = true,
            drawn = null,
            followsDefault = true,
            catalogDefaultLauncher = null,
            configLauncher = "agy",
            sessionLauncher = "agy",
            pageLauncher = "claude",
            pageModel = "stale-local",
        )
        assertTrue(initial.kept)
        assertTrue(initial.overridden)
        assertEquals("agy", initial.activeLauncher)
        assertEquals("claude-opus-4-6-thinking", initial.activeModel)
        assertEquals("agy", initial.effectiveLauncher)
        assertFalse(initial.saving)

        val optimisticClear = DrawnConversationPick(
            pick = null,
            beforePick = ModelPick("agy", "claude-opus-4-6-thinking"),
            beforeRev = 5L,
            saving = true,
        )
        val whileSaving = resolveConversationLauncherState(
            session = locked,
            kept = true,
            drawn = optimisticClear,
            followsDefault = true,
            catalogDefaultLauncher = null,
            configLauncher = "agy",
            sessionLauncher = "agy",
            pageLauncher = null,
            pageModel = null,
        )
        assertFalse(whileSaving.overridden)
        assertNull(whileSaving.activeModel)
        assertTrue(whileSaving.saving)

        // Settled optimistic pick holds while authoritative session row is unchanged, then yields to remote updates even on the same rev.
        val settled = optimisticClear.copy(saving = false)
        val beforePush = resolveConversationLauncherState(
            session = locked,
            kept = true,
            drawn = settled,
            followsDefault = true,
            catalogDefaultLauncher = null,
            configLauncher = "agy",
            sessionLauncher = "agy",
            pageLauncher = null,
            pageModel = null,
        )
        assertFalse(beforePush.overridden)
        assertFalse(beforePush.saving)

        val remoteUpdated = locked.copy(modelPick = ModelPick("claude", "sonnet"))
        val afterRemotePush = resolveConversationLauncherState(
            session = remoteUpdated,
            kept = true,
            drawn = settled,
            followsDefault = true,
            catalogDefaultLauncher = null,
            configLauncher = "agy",
            sessionLauncher = "agy",
            pageLauncher = null,
            pageModel = null,
        )
        assertTrue(afterRemotePush.overridden)
        assertEquals("claude", afterRemotePush.activeLauncher)
        assertEquals("sonnet", afterRemotePush.activeModel)
    }

    @Test fun legacyHubAndNullSessionFallBackToPageLocalPickIgnoringStaleModelPick() {
        val withServerPick = assistantSession(modelPick = ModelPick("agy", "claude-opus-4-6-thinking"))
        val legacy = resolveConversationLauncherState(
            session = withServerPick,
            kept = false,
            drawn = null,
            followsDefault = true,
            catalogDefaultLauncher = null,
            configLauncher = "agy",
            sessionLauncher = "agy",
            pageLauncher = "claude",
            pageModel = "sonnet",
        )
        assertFalse(legacy.kept)
        assertTrue(legacy.overridden)
        assertEquals("claude", legacy.activeLauncher)
        assertEquals("sonnet", legacy.activeModel)

        val unsynced = resolveConversationLauncherState(
            session = null,
            kept = true,
            drawn = null,
            followsDefault = true,
            catalogDefaultLauncher = null,
            configLauncher = "agy",
            sessionLauncher = null,
            pageLauncher = null,
            pageModel = null,
        )
        assertFalse(unsynced.kept)
        assertFalse(unsynced.overridden)
        assertEquals("agy", unsynced.effectiveLauncher)
    }

    @Test fun actionPlannerMapsClearSwitchAndNoOpAccurately() {
        val defaultState = ConversationLauncherState(
            kept = true,
            activeLauncher = null,
            activeModel = null,
            defaultLauncher = "agy",
            effectiveLauncher = "agy",
            overridden = false,
            saving = false,
        )
        assertEquals(ConversationLauncherAction.NoOp, planClearToDefault(defaultState))
        assertEquals(ConversationLauncherAction.NoOp, planSelectDefaultModel(defaultState))
        assertEquals(
            ConversationLauncherAction.SetPick(ModelPick("claude", null)),
            planSelectLauncher(defaultState, "claude"),
        )
        val gemini = AgentModel("gemini-3-pro", "Gemini 3 Pro")
        assertEquals(
            ConversationLauncherAction.SetPick(ModelPick("agy", "gemini-3-pro")),
            planSelectModel(defaultState, gemini, null),
        )

        val lockedOnDefaultLauncher = defaultState.copy(
            activeLauncher = "agy",
            activeModel = "claude-opus-4-6-thinking",
            overridden = true,
        )
        assertEquals(ConversationLauncherAction.ClearPick, planClearToDefault(lockedOnDefaultLauncher))
        assertEquals(ConversationLauncherAction.ClearPick, planSelectDefaultModel(lockedOnDefaultLauncher))

        val lockedOnOtherLauncher = defaultState.copy(
            activeLauncher = "claude",
            activeModel = "sonnet",
            effectiveLauncher = "claude",
            overridden = true,
        )
        assertEquals(ConversationLauncherAction.ClearPick, planClearToDefault(lockedOnOtherLauncher))
        assertEquals(
            ConversationLauncherAction.SetPick(ModelPick("claude", null)),
            planSelectDefaultModel(lockedOnOtherLauncher),
        )
        assertEquals(
            ConversationLauncherAction.NoOp,
            planSelectLauncher(lockedOnOtherLauncher.copy(activeModel = null), "claude"),
        )
        val sonnet = AgentModel("sonnet", "Sonnet 4.5")
        assertEquals(ConversationLauncherAction.NoOp, planSelectModel(lockedOnOtherLauncher, sonnet, sonnet))
    }

    @Test fun listedAgentModelPrefersConcreteRowOverDefaultAlias() {
        val defaultRow = AgentModel("default", "Default", resolved = "claude-opus-5-5", isDefault = true)
        val opusRow = AgentModel("claude-opus-5-5", "Opus 5.5", aliases = listOf("opus"))
        assertEquals(opusRow, listedAgentModel(listOf(defaultRow, opusRow), "opus"))
        assertEquals(opusRow, listedAgentModel(listOf(defaultRow, opusRow), "claude-opus-5-5"))
    }

    @Test fun sheetActionsExposeUnlistedAndLoadingLockedModelWithoutCheckingDefault() {
        val lockedState = ConversationLauncherState(
            kept = true,
            activeLauncher = "agy",
            activeModel = "legacy-locked-model",
            defaultLauncher = "agy",
            effectiveLauncher = "agy",
            overridden = true,
            saving = false,
        )
        assertEquals(
            "legacy-locked-model",
            conversationLauncherChipModelText(lockedState, emptyList(), "Model selected"),
        )

        // 1. Unlisted locked model when launcher and model lists have loaded.
        val recordedUnlisted = mutableListOf<ConversationLauncherAction>()
        val unlistedActions = buildConversationLauncherActions(
            state = lockedState,
            launchers = listOf(AgentLauncher("agy"), AgentLauncher("claude")),
            launchersFailed = false,
            currentModels = LauncherModels(
                agent = "agy",
                models = listOf(
                    AgentModel("default", "Default", isDefault = true),
                    AgentModel("gemini-3-pro", "Gemini 3 Pro"),
                ),
            ),
            modelsLoading = false,
            modelsFailed = false,
            actionEnabled = true,
            labels = defaultLabels,
            onKeptAction = recordedUnlisted::add,
            onLegacyPick = { _, _ -> error("legacy callback must not fire in kept mode") },
        )
        val useDefault = unlistedActions.single { it.label == "Use the default" }
        val defaultModel = unlistedActions.single { it.label == "Default model" }
        val lockedRow = unlistedActions.single { it.label == "legacy-locked-model" }
        val agyRow = unlistedActions.single { it.label == "agy" }
        val geminiRow = unlistedActions.single { it.label == "Gemini 3 Pro" }
        assertFalse("Use the default must not be checked when locked", useDefault.checked)
        assertFalse("Default model must not be checked when locked", defaultModel.checked)
        assertTrue("Locked model row must be checked", lockedRow.checked)
        assertTrue("Locked launcher row must be checked", agyRow.checked)
        assertFalse(geminiRow.checked)
        useDefault.onClick()
        defaultModel.onClick()
        geminiRow.onClick()
        assertEquals(
            listOf(
                ConversationLauncherAction.ClearPick,
                ConversationLauncherAction.ClearPick,
                ConversationLauncherAction.SetPick(ModelPick("agy", "gemini-3-pro")),
            ),
            recordedUnlisted,
        )

        // 2. Launchers and models still loading: locked launcher and locked model ID stay visible and checked, one-tap clear stays enabled.
        val recordedLoading = mutableListOf<ConversationLauncherAction>()
        val loadingActions = buildConversationLauncherActions(
            state = lockedState,
            launchers = null,
            launchersFailed = false,
            currentModels = null,
            modelsLoading = true,
            modelsFailed = false,
            actionEnabled = true,
            labels = defaultLabels,
            onKeptAction = recordedLoading::add,
            onLegacyPick = { _, _ -> error("legacy callback must not fire in kept mode") },
        )
        assertFalse(loadingActions.single { it.label == "Use the default" }.checked)
        assertTrue(loadingActions.single { it.label == "agy" }.checked)
        assertFalse(loadingActions.single { it.label == "Loading launchers…" }.enabled)
        assertFalse(loadingActions.single { it.label == "Default model" }.checked)
        assertTrue(loadingActions.single { it.label == "legacy-locked-model" }.checked)
        assertFalse(loadingActions.single { it.label == "Loading models…" }.enabled)
        loadingActions.single { it.label == "Use the default" }.onClick()
        assertEquals(listOf(ConversationLauncherAction.ClearPick), recordedLoading)

        // 3. In-flight save disables clickable actions.
        val savingActions = buildConversationLauncherActions(
            state = lockedState.copy(saving = true),
            launchers = listOf(AgentLauncher("agy")),
            launchersFailed = false,
            currentModels = null,
            modelsLoading = true,
            modelsFailed = false,
            actionEnabled = false,
            labels = defaultLabels,
            onKeptAction = {},
            onLegacyPick = { _, _ -> },
        )
        assertTrue(savingActions.none { it.enabled })
    }

    // --- Compose UI & interaction tests ---

    @Test fun briefReflectsServerPickSwitchViaPutAndResetViaDelete() =
        verifyBriefOrBoardPutAndDelete(AppStyle.Brief)

    @Test fun boardReflectsServerPickSwitchViaPutAndResetViaDelete() =
        verifyBriefOrBoardPutAndDelete(AppStyle.Board)

    private fun verifyBriefOrBoardPutAndDelete(style: AppStyle) {
        val hub = RecordingHub()
        var session by mutableStateOf(
            assistantSession(modelPick = ModelPick("agy", "claude-opus-4-6-thinking"), rev = 3L),
        )
        var legacyCalls = 0
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = style) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = session.launcher,
                        pickedLauncher = null,
                        pickedModel = null,
                        session = session,
                    ) { _, _ -> legacyCalls++ }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · Opus 4.6")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Default: agy").assertDoesNotExist()
        rule.onNodeWithText("agy · Opus 4.6").assertIsSelected().performClick()

        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Gemini 3 Pro")).performClick()
        rule.waitUntil(5_000) { hub.requests.size == 1 }
        assertEquals("PUT main agy gemini-3-pro", hub.requests[0])
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }

        // Simulate hub push confirming the new pick, then reset via "Default model" on default launcher -> DELETE.
        rule.runOnIdle { session = session.copy(modelPick = ModelPick("agy", "gemini-3-pro"), rev = 4L) }
        rule.onNodeWithText("agy · Gemini 3 Pro").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Default model")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Default model")).performClick()
        rule.waitUntil(5_000) { hub.requests.size == 2 }
        assertEquals("DELETE main", hub.requests[1])
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Default: agy").assertIsNotSelected()

        // Switch to another launcher via PUT, then clear via "Use the default" -> DELETE.
        rule.runOnIdle { session = session.copy(modelPick = null, rev = 5L) }
        rule.onNodeWithText("Default: agy").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("claude")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("claude")).performClick()
        rule.waitUntil(5_000) { hub.requests.size == 3 }
        assertEquals("PUT main claude ", hub.requests[2])
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("claude")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.runOnIdle { session = session.copy(modelPick = ModelPick("claude"), rev = 6L) }
        rule.onNodeWithText("claude").assertIsSelected().performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Use the default")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Use the default")).performClick()
        rule.waitUntil(5_000) { hub.requests.size == 4 }
        assertEquals("DELETE main", hub.requests[3])
        assertEquals(0, legacyCalls)
    }

    @Test fun unsyncedNullSessionFallsBackToLocalPickEvenWhenHubAdvertisesModelPick() {
        val hub = RecordingHub()
        var pickedLauncher by mutableStateOf<String?>(null)
        var pickedModel by mutableStateOf<String?>(null)
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Brief) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = "agy",
                        pickedLauncher = pickedLauncher,
                        pickedModel = pickedModel,
                        session = null,
                    ) { l, m ->
                        pickedLauncher = l
                        pickedModel = m
                    }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Default: agy").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Gemini 3 Pro")).performClick()
        rule.waitForIdle()
        assertNull(pickedLauncher)
        assertEquals("gemini-3-pro", pickedModel)
        assertTrue("unsynced chat must keep local fallback without PUT/DELETE", hub.requests.isEmpty())
    }

    @Test fun clearRequestedOptimisticallyClearsServerPickViaDelete() {
        val hub = RecordingHub()
        val session = assistantSession(modelPick = ModelPick("agy", "legacy-locked-model"))
        val notices = mutableListOf<String>()
        var clearRequested by mutableStateOf(false)
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Brief) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = session.launcher,
                        pickedLauncher = null,
                        pickedModel = null,
                        session = session,
                        clearRequested = clearRequested,
                        onClearHandled = { clearRequested = false },
                        onNotice = notices::add,
                    ) { _, _ -> }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · legacy-locked-model")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.runOnIdle { clearRequested = true }
        rule.waitForIdle()
        assertEquals("requests=${hub.requests} clear=$clearRequested notices=$notices", listOf("DELETE main"), hub.requests)
        assertFalse("clearRequested should be handled", clearRequested)
        rule.onNodeWithText("Default: agy").assertIsDisplayed()
    }

    @Test fun unlistedLockedModelShowsIdInChipAndSheetAndClearsInOneTap() {
        val hub = RecordingHub(
            modelsByLauncher = mapOf(
                "agy" to LauncherModels(
                    agent = "agy",
                    models = listOf(
                        AgentModel("default", name = "Default", isDefault = true),
                        AgentModel("gemini-3-pro", name = "Gemini 3 Pro"),
                    ),
                ),
            ),
        )
        val session = assistantSession(modelPick = ModelPick("agy", "legacy-locked-model"))
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Brief) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = session.launcher,
                        pickedLauncher = null,
                        pickedModel = null,
                        session = session,
                    ) { _, _ -> error("kept hub must not call legacy onPick") }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · legacy-locked-model")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Default: agy").assertDoesNotExist()
        rule.onNodeWithText("agy · Model selected").assertDoesNotExist()
        rule.onNodeWithText("agy · legacy-locked-model").assertIsSelected().performClick()

        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("legacy-locked-model")).assertIsDisplayed()
        rule.onNode(hasDialogText("Use the default")).performClick()
        rule.waitUntil(5_000) { hub.requests == listOf("DELETE main") }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Default: agy").assertIsNotSelected()
    }

    @Test fun loadingLaunchersAndModelsStillDisplaysLockedModelIdAndClearsInOneTap() {
        val launchersGate = CompletableDeferred<List<AgentLauncher>>()
        val modelsGate = CompletableDeferred<LauncherModels?>()
        val hub = object : RecordingHub() {
            override suspend fun assistantLaunchers(machineId: String): List<AgentLauncher> = launchersGate.await()
            override suspend fun launcherModels(machineId: String, launcher: String): LauncherModels? = modelsGate.await()
        }
        val session = assistantSession(modelPick = ModelPick("agy", "claude-opus-4-6-thinking"))
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Board) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = session.launcher,
                        pickedLauncher = null,
                        pickedModel = null,
                        session = session,
                    ) { _, _ -> error("kept hub must not call legacy onPick") }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · claude-opus-4-6-thinking")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("agy · claude-opus-4-6-thinking").assertIsSelected().performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Loading launchers…")).fetchSemanticsNodes().isNotEmpty() &&
                rule.onAllNodes(hasDialogText("Loading models…")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("agy")).assertIsDisplayed()
        rule.onNode(hasDialogText("claude-opus-4-6-thinking")).assertIsDisplayed()
        rule.onNode(hasDialogText("Use the default")).performClick()
        rule.waitUntil(5_000) { hub.requests == listOf("DELETE main") }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isNotEmpty()
        }
        launchersGate.cancel()
        modelsGate.cancel()
    }

    @Test fun inFlightSaveBlocksReopenAndFailureRollsBackWithNotice() {
        val putGate = CompletableDeferred<ModelPick>()
        val hub = object : RecordingHub() {
            override suspend fun setModelPick(session: SessionKey, pick: ModelPick): ModelPick {
                super.setModelPick(session, pick)
                return putGate.await()
            }
        }
        val notices = mutableListOf<String>()
        val session = assistantSession(modelPick = ModelPick("agy", "claude-opus-4-6-thinking"))
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Brief) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = session.launcher,
                        pickedLauncher = null,
                        pickedModel = null,
                        session = session,
                        onNotice = notices::add,
                    ) { _, _ -> }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · Opus 4.6")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("agy · Opus 4.6").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Gemini 3 Pro")).performClick()

        // Optimistic chip label is shown immediately, and tapping while saving does not reopen the sheet.
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("agy · Gemini 3 Pro").performClick()
        rule.waitForIdle()
        rule.onNode(isDialog()).assertDoesNotExist()

        // Refusal rolls the chip back and surfaces a localized notice.
        rule.runOnIdle {
            putGate.completeExceptionally(
                HubFailure(
                    HubError.Server,
                    message = "Launcher not available",
                    status = 409,
                    code = "launcher_missing",
                ),
            )
        }
        rule.waitForIdle()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · Opus 4.6")).fetchSemanticsNodes().isNotEmpty() &&
                notices.isNotEmpty()
        }
        assertTrue(notices.single().isNotBlank())
    }

    @Test fun nonMainConversationShowsChipWhileLockedAndHidesAfterClear() {
        val hub = RecordingHub()
        val session = assistantSession(
            sessionId = "side",
            launcher = "agy",
            modelPick = ModelPick("agy", "legacy-locked-model"),
        )
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Brief) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "side",
                        sessionLauncher = session.launcher,
                        pickedLauncher = null,
                        pickedModel = null,
                        session = session,
                    ) { _, _ -> }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · legacy-locked-model")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("agy · legacy-locked-model").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Use the default")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Use the default")).performClick()
        rule.waitUntil(5_000) { hub.requests == listOf("DELETE side") }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("agy · legacy-locked-model")).fetchSemanticsNodes().isEmpty() &&
                rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isEmpty()
        }
    }

    @Test fun legacyHubWithoutModelPickFeatureKeepsLocalFallback() {
        val hub = RecordingHub()
        val session = assistantSession(modelPick = ModelPick("agy", "claude-opus-4-6-thinking"))
        var pickedLauncher by mutableStateOf<String?>(null)
        var pickedModel by mutableStateOf<String?>(null)
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides emptySet()) {
                PersonaTheme(reduceMotion = true, style = AppStyle.Brief) {
                    ConversationLauncherChip(
                        api = hub,
                        machineId = "box",
                        sessionId = "main",
                        sessionLauncher = session.launcher,
                        pickedLauncher = pickedLauncher,
                        pickedModel = pickedModel,
                        session = session,
                    ) { l, m ->
                        pickedLauncher = l
                        pickedModel = m
                    }
                }
            }
        }
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Default: agy").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Gemini 3 Pro")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Gemini 3 Pro")).performClick()
        rule.waitForIdle()
        assertNull(pickedLauncher)
        assertEquals("gemini-3-pro", pickedModel)
        assertTrue("older hub must not call PUT/DELETE model", hub.requests.isEmpty())
    }

    @Test fun chatScreenBriefAndBoardSyncServerModelPickWithoutAffectingGlass() {
        val hub = RecordingHub(
            modelsByLauncher = mapOf(
                "agy" to LauncherModels(
                    agent = "agy",
                    models = listOf(
                        AgentModel("default", name = "Default", isDefault = true),
                        AgentModel("gemini-3-pro", name = "Gemini 3 Pro"),
                    ),
                ),
            ),
        )
        val initialRow = assistantSession(modelPick = ModelPick("agy", "legacy-locked-model"), rev = 3L)
        val sessionsFlow = MutableStateFlow(listOf(initialRow))
        var refreshCount = 0
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(Machine("box", "workstation", online = true)))
            override val sessions = sessionsFlow
            override suspend fun refresh() {
                refreshCount++
                if (hub.requests.lastOrNull() == "DELETE main") {
                    sessionsFlow.value = listOf(initialRow.copy(modelPick = null, rev = 4L))
                }
            }
        }
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(loading = false))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
        }
        val sentOverrides = mutableListOf<Pair<String?, String?>>()
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
            override suspend fun send(
                session: SessionKey,
                text: String,
                clientMessageId: String,
                clearDraft: String?,
                launcher: String?,
                model: String?,
            ) {
                sentOverrides += launcher to model
            }
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(emptyList<Interaction>())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        var style by mutableStateOf(AppStyle.Brief)
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf(PersonaModelPanel.MODEL_PICK)) {
                PersonaTheme(reduceMotion = true, style = style) {
                    ChatScreen(
                        sessionKey = initialRow.key,
                        repository = chats,
                        sender = sender,
                        interactions = interactions,
                        sessions = sessions,
                        onBack = {},
                        renderStyle = ChatRenderStyle.Assistant,
                        chrome = ChatChromeBindings(assistantManagement = hub),
                    )
                }
            }
        }

        // Brief and Board both display the server-locked model ID instead of "Default: agy".
        for (targetStyle in listOf(AppStyle.Brief, AppStyle.Board)) {
            rule.runOnIdle { style = targetStyle }
            rule.waitUntil(10_000) {
                rule.onAllNodes(hasText("agy · legacy-locked-model")).fetchSemanticsNodes().isNotEmpty()
            }
            rule.onNodeWithText("Default: agy").assertDoesNotExist()
        }

        // Clearing from Board hits DELETE /api/v1/assistant/conversations/{id}/model and refreshes sessions.
        rule.onNodeWithText("agy · legacy-locked-model").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasDialogText("Use the default")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasDialogText("Use the default")).performClick()
        rule.waitForIdle()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasText("Default: agy")).fetchSemanticsNodes().isNotEmpty() &&
                hub.requests == listOf("DELETE main") &&
                refreshCount >= 1
        }

        // Sending on a hub with assistant_model_pick sends no per-message launcher/model override.
        rule.onNode(hasSetTextAction()).performTextInput("Hello")
        rule.onNodeWithContentDescription("Send").performClick()
        rule.waitForIdle()
        assertEquals(listOf(null to null), sentOverrides)

        // Glass continues to render its PersonaModelPill (when the composer expands) without Brief/Board's launcher chip.
        rule.runOnIdle { style = AppStyle.Glass }
        rule.waitForIdle()
        rule.onNodeWithText("Default: agy").assertDoesNotExist()
        rule.onNodeWithText("Ask Persona…").performClick()
        rule.waitUntil(5_000) {
            rule.onAllNodes(hasTestTag("model-pill")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithTag("model-pill").assertExists()
        assertNotNull(sessionsFlow.value.singleOrNull())
    }
}
