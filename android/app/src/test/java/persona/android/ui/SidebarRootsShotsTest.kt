package persona.android.ui

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.navigation3.rememberViewModelStoreNavEntryDecorator
import androidx.navigation3.runtime.NavEntry
import androidx.navigation3.runtime.NavEntryDecorator
import androidx.navigation3.runtime.NavKey
import androidx.navigation3.runtime.entryProvider
import androidx.navigation3.runtime.rememberSaveableStateHolderNavEntryDecorator
import androidx.navigation3.ui.NavDisplay
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.android.R
import persona.android.navigation.HomeLeadingAction
import persona.android.navigation.SidebarPageRoots
import persona.android.navigation.noPageMotion
import persona.android.navigation.personaSceneRootSwapped
import persona.android.navigation.replaceBackStack
import persona.android.navigation.resolveHomeLeadingAction
import persona.android.navigation.showsPlaceRootLeading
import persona.core.contracts.Session
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.designsystem.AppStyle
import persona.core.designsystem.HomeChrome
import persona.core.designsystem.JButton
import persona.core.designsystem.JPage
import persona.core.designsystem.JPersonaButton
import persona.core.designsystem.JPersonaButtonRoom
import persona.core.designsystem.JSidebarColumn
import persona.core.designsystem.LocalHomeChrome
import persona.core.designsystem.LocalRootBack
import persona.core.designsystem.LocalRootLeading
import persona.core.designsystem.PersonaState
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.rememberPageTransitions
import persona.core.navigation.AssistantRoute
import persona.core.navigation.InboxRoute
import persona.core.navigation.TodosRoute
import persona.feature.agents.R as AgentsR
import persona.feature.assistant.AssistantSidebarContent
import persona.feature.inbox.MailRoute
import persona.feature.inbox.R as InboxR
import java.io.File

