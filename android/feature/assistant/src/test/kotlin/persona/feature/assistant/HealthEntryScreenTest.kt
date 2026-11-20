package persona.feature.assistant

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import java.io.File
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertTrue
import org.robolectric.Robolectric
import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.navigationevent.compose.LocalNavigationEventDispatcherOwner
import androidx.navigationevent.compose.rememberNavigationEventDispatcherOwner
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.Flow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

/**
 * The sidebar's Health (#4329): the newest health chat opens in the assistant's chat; with none, the health space's
 * new-chat page, whose first message starts a health chat (never the main one). New on a health chat opens that page.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class HealthEntryScreenTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val main = SessionKey("mac", "main")
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    @Before fun setup() = AssistantManagerChoice.choose("hub", null, "assistant")
    @After fun cleanup() = AssistantManagerChoice.reset()

    private inner class Hub(val health: List<Session>, val records: HealthSpace = Records()) : AssistantConversations, MessageSender, MainConversationSender {
        override val assistant = MutableStateFlow(AssistantSnapshot(mainSession = main))
        override val pinned = MutableStateFlow(emptySet<SessionKey>())
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        val listed = mutableListOf<String>()
        val spaceStarts = mutableListOf<String>()
        val starts = mutableListOf<SessionKey>()
        val targets = mutableListOf<SessionKey>()
        val toMain = mutableListOf<String>()
        val embeddedHealth = mutableMapOf<SessionKey, Boolean>()
        var historyWait: CompletableDeferred<Unit>? = null
        override suspend fun healthSpace() = records
        override suspend fun refresh() = Unit
        override suspend fun startConversation(): SessionKey = SessionKey("mac", "new-${starts.size + 1}").also { starts += it }
        override suspend fun spaceConversations(space: String): List<Session> { listed += space; historyWait?.await(); return health }
        override suspend fun startConversationIn(space: String): SessionKey {
            spaceStarts += space
            return SessionKey("mac", "h-${spaceStarts.size}")
        }
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) { toMain += text }
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) { targets += session }
    }

    private open class Records : HealthSpace {
        override val machineId = "fixture-health-owner"
        val checkup = CheckupReport("sample.pdf", "Sample checkup", "/private/sample.pdf", "/private/sample.md")
        val reports = mutableListOf(checkup)
        val saved = mutableListOf<String>()
        val notes = mutableListOf(MemoryFile("sleep.md", "---\nname: Sample health note\n---\nExample note"))
        var refuse = false
        override suspend fun reports() = reports.toList()
        override suspend fun memory() = notes.toList()
        override suspend fun deleteReport(name: String) { reports.removeAll { it.name == name } }
        override suspend fun saveMemory(name: String, content: String) {
            if (refuse) error("Fixture refusal")
            saved += name
            notes.replaceAll { if (it.name == name) it.copy(content = content) else it }
        }
        override suspend fun deleteMemory(name: String) { notes.removeAll { it.name == name } }
        override suspend fun startReportChat(prompt: String): SessionKey = error("Reading opens no new chat")
    }
    private class Phone : PhoneHealthRecords {
        override val changes: Flow<Unit> = emptyFlow()
        val periods = mutableListOf<Int>()
        var denied = false
        val walk = HealthWorkout("fixture-id", "Walking", "2026-10-08T07:00:00Z", 1800.0, 2000.0)
        val detailReads = mutableListOf<String>()
        override suspend fun trends(days: Int): HealthTrends {
            if (denied) throw HealthReadException(HealthReadProblem.Permission)
            periods += days
            return HealthTrends(if (days == 7) listOf(HealthDay("2026-10-08", 4200.0, null, null)) else emptyList(), setOf("sleep"), emptySet())
        }
        override suspend fun workouts() = listOf(walk)
        override suspend fun workout(id: String): HealthWorkout {
            detailReads += id
            return walk.copy(averageHeartRate = 102.0, splits = listOf(HealthSplit(1, 900.0)))
        }
    }
    private val openedFiles = mutableListOf<Pair<String, String>>()
    private var settingsOpened = false
    private fun mount(hub: Hub, phone: PhoneHealthRecords? = null, online: Boolean = true): AssistantSidebarHost {
        val sidebar = AssistantSidebarHost()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                // The production shell gives its selected tab a navigation-event dispatcher.
                val shellBack = rememberNavigationEventDispatcherOwner(enabled = true)
                CompositionLocalProvider(LocalAssistantSidebarHost provides sidebar, LocalNavigationEventDispatcherOwner provides shellBack) {
                    AssistantScreen(navigator, hub, hub, hub::startConversation, composerService = null, mainConversationSender = hub,
                        hubOnline = online, phoneHealth = phone, onHealthAccess = { settingsOpened = true },
                        healthFilePreview = { machine, path, close ->
                            androidx.compose.runtime.LaunchedEffect(machine, path) { openedFiles += machine to path }
                            androidx.compose.material3.TextButton(onClick = close) { Text("Close sample preview") }
                        },
                        assistantChatContent = { key, options ->
                            hub.embeddedHealth[key] = options.healthChat
                            Text("chat:${key.sessionId}")
                        })
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("chat:main").assertExists()
        return sidebar
    }
    private fun health(sidebar: AssistantSidebarHost) {
        rule.runOnIdle { sidebar.action = AssistantSidebarAction.Health }
        rule.waitForIdle()
    }
    private fun waitFor(text: String) = rule.waitUntil(5_000) { rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty() }
    private fun send(text: String) {
        rule.onNode(hasSetTextAction()).performTextReplacement(text)
        rule.onNodeWithContentDescription("Send").performClick()
        rule.waitForIdle()
    }
    private fun chat(id: String, updated: String, archived: Boolean = false) = Session(machineId = "mac", sessionId = id,
        title = "Health $id", updatedAt = updated, assistant = true, archived = archived, space = AssistantSpace.HEALTH)

    @Test fun healthOpensTheNewestChatAndItsNewOpensTheHealthPage() {
        val hub = Hub(listOf(chat("older", "2026-11-01T08:00:00Z"), chat("newest", "2026-11-10T08:00:00Z"),
            chat("put-away", "2026-11-11T08:00:00Z", archived = true)))
        val sidebar = mount(hub)
        health(sidebar)
        waitFor("chat:newest")
        assertEquals(listOf(AssistantSpace.HEALTH), hub.listed)
        assertEquals(false, hub.embeddedHealth[main])
        assertEquals(true, hub.embeddedHealth[SessionKey("mac", "newest")])

        // New on a health chat: the health space's page, with its history; nothing is started yet.
        rule.onNodeWithContentDescription("New health chat").performClick()
        rule.waitForIdle()
        waitFor("Ask about your health")
        rule.onAllNodesWithText("chat:newest").assertCountEquals(0)
        waitFor("Health older")
        assertEquals(emptyList<String>(), hub.spaceStarts)

        send("How did I sleep?")
        waitFor("chat:h-1")
        assertEquals(true, hub.embeddedHealth[SessionKey("mac", "h-1")])
        assertEquals(listOf(AssistantSpace.HEALTH), hub.spaceStarts)
        assertEquals(listOf(SessionKey("mac", "h-1")), hub.targets)
        assertEquals("never the main chat or a plain new one", emptyList<String>(), hub.toMain)
        assertEquals(emptyList<SessionKey>(), hub.starts)
    }

    @Test fun newHealthChatOffersItsPrivateRecordsAlongsideTheComposer() {
        val hub = Hub(emptyList())
        health(mount(hub))
        waitFor("Ask about your health")
        rule.onNodeWithText("Checkup reports").assertExists()
        rule.onNodeWithText("Health trends").assertExists()
        rule.onNodeWithText("Workout reports").assertExists()
        rule.onNodeWithText("Health memory").assertExists()
        assertEquals(emptyList<String>(), hub.spaceStarts)
        assertEquals(emptyList<String>(), hub.toMain)
    }

    @Test fun reportOpensItsOwnOriginalAndSummaryAndOnlyDeletesAfterConfirmation() {
        val records = Records()
        val hub = Hub(emptyList(), records)
        health(mount(hub)); waitFor("Checkup reports")
        rule.onNodeWithText("Checkup reports").performClick()
        waitFor("Sample checkup")
        rule.onNodeWithText("Sample checkup").performClick()
        for ((action, path) in listOf("View summary" to "/private/sample.md", "Open original" to "/private/sample.pdf")) {
            rule.onNodeWithText(action).performClick(); waitFor("Close sample preview")
            assertEquals(records.machineId to path, openedFiles.last())
            rule.onNodeWithText("Close sample preview").performClick()
        }
        // The scrim fills the window behind the sheet; a tap at its centre hits sheet content.
        // Invoke its dismiss action, which Android system Back also enters.
        rule.onNode(hasContentDescription("Close") and hasAnyAncestor(isDialog())).performSemanticsAction(SemanticsActions.OnClick)
        waitFor("Sample checkup")
        rule.onNodeWithText("Open original").assertDoesNotExist()
        rule.onNodeWithText("Sample checkup").assertHasClickAction()
        rule.onNodeWithText("Sample checkup").performClick()
        rule.onNodeWithText("Delete").performClick()
        waitFor("Delete this report?")
        assertEquals(1, records.reports.size)
        rule.onAllNodesWithText("Delete").onLast().performClick()
        waitFor("No checkup reports yet")
        assertEquals(emptyList<CheckupReport>(), records.reports)
        assertEquals(emptyList<String>(), hub.spaceStarts)
    }

    @Test fun checkupUploadsPdfAndImageAsOriginalsBeforeStartingOnePrivateChat() = runBlocking {
        val files = mutableListOf<String>()
        val prompts = mutableListOf<String>()
        val expected = SessionKey("computer", "health-report")
        val owner = object : Records() {
            override suspend fun startReportChat(prompt: String): SessionKey {
                prompts += prompt
                return expected
            }
        }
        Robolectric.setupContentProvider(ReportPickerProvider::class.java, ReportPickerProvider.authority)
        var refuseImage = false
        val uploader = object : ChatComposerService {
            override val deliveries = MutableStateFlow(emptyMap<String, ComposerDelivery>())
            override suspend fun upload(session: SessionKey, name: String, file: File): String =
                error("The temporary upload route cannot be used for a report")
            override suspend fun upload(session: SessionKey, name: String, file: File, destination: String): String {
                assertEquals(owner.machineId, session.machineId)
                assertEquals(AssistantSpace.HEALTH, destination)
                assertEquals("synthetic $name", file.readText())
                if (refuseImage && name.endsWith(".png")) error("Fixture upload refusal")
                files += name
                return "/private/$name"
            }
            override suspend fun interrupt(session: SessionKey) = error("Unexpected interruption")
            override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean) = error("Unexpected delivery")
        }
        val originals = listOf("sample report.pdf", "image.png")
        val uris = originals.map { Uri.Builder().scheme("content").authority(ReportPickerProvider.authority).appendPath(it).build() }
        refuseImage = true
        assertEquals("Fixture upload refusal", runCatching { uploadReports(rule.activity, owner, uploader, uris) }.exceptionOrNull()?.message)
        assertEquals(listOf("sample report.pdf"), files)
        assertEquals(emptyList<String>(), prompts)
        files.clear()
        refuseImage = false
        assertEquals(expected, uploadReports(rule.activity, owner, uploader, uris))
        assertEquals(originals, files)
        for (name in originals) assertTrue(prompts.single().contains("/private/$name"))
        assertTrue(rule.activity.contentResolver.persistedUriPermissions.none { it.uri in uris })
    }

    /** Two synthetic documents with Android's normal picker metadata and a one-time read grant. */
    class ReportPickerProvider : ContentProvider() {
        companion object { const val authority = "example.persona.health.checkups" }
        override fun onCreate() = true
        override fun getType(uri: Uri) = if (uri.lastPathSegment?.endsWith(".png") == true) "image/png" else "application/pdf"
        override fun query(uri: Uri, projection: Array<out String>?, selection: String?,
            selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
            val name = requireNotNull(uri.lastPathSegment)
            return MatrixCursor(projection).apply {
                addRow(columnNames.map { if (it == OpenableColumns.DISPLAY_NAME) name else "synthetic $name".length })
            }
        }
        override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
            val name = requireNotNull(uri.lastPathSegment)
            require(name in setOf("sample report.pdf", "image.png") && mode == "r")
            val file = File(requireNotNull(context).cacheDir, name).apply { writeText("synthetic $name") }
            return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
        }
        override fun insert(uri: Uri, values: ContentValues?) = error("Read only")
        override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?) = error("Read only")
        override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?) = error("Read only")
    }

    @Test fun healthMemoryEditsOnlyItsOwnFileAndKeepsTextOnRefusal() {
        val records = Records()
        val hub = Hub(emptyList(), records)
        health(mount(hub)); waitFor("Health memory")
        rule.onNodeWithText("Health memory").performClick()
        waitFor("Sample health note")
        rule.onNodeWithText("Sample health note").performClick()
        rule.onNodeWithContentDescription("Edit").performClick()
        rule.onNode(hasSetTextAction() and hasAnyAncestor(isDialog())).performTextReplacement("---\nname: Sample health note\n---\nRevised sample")
        records.refuse = true
        rule.onNodeWithText("Save").performClick()
        waitFor("The change could not be completed. Try again.")
        rule.onNode(hasSetTextAction() and hasAnyAncestor(isDialog())).assertTextContains("Revised sample", substring = true)
        records.refuse = false
        rule.onNodeWithText("Save").performClick()
        waitFor("Revised sample")
        assertEquals(listOf("sleep.md"), records.saved)
        assertEquals(emptyList<String>(), hub.spaceStarts)
    }

    @Test fun privateAndOrdinaryDraftsStayApartWhileSwitchingPages() {
        val hub = Hub(emptyList())
        val sidebar = mount(hub)
        rule.runOnIdle { sidebar.action = AssistantSidebarAction.NewConversation }
        rule.onNode(hasSetTextAction()).performTextReplacement("Ordinary sample")
        health(sidebar); waitFor("Ask about your health")
        rule.onAllNodesWithText("Ordinary sample").assertCountEquals(0)
        rule.onNode(hasSetTextAction()).performTextReplacement("Private sample")
        rule.runOnIdle { sidebar.action = AssistantSidebarAction.NewConversation }
        waitFor("Ordinary sample")
        rule.onAllNodesWithText("Private sample").assertCountEquals(0)
        health(sidebar); waitFor("Private sample")
        assertEquals(emptyList<String>(), hub.toMain)
        assertEquals(emptyList<String>(), hub.spaceStarts)
    }

    @Test fun replacingThePairingWhileAnOldHealthListIsPendingCannotOpenThatChat() {
        val hub = Hub(listOf(chat("old-pair", "2026-10-11T08:00:00Z")))
        val sidebar = mount(hub)
        hub.historyWait = CompletableDeferred()
        health(sidebar)
        rule.runOnIdle { AssistantManagerChoice.choose("new-hub", null, "assistant") }
        rule.runOnIdle { hub.historyWait!!.complete(Unit) }
        rule.waitForIdle()
        rule.onAllNodesWithText("chat:old-pair").assertCountEquals(0)
        assertEquals(emptyList<String>(), hub.spaceStarts)
    }

    @Test fun trendsUseTheSelectedPeriodAndLocalGrantsAndWorkoutsLoadDetailsOnTap() {
        val phone = Phone()
        health(mount(Hub(emptyList()), phone)); waitFor("Health trends")
        rule.onNodeWithText("Health trends").performClick()
        waitFor("4,200 steps")
        rule.onNodeWithText("30 days").performClick(); waitFor("No measurements in this period")
        assertEquals(listOf(7, 30), phone.periods)
        rule.onNode(hasContentDescription("Back") and hasAnyAncestor(isDialog())).performClick()
        waitFor("Workout reports")
        rule.onNodeWithText("Workout reports").performClick()
        waitFor("Walking")
        assertEquals(emptyList<String>(), phone.detailReads)
        rule.onNodeWithText("Walking").performClick()
        waitFor("102 bpm")
        rule.onAllNodesWithText("Active energy").assertCountEquals(0)
        assertEquals(listOf("fixture-id"), phone.detailReads)
    }

    @Test fun unavailableHubKeepsPrivateRecordsOfflineAndLocalDenialOpensDeviceAccess() {
        val hub = Hub(emptyList())
        health(mount(hub, Phone().apply { denied = true }, online = false))
        waitFor("Checkup reports")
        rule.onNodeWithText("Checkup reports").performClick()
        waitFor("Your hub is offline. Reconnect to see private records and chats.")
        assertEquals(emptyList<String>(), hub.listed)
        rule.onNode(hasContentDescription("Back") and hasAnyAncestor(isDialog())).performClick()
        rule.onNodeWithText("Health trends").performClick()
        waitFor("Allow Persona to read these measurements in Health Connect.")
        rule.onNodeWithText("Manage access").performClick()
        assertEquals(true, settingsOpened)
    }

    @Test fun noHealthChatOpensTheHealthNewPage() {
        val hub = Hub(emptyList())
        val sidebar = mount(hub)
        health(sidebar)
        waitFor("Ask about your health")
        rule.onAllNodesWithText("chat:main").assertCountEquals(0)
        assertEquals(emptyList<String>(), hub.spaceStarts)
        send("Log my weight")
        waitFor("chat:h-1")
        assertEquals(true, hub.embeddedHealth[SessionKey("mac", "h-1")])
        assertEquals(listOf(AssistantSpace.HEALTH), hub.spaceStarts)
        assertEquals(emptyList<String>(), hub.toMain)
    }
}
