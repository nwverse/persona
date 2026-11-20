package persona.feature.knowledge

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.io.File
import java.io.IOException
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.HubScope
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

@OptIn(ExperimentalTestApi::class)
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h950dp-xhdpi")
class ProjectEvidenceScreenTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val coverage = """{"timeline_end":120,"needs_review":true,"fields":{
        "summary":{"needs_review":true,"review_reason":"new_evidence","cutoff":{"timeline_from":0,"timeline_through":40,
        "sources":[{"ref":"chats/planning","position":"rev-1"}]},"uncovered_evidence_count":2,"writer":"ext:garden","source":"kb.update","review_by":"user","lock_reason":"field_locked"},
        "one_line":{"needs_review":false,"cutoff":{"timeline_from":0,"timeline_through":120},"writer":"persona","review_by":"persona"}}}"""
    private val sources = """[{"id":"github:example/orchard#2","url":"https://github.com/example/orchard/pull/2","refs":["chats/planning"],
        "delivery_state":"merged","delivery_at":"2026-06-02T12:00:00Z","environment_verification":"unverified"},
        {"id":"https://example.org/orchard","url":"https://example.org/orchard"},{"id":"projects/orchard/log","refs":["projects/orchard/log"]}]"""
    private val rawEntry = """{"kind":"projects","id":"orchard","name":"Orchard plan","one_line":"All deliveries received","freshness":$coverage,"sources":$sources}"""
    private val answer = """{"kind":"projects","id":"orchard","path":"data/projects/orchard",
        "summary":"Two deliveries pending","log":["2026-06-01 Ordered the second batch"],
        "meta":{"name":"Orchard plan","one_line":"All deliveries received","source_urls":["https://example.org/orchard"]},
        "links":[{"kind":"chats","id":"planning","name":"Planning room"}],"freshness":$coverage,"sources":$sources}"""
    private var sample = JsonCodec.decodeFromString<KnowledgeDetail>(answer)
    private var entries = listOf(JsonCodec.decodeFromString<KnowledgeEntry>(rawEntry))
    private val updates = mutableListOf<KnowledgeUpdate>()
    private var refusal: String? = null
    private val repository = object : KnowledgeRepository {
        override val scope = HubScope("sample", "phone")
        override suspend fun list(kind: KnowledgeKind, tag: String?) = KnowledgeList(entries)
        override suspend fun search(query: String) = emptyList<KnowledgeHit>()
        override suspend fun detail(kind: KnowledgeKind, id: String) = sample
        override suspend fun update(kind: KnowledgeKind, id: String, update: KnowledgeUpdate) {
            updates += update
            refusal?.let { reason -> throw object : IOException(), HubCodedFailure { override val code = reason; override val status = 403 } }
            sample = sample.copy(projectMode = if (update.projectMode == "full") null else update.projectMode ?: sample.projectMode,
                meta = update.sourceUrls?.let { JsonObject(sample.meta.orEmpty() + ("source_urls" to JsonArray(it.map(::JsonPrimitive)))) } ?: sample.meta)
        }
    }
    private val connection = object : KnowledgeConnection { override val current = MutableStateFlow<KnowledgeRepository?>(repository) }
    private val opened = mutableListOf<NavKey>()
    private val nav = object : Navigator {
        override fun navigate(key: NavKey) { opened += key }
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
    private val sender = object : MainConversationSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
    }
    private fun detailModel() = KnowledgeDetailViewModel(KnowledgeKind.Projects, "orchard", connection, sender, owner = "garden-owner")
    private fun show(text: String, partial: Boolean = false): SemanticsNodeInteraction {
        rule.onNode(hasScrollToIndexAction()).performScrollToNode(hasText(text, substring = partial))
        return rule.onNodeWithText(text, substring = partial)
    }
    private fun openEvidence() { show("Sources and coverage").performClick() }
    private fun showDetail() {
        val model = detailModel()
        rule.setContent { PersonaTheme { KnowledgeDetailScreen(model, nav, openLinkInApp = {}) } }
        rule.waitUntilExactlyOneExists(hasText("Orchard plan"), 10_000)
    }
    @Test fun normalProjectListShowsTheLockedSummaryGapAlongsideTheNewerOverview() {
        val model = KnowledgeViewModel(connection)
        rule.setContent { PersonaTheme { KnowledgeScreen(model, nav) } }
        rule.waitUntilExactlyOneExists(hasText("Orchard plan"), 10_000)
        rule.onNodeWithText("All deliveries received").assertExists()
        rule.onNodeWithText("Summary needs review · 2 newer updates").assertExists()
    }

    @Test fun detailsKeepTheOriginalSummarySeparateFromItsSourcesWriterMaintainerAndLock() {
        showDetail()
        show("Summary needs review · 2 newer updates").assertExists()
        rule.onNodeWithText("Written by garden", substring = true).assertDoesNotExist()
        openEvidence()
        show("Timeline bytes 0–40 of 120", true).assertExists()
        for (part in listOf("Locked", "Written by garden", "Review by you", "Based on chats/planning", "Recorded by kb.update"))
            rule.onNodeWithText(part, substring = true).assertExists()
        rule.onNodeWithText("rev-1", substring = true).assertDoesNotExist()
        show("Source revisions").performClick()
        show("chats/planning · rev-1", true).assertExists()
        show("Source revisions").performClick()
        show("Source status: Merged", true).assertExists()
        show("Deployment requires separate verification.", true).assertExists()
        show("Planning room").performClick()
        assertEquals(listOf<NavKey>(KnowledgeDetailRoute(KnowledgeKind.Chats, "planning", "garden-owner")), opened)
        show("Two deliveries pending").assertExists()
        show("data/projects/orchard").assertExists()
    }

    @Test fun historicalUnknownsAndAnOlderHubNeverInferCoverageFromUpdatedOrAFieldLock() {
        val old = entries.single().copy(id = "library", name = "Library notes", updated = "2026-06-08T00:00:00Z", projectMode = null, freshness = null, sources = emptyList())
        entries = listOf(old, entries.single().copy(freshness = ProjectFreshness(fields = mapOf(
            "summary" to ProjectFieldFreshness(lockReason = "field_locked"), "one_line" to ProjectFieldFreshness(cutoff = ProjectCutoff(0, 0))))))
        val model = KnowledgeViewModel(connection)
        rule.setContent { PersonaTheme { KnowledgeScreen(model, nav) } }
        rule.waitUntilExactlyOneExists(hasText("Orchard plan"), 10_000)
        rule.onAllNodesWithText("Summary coverage unknown").assertCountEquals(1)
        rule.onNodeWithText("Overview coverage unknown").assertDoesNotExist()
        rule.onNodeWithText("Library notes").assertExists()
    }

    @Test fun projectEvidenceStaysReadableAcrossStylesAppearancesAndPhoneTextSizes() {
        val output = System.getenv("PERSONA_PROJECT_EVIDENCE_SHOTS")?.let { File(it).apply { mkdirs() } }
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scale by mutableFloatStateOf(1f)
        var detail by mutableStateOf(false)
        val list = KnowledgeViewModel(connection)
        val page = detailModel()
        rule.setContent {
            val device = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(device.density, scale)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.testTag("project-proof").width(400.dp).height(900.dp).background(PersonaTheme.palette.background)) {
                        if (detail) KnowledgeDetailScreen(page, nav, openLinkInApp = {}) else KnowledgeScreen(list, nav)
                    }
                }
            }
        }
        fun capture(view: String) {
            if (output == null) return
            val name = "project-$view-${style.name.lowercase()}-${if (dark) "dark" else "light"}-${if (scale > 1) "large" else "standard"}.png"
            File(output, name).outputStream().use { stream ->
                rule.onNodeWithTag("project-proof").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, stream)
            }
        }
        for (look in AppStyle.entries) for (night in listOf(false, true)) for (font in listOf(1f, 2f)) {
            rule.runOnIdle { detail = false; style = look; dark = night; scale = font }
            rule.waitUntilExactlyOneExists(hasText("Orchard plan"), 10_000)
            show("Summary needs review · 2 newer updates").assertExists()
            capture("list")
            rule.runOnIdle { detail = true }
            rule.waitUntilExactlyOneExists(hasText("Orchard plan"), 10_000)
            openEvidence()
            show("Written by garden", partial = true).assertExists()
            rule.onNode(hasScrollToIndexAction()).performScrollToKey("project-evidence")
            capture("coverage")
            show("Deployment requires separate verification.", true).assertExists()
            rule.onNode(hasScrollToIndexAction()).performScrollToKey("project-sources")
            capture("sources")
        }
    }

    @Test fun olderProjectDetailsKeepTheirSummaryWithoutOfferingStorageWrites() {
        sample = sample.copy(projectMode = null, freshness = null, sources = emptyList(), meta = JsonObject(sample.meta.orEmpty() - "source_urls"))
        showDetail()
        show("Two deliveries pending").assertExists()
        rule.onNodeWithText("Sources and coverage").assertDoesNotExist()
        rule.onNodeWithText("Edit storage").assertDoesNotExist()
        assertTrue(updates.isEmpty())
    }

    @Test fun changedEvidenceWithUnknownCountAndOldCutoffKeepsAnActionableReviewNotice() {
        val field = ProjectFieldFreshness(cutoff = ProjectCutoff(2, 8, timelineTruncated = true),
            needsReview = true, reviewReason = "summary_edited", reviewBy = "persona")
        sample = sample.copy(freshness = ProjectFreshness(needsReview = true, timelineEnd = 12, fields = mapOf("one_line" to field)))
        showDetail()
        show("Overview needs review").assertExists()
        openEvidence()
        show("Timeline bytes 2–8 of 12", true).assertExists()
        show("Earlier timeline bytes", true).assertExists()
        show("The summary changed after this overview was written.", true).assertExists()
        rule.onNodeWithText("0 uncovered", substring = true).assertDoesNotExist()
    }

    @Test fun emptyFreshnessDistinguishesAnEditableNewFullProjectFromAnOlderHub() {
        sample = sample.copy(freshness = ProjectFreshness(), sources = emptyList(), meta = JsonObject(sample.meta.orEmpty() - "source_urls"))
        showDetail(); openEvidence(); show("Edit storage").performClick()
        rule.onNodeWithTag("project-storage-policy").assertIsOff().performClick()
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 1 }
        assertEquals(KnowledgeUpdate(projectMode = "reference-only"), updates.single())
    }

    @Test fun urlEditorKeepsLocalRefsCredentialsAndOverLimitDraftsOutOfTheOwnerWrite() {
        showDetail(); openEvidence(); show("Edit storage").performClick()
        val field = rule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag("project-source-refs")))
        for (bad in listOf("chats/planning", "file:///tmp/planning", "https://user@example.org/", "https://example.org/" + "界".repeat(700),
            (1..31).joinToString("\n") { "https://example.org/$it" })) {
            field.performTextReplacement(bad)
            rule.onNodeWithText("Save").assertIsNotEnabled()
            assertTrue(updates.isEmpty())
        }
        field.performTextReplacement("https://例子.example/allowed")
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 1 }
        assertEquals(KnowledgeUpdate(sourceUrls = listOf("https://例子.example/allowed")), updates.single())
    }

    @Test fun policyOnlySavePreservesTheOriginalPointerBytes() {
        val original = JsonArray(listOf(JsonPrimitive("  https://example.org/archived  ")))
        sample = sample.copy(meta = JsonObject(sample.meta.orEmpty() + ("source_urls" to original)))
        showDetail(); openEvidence(); show("Edit storage").performClick()
        rule.onNodeWithText("Save").assertIsNotEnabled()
        rule.onNodeWithTag("project-storage-policy").performClick()
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 1 }
        assertEquals(KnowledgeUpdate(projectMode = "reference-only"), updates.single())
        assertEquals(original, sample.meta?.get("source_urls"))
    }

    @Test fun readOnlyKeepsTheExactEditAndClearingExplicitPointersNeverCapturesLinkedSources() {
        showDetail(); openEvidence(); show("Edit storage").performClick()
        val field = rule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag("project-source-refs")))
        field.assertTextContains("https://example.org/orchard")
        field.performTextClearance()
        rule.onNodeWithTag("project-storage-policy").assertIsOff().performClick()
        refusal = "kb_read_only"
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 1 }
        rule.waitUntilExactlyOneExists(hasText("read-only", substring = true), 10_000)
        field.assertExists()
        rule.onNodeWithTag("project-storage-policy").assertIsOn()
        assertEquals(KnowledgeUpdate(projectMode = "reference-only", sourceUrls = emptyList()), updates.single())
        assertEquals("full", sample.editableProjectMode)
        refusal = "forbidden"
        rule.onNodeWithText("Save").performClick()
        rule.waitUntilExactlyOneExists(hasText("This device does not have permission", substring = true), 10_000)
        assertEquals(listOf(updates.first(), updates.first()), updates)
        rule.onNodeWithTag("project-storage-policy").assertIsOn()
        refusal = null
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 3 && sample.editableProjectMode == "reference-only" }
        rule.waitUntilDoesNotExist(hasTestTag("project-source-refs"), 10_000)
        show("Edit storage").performClick()
        field.performTextReplacement("https://example.org/news\n")
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 4 }
        assertEquals(KnowledgeUpdate(sourceUrls = listOf("https://example.org/news")), updates.last())
        rule.waitUntilDoesNotExist(hasTestTag("project-source-refs"), 10_000)
        show("Edit storage").performClick()
        rule.onNodeWithTag("project-storage-policy").assertIsOn().performClick()
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil(10_000) { updates.size == 5 }
        assertEquals(KnowledgeUpdate(projectMode = "full"), updates.last())
        assertEquals(3, sample.sources.size)
        assertEquals("Two deliveries pending", sample.summary)
    }
}