/**
 * Regular-width sidebar roots (#9667). Pages are the real chrome (`JListPage` / `JPage`, `JSidebarColumn`,
 * `NavDisplay`) with fixture bodies. Inbox → To-dos has no page transition (iOS `UnifiedSidebar.apply`,
 * UnifiedSidebar.swift:67–74). A drill-down still pushes.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35])
class SidebarRootsShotsTest {
    @get:Rule val rule = createEmptyComposeRule()

    @After fun resetAssistant() { AssistantManagerChoice.reset() }

    @Test
    @Config(qualifiers = "en-w673dp-h951dp-mdpi")
    fun uprightOverlay() = exercise("673x951", overlay = true)

    @Test
    @Config(qualifiers = "en-w951dp-h673dp-mdpi")
    fun landscapeBeside() = exercise("951x673", overlay = false)

    private fun exercise(size: String, overlay: Boolean) {
        AssistantManagerChoice.choose("hub", null, "assistant")
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        val main = Session("computer", "assistant", title = "Main conversation", assistant = true)
        val snapshot = AssistantSnapshot(mainSession = main.key, conversations = listOf(main))
        val repository = object : AssistantRepository {
            override val assistant = MutableStateFlow(snapshot)
            override suspend fun refresh() = Unit
        }
        var dark by mutableStateOf(false)
        val columnShown = mutableStateOf(true)
        val overlayShown = mutableStateOf(overlay)
        val stack = mutableStateListOf<NavKey>(AssistantRoute)
        val roots = SidebarPageRoots()
        fun pick(key: NavKey) {
            overlayShown.value = false
            replaceBackStack(stack, roots.select(key, stack.drop(1)).stack)
        }
        fun reset() {
            roots.forget()
            replaceBackStack(stack, listOf(AssistantRoute))
            overlayShown.value = overlay
            columnShown.value = true
        }
        rule.mainClock.autoAdvance = false
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                activity.setContent {
                    PersonaTheme(style = AppStyle.Glass, darkTheme = dark, reduceMotion = false) {
                        SidebarRootsHarness(snapshot, repository, stack, roots, overlay, columnShown, overlayShown, ::pick)
                    }
                }
            }
            for (isDark in listOf(false, true)) {
                rule.runOnIdle { dark = isDark; reset() }
                val tone = if (isDark) "dark" else "light"
                settle()
                reveal(overlay)
                rule.onNode(hasText("Inbox") and hasClickAction()).performClick()
                settle(1_000)
                rule.onAllNodesWithContentDescription("Back").assertCountEquals(0)
                if (overlay) rule.onNodeWithContentDescription("Sidebar", useUnmergedTree = true).assertExists()
                else rule.onAllNodesWithContentDescription("Sidebar").assertCountEquals(0)
                val inboxBefore = shot("page-inbox")
                save(inboxBefore, "$size-inbox-rest-before-glass-$tone")

                rule.onNode(hasText("Open the item") and hasClickAction()).performClick()
                advance(80)
                val pushMid = shot("window")
                save(pushMid, "$size-item-push-mid-glass-$tone")
                settle(1_000)
                val pushEnd = shot("window")
                save(pushEnd, "$size-item-push-end-glass-$tone")
                assertTrue("$size $tone item push mid frame differs", moved(maskSidebarHeader(pushMid), maskSidebarHeader(pushEnd)))
                rule.onNodeWithContentDescription("Back").assertExists()
                rule.onNodeWithTag("page-item").assertExists()
                if (overlay) {
                    // The column already slid away on the place pick. A sub-page has Back alone, so the sidebar is
                    // reached from a root (SidebarRootsUITests.swift:257–262). Beside (below) keeps the item across a switch.
                    rule.onAllNodesWithContentDescription("Sidebar", useUnmergedTree = true).assertCountEquals(0)
                    rule.onNodeWithContentDescription("Back").performClick()
                    settle(1_000)
                    rule.onNodeWithTag("page-inbox").assertExists()
                    rule.onAllNodesWithContentDescription("Back").assertCountEquals(0)
                } else {
                    rule.onNode(hasText("To-dos") and hasClickAction()).performClick()
                    settle(1_000)
                    rule.onNodeWithTag("page-todos").assertExists()
                    rule.onNode(hasText("Inbox") and hasClickAction()).performClick()
                    settle(1_000)
                    rule.onNodeWithTag("page-item").assertExists()
                    rule.onNode(hasText("Inbox") and hasClickAction()).performClick()
                    advance(80)
                    val popMid = shot("window")
                    save(popMid, "$size-inbox-pop-mid-glass-$tone")
                    settle(1_000)
                    val popEnd = shot("window")
                    save(popEnd, "$size-inbox-pop-end-glass-$tone")
                    assertTrue("$size $tone re-pick pop mid frame differs", moved(maskSidebarHeader(popMid), maskSidebarHeader(popEnd)))
                    rule.onNodeWithTag("page-inbox").assertExists()
                    rule.onAllNodesWithContentDescription("Back").assertCountEquals(0)
                }
                val inboxAfter = shot("page-inbox")
                save(inboxAfter, "$size-inbox-rest-after-glass-$tone")
                assertTrue("$size $tone resting page matches aside from the leading button",
                    matches(maskLeading(inboxBefore), maskLeading(inboxAfter)))

                reveal(overlay)
                val todosBefore = shot("window")
                rule.onNode(hasText("To-dos") and hasClickAction()).performClick()
                advance(80)
                val swapMidPage = shot("page-todos")
                val swapMid = shot("window")
                save(swapMid, "$size-inbox-todos-mid-glass-$tone")
                settle(1_000)
                val swapEndPage = shot("page-todos")
                val swapEnd = shot("window")
                save(swapEnd, "$size-inbox-todos-end-glass-$tone")
                // The idle face blinks (PersonaFace); that is not a page transition. Mask it and compare the page.
                // Overlay: the page is already the new root. Only the column slides away, and its scrim dims the
                // page (SplitNavigation.swift hideOverlay; Theme.Motion.layout). Mask the 320dp column. The scrim
                // shifts channels by about 20; a real push still differs by 255.
                val pageMid = if (overlay) mask(swapMidPage, 320, swapMidPage.height) else maskLeading(swapMidPage)
                val pageEnd = if (overlay) mask(swapEndPage, 320, swapEndPage.height) else maskLeading(swapEndPage)
                assertTrue("$size $tone Inbox → To-dos page mid frame equals the end frame",
                    matches(pageMid, pageEnd, tolerance = if (overlay) 24 else 8))
                // The column stays. Its row shade can move; the page beside it must not (x past the 320dp column).
                if (!overlay) assertTrue("$size $tone beside-column mid frame equals the end frame",
                    matches(pageBesideColumn(swapMid), pageBesideColumn(swapEnd)))
                else {
                    assertTrue("$size $tone overlay column moved off the frame before the tap", moved(swapMid, todosBefore))
                    assertTrue("$size $tone overlay column is still moving at the mid frame", moved(swapMid, swapEnd))
                }
                rule.onAllNodesWithContentDescription("Back").assertCountEquals(0)

                scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
                settle()
                rule.onNodeWithTag("page-todos").assertExists()
                rule.onNodeWithTag("page-inbox").assertDoesNotExist()
                rule.onNodeWithTag("page-assistant").assertDoesNotExist()
            }
        }
    }

    private fun reveal(overlay: Boolean) {
        if (!overlay) return
        // Merged: the row's text and its click are one node. Unmerged splits them, so the column looks closed.
        val visible = rule.onAllNodes(hasText("To-dos") and hasClickAction()).fetchSemanticsNodes()
        if (visible.isEmpty()) {
            // The face's semantics sit under the button; the merged node can hide the description.
            rule.onNodeWithContentDescription("Sidebar", useUnmergedTree = true).performClick()
            settle()
        }
    }

    private fun settle(ms: Long = 32) {
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(ms)
        repeat(2) { rule.mainClock.advanceTimeByFrame() }
    }

    private fun advance(ms: Long) {
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(ms)
    }

    private fun shot(tag: String): Bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()

    private fun save(bitmap: Bitmap, name: String) {
        val out = File("build/sidebar-roots").also { it.mkdirs() }
        File(out, "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    /** The leading control is the only pixel a root may change between two resting visits. */
    private fun maskLeading(bitmap: Bitmap): Bitmap = mask(bitmap, 160, 180)

    /** The sidebar header's face blinks while Reduce Motion is off. The page beside a 320dp column starts further right. */
    private fun maskSidebarHeader(bitmap: Bitmap): Bitmap = mask(bitmap, 320, 220)

    /** The page to the right of a beside column. Drops the column, whose shaded row may move without a page transition. */
    private fun pageBesideColumn(bitmap: Bitmap): Bitmap {
        val start = minOf(320, bitmap.width)
        return Bitmap.createBitmap(bitmap, start, 0, bitmap.width - start, bitmap.height)
    }

    /**
     * Glass blurs the same resting page by a couple of channel values. A real push moves regions by much more
     * (the 951 light push mid-frame differs by 255).
     */
    private fun matches(a: Bitmap, b: Bitmap, tolerance: Int = 8): Boolean {
        if (a.width != b.width || a.height != b.height) return false
        for (y in 0 until a.height) for (x in 0 until a.width) {
            if (channelDelta(a.getPixel(x, y), b.getPixel(x, y)) > tolerance) return false
        }
        return true
    }

    private fun moved(a: Bitmap, b: Bitmap): Boolean = !matches(a, b)

    private fun channelDelta(a: Int, b: Int): Int {
        var delta = 0
        for (shift in intArrayOf(0, 8, 16, 24)) {
            val part = kotlin.math.abs(((a shr shift) and 0xFF) - ((b shr shift) and 0xFF))
            if (part > delta) delta = part
        }
        return delta
    }

    private fun mask(bitmap: Bitmap, width: Int, height: Int): Bitmap {
        val copy = bitmap.copy(Bitmap.Config.ARGB_8888, true)
        val w = minOf(width, copy.width)
        val h = minOf(height, copy.height)
        for (y in 0 until h) for (x in 0 until w) copy.setPixel(x, y, 0)
        return copy
    }
}

