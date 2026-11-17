package persona.android.ui

import android.app.Application
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.FocusManager
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.navigation3.runtime.NavKey
import androidx.navigation3.runtime.entryProvider
import androidx.navigation3.runtime.rememberSaveableStateHolderNavEntryDecorator
import androidx.navigation3.ui.NavDisplay
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.android.navigation.AppNavigator
import persona.android.navigation.SidebarPageRoots
import persona.android.navigation.SidebarPlaceWindow
import persona.android.navigation.SidebarRootHosts
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AgentsRoute
import persona.core.navigation.AppTab
import persona.core.navigation.AssistantRoute
import persona.core.navigation.ChatRoute
import persona.core.navigation.ConversationKind
import persona.core.navigation.InboxItemRoute
import persona.core.navigation.InboxRoute
import persona.core.navigation.RouteSource
import persona.core.navigation.TodoDetailRoute

/**
 * [SidebarRootsRuntime] is the composable [Root] calls for width changes, one display per host, and
 * the place-root Back handler. These tests go through that function, not a copy of its loop.
 */
@RunWith(AndroidJUnit4::class)
@Config(application = Application::class, sdk = [35], qualifiers = "en-w951dp-h673dp-mdpi")
class SidebarRootsRuntimeTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test
    fun selectionOnTheInboxRootConsumesBackAndTheNextBackReturnsToTheConversation() {
        val world = world()
        world.window.noteSize(951f, 673f)
        world.hosts.activate(InboxRoute, listOf(InboxRoute))
        var placeBacks = 0
        rule.setContent {
            Theme {
                Hosts(world, regular = true, onPlaceBack = {
                    placeBacks++
                    world.window.leavePlaceRoot(true, world.hosts.current, world.pageRoots, world.show)
                }) {
                    // Same handler InboxScreen registers while a row is selected (InboxScreen.kt).
                    var selecting by remember { mutableStateOf(false) }
                    BackHandler(selecting) { selecting = false }
                    Column {
                        Text(if (selecting) "Selecting" else "Inbox", Modifier.testTag("inbox-state"))
                        TextButton({ selecting = true }, Modifier.testTag("select")) { Text("Select") }
                    }
                }
            }
        }
        rule.onNodeWithTag("select").performClick()
        rule.waitForIdle()
        assertEquals("Selecting", text("inbox-state"))
        back()
        assertEquals("the page's Back handler runs before the place root", "Inbox", text("inbox-state"))
        assertEquals(0, placeBacks)
        assertTrue(placed("page-inbox"))
        back()
        assertEquals(1, placeBacks)
        assertTrue(placed("page-conversation"))
        assertFalse(placed("page-inbox"))
        assertEquals(listOf(AssistantRoute), world.hosts.current.toList())
    }

    @Test
    fun aMeasuredCompactOpenIsNotAppliedWhenTheWindowLaterUnfolds() {
        val world = world()
        world.window.noteSize(400f, 800f)
        var regular by mutableStateOf(false)
        rule.setContent {
            Theme { Hosts(world, regular) { Text("Inbox", Modifier.testTag("page-inbox-body")) } }
        }
        rule.waitForIdle()
        rule.runOnIdle {
            val opened = world.window.openPlace(InboxItemRoute("stale"), world.pageRoots, world.hosts, world.show) {}
            assertFalse(opened)
            assertNull(world.window.pending)
        }
        regular = true
        rule.waitForIdle()
        assertNull(world.hosts.stack("inbox"))
        assertEquals(listOf(AssistantRoute), world.hosts.current.toList())
        assertTrue(placed("page-conversation"))
    }

    @Test
    fun anUnmeasuredOpenIsAppliedByTheRuntimeOnTheFirstRegularFrame() {
        val world = world()
        var regular by mutableStateOf(false)
        rule.setContent {
            Theme { Hosts(world, regular) { Text("Inbox", Modifier.testTag("page-inbox-body")) } }
        }
        rule.waitForIdle()
        rule.runOnIdle {
            val opened = world.window.openPlace(InboxItemRoute("fresh"), world.pageRoots, world.hosts, world.show) {}
            assertFalse(opened)
            assertEquals(InboxItemRoute("fresh"), world.window.pending)
            world.window.noteSize(951f, 673f)
            regular = true
        }
        rule.waitForIdle()
        assertNull(world.window.pending)
        assertEquals(listOf(AssistantRoute), world.conversation.toList())
        assertEquals(listOf(InboxRoute, InboxItemRoute("fresh")), world.hosts.current.toList())
        assertTrue(placed("page-item"))
        assertFalse(placed("page-conversation"))
    }

    @Test
    fun switchingTheHostInFrontClearsFocusAndACompactPrefixDoesNot() {
        val world = world()
        val clears = mutableListOf<Boolean>()
        val focus = object : FocusManager {
            override fun clearFocus(force: Boolean) { clears += force }
            override fun moveFocus(focusDirection: FocusDirection) = false
        }
        rule.setContent {
            CompositionLocalProvider(LocalFocusManager provides focus) {
                ReleaseFocusOnSidebarHost(world.hosts.currentId())
            }
        }
        rule.waitForIdle()
        assertEquals(emptyList<Boolean>(), clears)
        rule.runOnIdle { world.hosts.activate(InboxRoute, listOf(InboxRoute)) }
        rule.waitForIdle()
        assertEquals(listOf(true), clears)
        rule.runOnIdle { world.hosts.foldOntoConversation() }
        rule.waitForIdle()
        assertEquals("a prefix under the same host is not a host switch", listOf(true), clears)
        assertEquals("inbox", world.hosts.currentId())
        rule.runOnIdle { world.hosts.activate(AssistantRoute, listOf(AssistantRoute)) }
        rule.waitForIdle()
        assertEquals(listOf(true, true), clears)
    }

    @Test
    fun foldingAndUnfoldingAFixedPlaceDoesNotRebuildItsEntry() {
        val world = world()
        world.window.noteSize(951f, 673f)
        world.hosts.activate(InboxRoute, listOf(InboxRoute))
        var regular by mutableStateOf(true)
        var births = 0
        rule.setContent {
            Theme {
                Hosts(world, regular) {
                    val born = remember { births++; births }
                    Text("inbox-$born", Modifier.testTag("inbox-birth"))
                }
            }
        }
        rule.waitForIdle()
        assertEquals("inbox-1", text("inbox-birth"))
        assertEquals(1, births)
        regular = false
        rule.waitForIdle()
        assertEquals(1, births)
        assertEquals("inbox-1", text("inbox-birth"))
        assertEquals(listOf(AssistantRoute, InboxRoute), world.hosts.current.toList())
        regular = true
        rule.waitForIdle()
        assertEquals("unfolding keeps the same Inbox entry", 1, births)
        assertEquals("inbox-1", text("inbox-birth"))
        assertEquals(listOf(InboxRoute), world.hosts.current.toList())
    }

    @Test
    fun foldingATodoRootThenUnfoldingKeepsTheInboxEntry() {
        val world = world()
        world.window.noteSize(951f, 673f)
        world.pageRoots.markOpenedAsRoot(InboxRoute)
        world.hosts.activate(InboxRoute, listOf(InboxRoute))
        val todo = TodoDetailRoute("x")
        var regular by mutableStateOf(true)
        var births = 0
        rule.setContent {
            Theme {
                Hosts(world, regular) {
                    val born = remember { births++; births }
                    Text("inbox-$born", Modifier.testTag("inbox-birth"))
                }
            }
        }
        rule.waitForIdle()
        assertEquals("inbox-1", text("inbox-birth"))
        rule.runOnIdle {
            world.pageRoots.markOpenedAsRoot(todo)
            world.hosts.activate(todo, listOf(todo))
        }
        rule.waitForIdle()
        regular = false
        rule.waitForIdle()
        assertEquals(1, births)
        assertTrue(world.hosts.visited.contains("inbox"))
        regular = true
        rule.waitForIdle()
        rule.runOnIdle { world.hosts.activate(InboxRoute, listOf(InboxRoute)) }
        rule.waitForIdle()
        assertEquals("inbox-1", text("inbox-birth"))
        assertEquals(1, births)
    }

    @Test
    fun pushingARouteAlreadyOnAnotherHostTrimsThatTop() {
        val world = world()
        world.window.noteSize(951f, 673f)
        val chat = ChatRoute("machine", "chat-x", ConversationKind.Agent)
        world.conversation.add(chat)
        world.hosts.activate(InboxRoute, listOf(InboxRoute))
        rule.setContent {
            Theme { Hosts(world, regular = true) { Text("Inbox", Modifier.testTag("page-inbox-body")) } }
        }
        rule.waitForIdle()
        assertEquals(listOf(AssistantRoute, chat), world.conversation.toList())
        rule.runOnIdle { world.hosts.current.add(chat) }
        rule.waitForIdle()
        assertEquals(listOf(InboxRoute, chat), world.hosts.current.toList())
        assertEquals(listOf(AssistantRoute), world.conversation.toList())
    }

    @Test
    fun backFromAgentsShowsTheInboxItemThroughTheRuntime() {
        val world = world()
        world.window.noteSize(951f, 673f)
        val agents = mutableStateListOf<NavKey>(AgentsRoute)
        var selected = AppTab.Agents
        val stacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to world.hosts.current,
            AppTab.Agents to agents,
        )
        val navigator = AppNavigator(
            stacks, { selected }, { selected = it },
            openPlace = { key ->
                world.window.openPlace(key, world.pageRoots, world.hosts, { root, next ->
                    world.hosts.activate(root, next)
                    stacks[AppTab.Persona] = world.hosts.current
                }) { selected = AppTab.Persona }
            },
            leavePlaceRoot = {
                world.window.leavePlaceRoot(selected == AppTab.Persona, stacks.getValue(AppTab.Persona), world.pageRoots, world.show)
            },
        )
        agents.add(ChatRoute("machine", "agent", ConversationKind.Agent, RouteSource.InboxItem("item-1")))
        rule.setContent {
            Theme { Hosts(world, regular = true) { Text("Inbox", Modifier.testTag("page-inbox-body")) } }
        }
        rule.waitForIdle()
        assertTrue(placed("page-conversation"))
        rule.runOnIdle { navigator.back() }
        rule.waitForIdle()
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute), world.conversation.toList())
        assertEquals(listOf(InboxRoute, InboxItemRoute("item-1")), world.hosts.current.toList())
        assertTrue(placed("page-item"))
        assertFalse(placed("page-conversation"))
    }

    private fun back() {
        rule.runOnIdle { rule.activity.onBackPressedDispatcher.onBackPressed() }
        rule.waitForIdle()
    }

    private fun text(tag: String) = rule.onNodeWithTag(tag).fetchSemanticsNode()
        .config[SemanticsProperties.Text].joinToString("") { it.text }

    private fun placed(tag: String) = rule.onNodeWithTag(tag).fetchSemanticsNode().layoutInfo.isPlaced

    private fun world() = World()
}

