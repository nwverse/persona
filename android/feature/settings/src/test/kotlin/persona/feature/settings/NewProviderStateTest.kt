package persona.feature.settings

import android.content.Context
import android.content.res.Resources
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

private class FakeProviders(var presets: List<ProviderPreset> = emptyList()) : ProviderSetup {
    val added = mutableListOf<NewProvider>()
    var presetsFail: Exception? = null
    var refuse: Exception? = null
    override suspend fun presets(): List<ProviderPreset> { presetsFail?.let { throw it }; return presets }
    override suspend fun add(provider: NewProvider) { added += provider; refuse?.let { throw it } }
}

/** "New model provider" (docs/integrations/launchers.md "Adding from extensions"): what is sent from each form, the name check, a refusal, older hubs. */
// Robolectric: the words come from the app's resources, in Simplified Chinese (robolectric.properties).
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class NewProviderStateTest {
    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources

    private val team = ProviderPreset("example-gateway/team", "example-gateway", "示例网关 · 团队", description = "团队共用",
        baseUrl = "https://gateway.example.com", model = "example-pro", models = listOf(PresetModel("example-mini", "Example Mini")), keyHint = "以 eg- 开头")
    private val api = ProviderPreset("example-gateway/api", "example-gateway", "示例 API", kind = "api", api = "responses",
        baseUrl = "https://api.example.com/v1", models = listOf(PresetModel("m1"), PresetModel("m2")), modelsError = "超时")

    @Test fun presetsArePickedAndOnlyTheNameTheKeyThePresetAndAnotherModelAreSent() = runBlocking {
        val hub = FakeProviders(listOf(team, api))
        val model = NewProviderModel(hub)
        model.load(preferPreset = true)
        var state = model.state.value
        assertTrue(state.loaded && state.usesPreset)
        assertEquals("example-gateway/team", state.selected?.id)
        assertEquals("example-pro", state.draft.presetModel)
        assertEquals("Claude Code 启动器 · gateway.example.com", presetSubtitle(res, team))
        assertEquals("直接 API · api.example.com", presetSubtitle(res, api))
        assertFalse(state.canSave)

        model.edit(state.draft.copy(name = "9team", apiKey = " eg-secret "))
        assertNotNull(model.state.value.nameProblem)
        assertFalse(model.state.value.canSave)
        model.edit(model.state.value.draft.copy(name = " jv_team "))
        assertTrue(model.state.value.canSave)
        assertEquals("launcher.jv_team", model.save())
        assertEquals(NewProvider("jv_team", "eg-secret", preset = "example-gateway/team"), hub.added.last())

        // Another preset: its default model; another of its models goes along.
        model.pick("example-gateway/api")
        state = model.state.value
        assertEquals("m1", state.draft.presetModel)
        model.edit(state.draft.copy(presetModel = "m2"))
        model.save()
        assertEquals(NewProvider("jv_team", "eg-secret", preset = "example-gateway/api", model = "m2"), hub.added.last())
    }

    @Test fun typedInSendsTheKindsFields() = runBlocking {
        val hub = FakeProviders()
        val model = NewProviderModel(hub)
        model.load(preferPreset = true)
        // No presets (or an older hub): only "Fill in".
        assertFalse(model.state.value.usesPreset)
        model.edit(NewProviderDraft(name = "jv_strong", apiKey = "k", baseUrl = " https://gateway.example.com ", model = "opus",
            smallModel = "haiku", subagentModel = "", args = " --verbose  -x "))
        model.save()
        assertEquals(NewProvider("jv_strong", "k", kind = "launcher", api = "messages", baseUrl = "https://gateway.example.com", model = "opus",
            smallModel = "haiku", args = listOf("--verbose", "-x")), hub.added.last())

        // A direct API takes no small model, sub-agent model or flags.
        model.edit(model.state.value.draft.copy(kind = "api", api = "responses", subagentModel = "s"))
        model.save()
        assertEquals(NewProvider("jv_strong", "k", kind = "api", api = "responses", baseUrl = "https://gateway.example.com", model = "opus"), hub.added.last())

        model.edit(model.state.value.draft.copy(baseUrl = ""))
        assertFalse(model.state.value.canSave)
    }

    @Test fun namesAreCheckedLikeTheHub() {
        assertNull(providerNameProblem(""))
        assertNull(providerNameProblem("jv_team-2"))
        assertNotNull(providerNameProblem("9x"))
        assertNotNull(providerNameProblem("a b"))
        assertEquals("不能叫 claude", providerNameProblem("claude")?.resolve(res))
    }

    @Test fun aRefusalStaysOnThePageAndPresetsFailingLeaveTypingIn() = runBlocking {
        val hub = FakeProviders(listOf(team)).apply { presetsFail = HubFailure(HubError.Server, "offline", 503) }
        val model = NewProviderModel(hub)
        model.load(preferPreset = true)
        assertTrue(model.state.value.loaded)
        assertTrue(model.state.value.presets.isEmpty())

        hub.presetsFail = null
        model.load(preferPreset = true)
        hub.refuse = HubFailure(HubError.Server, "没有这个供应商预设（提供它的扩展停用或卸载了？）", 400)
        model.edit(model.state.value.draft.copy(name = "jv_team", apiKey = "k"))
        assertNull(model.save())
        val state = model.state.value
        assertFalse(state.busy)
        assertTrue(state.error?.resolve(res).orEmpty().contains("没有这个供应商预设"))
        assertEquals("jv_team", state.draft.name)
    }

    @Test fun aProviderAddedFromAUseIsPickedForIt() = runBlocking {
        var providers = listOf(ModelProvider("launcher.claude", "claude"))
        val purpose = ModelPurpose("agent.new", "新建 Agent", effective = ModelBinding("launcher.claude"), fields = listOf("provider", "model"))
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(listOf(purpose), providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purpose, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave = throw IllegalStateException()
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        }
        val model = ModelPurposeModel(api, "agent.new")
        model.load()
        providers = providers + ModelProvider("launcher.jv_team", "jv_team")
        model.providerAdded("launcher.jv_team")
        assertEquals("launcher.jv_team", model.state.value.draft.provider)
        assertEquals(2, model.state.value.detail?.providers?.size)
        // One this use cannot take (another kind) is not picked.
        providers = providers + ModelProvider("launcher.fast", "fast", kind = "api")
        model.providerAdded("launcher.fast")
        assertEquals("launcher.jv_team", model.state.value.draft.provider)
    }
}