@Composable
private fun SidebarRootsHarness(
    snapshot: AssistantSnapshot,
    repository: AssistantRepository,
    stack: SnapshotStateList<NavKey>,
    roots: SidebarPageRoots,
    overlay: Boolean,
    columnShown: androidx.compose.runtime.MutableState<Boolean>,
    overlayShown: androidx.compose.runtime.MutableState<Boolean>,
    pick: (NavKey) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val rootKeys = remember { listOf(AssistantRoute).map { NavEntry<NavKey>(it) {}.contentKey }.toSet() }
    val sidebarLabel = stringResource(R.string.app_sidebar)
    val chrome = NavEntryDecorator<NavKey> { entry ->
        val primary = entry.contentKey in rootKeys
        val stackRoot = stack.firstOrNull()?.let { NavEntry<NavKey>(it) {}.contentKey }
        val placeRoot = showsPlaceRootLeading(regular = true, persona = true, primary = primary,
            entryIsStackRoot = stackRoot != null && entry.contentKey == stackRoot)
        val leading: @Composable () -> Unit = {
            val action = resolveHomeLeadingAction(assistantRoot = true, regular = true, columnsOver = overlay,
                sidebarColumnShown = columnShown.value)
            when (action) {
                HomeLeadingAction.HiddenSpacer -> JPersonaButtonRoom()
                else -> JPersonaButton(PersonaState.Idle, sidebarLabel, {
                    if (action == HomeLeadingAction.ToggleOverlay) overlayShown.value = !overlayShown.value
                    else columnShown.value = true
                })
            }
        }
        androidx.compose.runtime.CompositionLocalProvider(
            LocalRootLeading provides if (placeRoot) leading else null,
            LocalRootBack provides if (primary || placeRoot) null else ({ if (stack.size > 1) stack.removeAt(stack.lastIndex) }),
            LocalHomeChrome provides if (primary) HomeChrome(emptyList(), "Persona", {}, leading = leading) else null,
        ) { entry.Content() }
    }
    val transitions = rememberPageTransitions()
    val shown = if (overlay) overlayShown.value else columnShown.value
    Box(Modifier.fillMaxSize().testTag("window")) {
        JSidebarColumn(
            sidebar = {
                AssistantSidebarContent(snapshot, repository, snapshot.mainSession, scope, demo = true,
                    onOpen = { _ -> }, onAction = { _ -> }, onSettings = {},
                    onInbox = { pick(InboxRoute) }, onKnowledge = {}, onTodos = { pick(TodosRoute) },
                    todoCount = 0, showTodos = true, inboxCount = 0, settingsCount = 0, settingsDot = false,
                    settingsBadge = null, pages = emptyList(), onPage = { _ -> }, onRetired = { _ -> },
                    shadedPlace = stack.firstOrNull()?.let { persona.android.navigation.sidebarShadeKey(it) })
            },
            width = if (overlay) persona.android.navigation.PaneSplits.DefaultLeft.dp else 320.dp,
            pageStart = if (overlay) 0.dp else 320.dp,
            shown = shown, overlay = overlay, onDismiss = { overlayShown.value = false },
        ) {
            NavDisplay(
                backStack = stack,
                onBack = { if (stack.size > 1) stack.removeAt(stack.lastIndex) },
                entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator(), rememberViewModelStoreNavEntryDecorator(), chrome),
                transitionSpec = {
                    if (personaSceneRootSwapped(true, true, initialState, targetState)) noPageMotion() else transitions.push
                },
                popTransitionSpec = {
                    if (personaSceneRootSwapped(true, true, initialState, targetState)) noPageMotion() else transitions.pop
                },
                predictivePopTransitionSpec = { transitions.predictivePop },
                entryProvider = entryProvider {
                    entry<AssistantRoute> { Box(Modifier.fillMaxSize().testTag("page-assistant")) { JPage("Assistant") {} } }
                    entry<InboxRoute> {
                        Box(Modifier.fillMaxSize().testTag("page-inbox")) {
                            JPage(stringResource(InboxR.string.inbox_title)) { padding ->
                                Box(Modifier.padding(padding)) {
                                    JButton("Open the item", { stack.add(MailRoute("item")) })
                                }
                            }
                        }
                    }
                    entry<TodosRoute> {
                        Box(Modifier.fillMaxSize().testTag("page-todos")) { JPage(stringResource(AgentsR.string.agents_todos_title)) {} }
                    }
                    entry<MailRoute> {
                        Box(Modifier.fillMaxSize().testTag("page-item")) { JPage("Opened item") {} }
                    }
                },
            )
        }
    }
}