private class World {
    val conversation = mutableStateListOf<NavKey>(AssistantRoute)
    val hosts = SidebarRootHosts(conversation)
    val pageRoots = SidebarPageRoots()
    val window = SidebarPlaceWindow()
    val show: (NavKey, List<NavKey>) -> Unit = { root, next -> hosts.activate(root, next) }
}

@Composable
private fun Theme(content: @Composable () -> Unit) {
    PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = true, content = content)
}

@Composable
private fun Hosts(
    world: World,
    regular: Boolean,
    onPlaceBack: () -> Unit = {},
    inbox: @Composable () -> Unit,
) {
    SidebarRootsRuntime(
        window = world.window,
        hosts = world.hosts,
        pageRoots = world.pageRoots,
        assistantStack = world.conversation,
        regular = regular,
        overlayOpen = false,
        shown = true,
        showHost = world.show,
        assignPersona = {},
        onBack = onPlaceBack,
    ) { _, stack, hostShown ->
        NavDisplay(
            backStack = stack,
            modifier = Modifier.fillMaxSize().then(if (hostShown) Modifier else Modifier.unplacedForRuntimeTest()),
            onBack = {},
            entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator()),
            entryProvider = entryProvider {
                entry<AssistantRoute> {
                    Text("Conversation", Modifier.testTag("page-conversation").fillMaxSize())
                }
                entry<InboxRoute> {
                    Column(Modifier.testTag("page-inbox").fillMaxSize()) { inbox() }
                }
                entry<InboxItemRoute> { route ->
                    Text(route.itemId, Modifier.testTag("page-item").fillMaxSize())
                }
                entry<TodoDetailRoute> { Text("To-do", Modifier.fillMaxSize()) }
                entry<ChatRoute> { Text("Chat", Modifier.fillMaxSize()) }
            },
        )
    }
}

private fun Modifier.unplacedForRuntimeTest(): Modifier = layout { measurable, constraints ->
    val placeable = measurable.measure(constraints)
    layout(placeable.width, placeable.height) {}
}
