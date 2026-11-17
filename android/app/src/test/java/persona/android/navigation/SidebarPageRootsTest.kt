package persona.android.navigation

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import androidx.navigation3.runtime.NavKey
import androidx.compose.runtime.mutableStateListOf
import persona.core.navigation.AgentsRoute
import persona.core.navigation.AppTab
import persona.core.navigation.AssistantRoute
import persona.core.navigation.ExtensionUiRoute
import persona.core.navigation.InboxItemRoute
import persona.core.navigation.InboxRoute
import persona.core.navigation.MachineDetailRoute
import persona.core.navigation.MachinesRoute
import persona.core.navigation.TodoDetailRoute
import persona.core.navigation.TodosRoute
import persona.feature.assistant.AssistantSidebarShade
import persona.feature.knowledge.KnowledgeRoute

/** Mirrors apple/PersonaTests/SidebarPageRootsTests.swift (#9667, iOS #8359). */
class SidebarPageRootsTest {
    private val inbox = InboxRoute
    private val recordings = KnowledgeRoute
    private val agent = TodoDetailRoute("a1")

    private fun path(vararg ids: String) = ids.map { MachineDetailRoute(it) }

    @Test
    fun fromTheConversationAPageOrAnAgentReplacesTheRootWithoutAPush() {
        for (root in listOf(recordings, agent)) {
            val roots = SidebarPageRoots()
            val step = roots.select(root, emptyList())
            assertEquals(SidebarPageRoots.Move.SwitchRoot, step.move)
            assertFalse(step.animated)
            assertTrue(step.path.isEmpty())
            assertEquals(root, step.root)
            assertEquals(root, roots.current)
        }
    }

    @Test
    fun backToTheConversationIsASwitchNotAPop() {
        val roots = SidebarPageRoots()
        roots.select(inbox, emptyList())
        val step = roots.select(AssistantRoute, emptyList())
        assertEquals(SidebarPageRoots.Move.SwitchRoot, step.move)
        assertFalse(step.animated)
        assertEquals(AssistantRoute, step.root)
    }

    @Test
    fun drillingDownPushesWithAnimation() {
        val roots = SidebarPageRoots()
        roots.select(inbox, emptyList())
        val step = roots.drill(MachineDetailRoute("x"), emptyList())
        assertEquals(SidebarPageRoots.Move.DrillDown, step.move)
        assertTrue(step.animated)
        assertFalse(step.closesOverlay)
        assertEquals(inbox, step.root)
        assertEquals(path("x"), step.path)
    }

    @Test
    fun eachRootKeepsItsDrillDown() {
        val roots = SidebarPageRoots()
        roots.select(inbox, emptyList())
        roots.select(agent, path("item"))
        assertTrue(roots.keeps(inbox))
        roots.select(AssistantRoute, path("sub"))
        assertTrue(roots.keeps(agent))
        val back = roots.select(inbox, emptyList())
        assertEquals(SidebarPageRoots.Move.SwitchRoot, back.move)
        assertFalse(back.animated)
        assertEquals(path("item"), back.path)
        assertFalse(roots.keeps(inbox))
        assertEquals(path("sub"), roots.select(agent, emptyList()).path)
    }

    @Test
    fun theRootInFrontPickedAgainGoesToItsRoot() {
        val roots = SidebarPageRoots()
        roots.select(inbox, emptyList())
        val pop = roots.select(inbox, path("item"))
        assertEquals(SidebarPageRoots.Move.PopToRoot, pop.move)
        assertTrue(pop.animated)
        assertTrue(pop.path.isEmpty())
        assertTrue(pop.closesOverlay)
        val none = roots.select(inbox, emptyList())
        assertEquals(SidebarPageRoots.Move.None, none.move)
        assertFalse(none.animated)
        assertTrue(none.closesOverlay)
    }

    @Test
    fun anotherConversationStartsAtItsRootWithoutAPop() {
        val roots = SidebarPageRoots()
        val step = roots.select(AssistantRoute, path("sub"), contentChanged = true)
        assertEquals(SidebarPageRoots.Move.SwitchRoot, step.move)
        assertFalse(step.animated)
        assertTrue(step.path.isEmpty())
        assertFalse(roots.keeps(AssistantRoute))
    }

    @Test
    fun anotherAssistantForgetsEveryRoot() {
        val roots = SidebarPageRoots()
        roots.select(inbox, emptyList())
        roots.select(agent, path("a"))
        val step = roots.forget()
        assertEquals(AssistantRoute, step.root)
        assertTrue(step.path.isEmpty())
        assertFalse(step.animated)
        assertEquals(AssistantRoute, roots.current)
        assertFalse(roots.keeps(inbox))
    }

    @Test
    fun theShadedPlaceFollowsTheRoot() {
        assertEquals(AssistantSidebarShade.Inbox, sidebarShadeKey(inbox))
        assertEquals(AssistantSidebarShade.Knowledge, sidebarShadeKey(recordings))
        assertEquals(AssistantSidebarShade.None, sidebarShadeKey(agent))
        assertEquals(AssistantSidebarShade.Conversation, sidebarShadeKey(AssistantRoute))
        assertEquals(AssistantSidebarShade.Todos, sidebarShadeKey(TodosRoute))
        assertEquals(AssistantSidebarShade.Machines, sidebarShadeKey(MachinesRoute))
        assertEquals("extension:ext/page", sidebarShadeKey(ExtensionUiRoute("ext", "page", "Page")))
        assertEquals(AssistantSidebarShade.None, sidebarShadeKey(ExtensionUiRoute("ext", "page", "Page", depth = 1)))
    }

    @Test
    fun widthRewritesOnlyTheCompactPrefix() {
        val drilled = listOf(InboxRoute, MachineDetailRoute("x"))
        assertEquals(listOf(AssistantRoute) + drilled, compactPrefix(drilled))
        assertNull(compactPrefix(listOf(AssistantRoute, InboxRoute)))
        assertNull(compactPrefix(listOf(AssistantRoute)))
        assertEquals(drilled, regularPromotion(listOf(AssistantRoute) + drilled))
        assertNull(regularPromotion(listOf(AssistantRoute, MachineDetailRoute("x"))))
        assertNull(regularPromotion(listOf(InboxRoute)))
    }

    @Test
    fun thePhoneDrawerStillPushesOverTheConversation() {
        val stack = mutableListOf<NavKey>(AssistantRoute, MachineDetailRoute("kept"))
        pushCompactSidebarPlace(stack, InboxRoute)
        assertEquals(listOf(AssistantRoute, InboxRoute), stack)
        pushCompactSidebarPlace(stack, TodosRoute)
        assertEquals(listOf(AssistantRoute, TodosRoute), stack)
        pushCompactSidebarPlace(stack, TodosRoute)
        assertEquals(listOf(AssistantRoute, TodosRoute), stack)
    }

    @Test
    fun replacingTheStackKeepsTheSharedPrefix() {
        val stack = mutableListOf<NavKey>(InboxRoute, MachineDetailRoute("a"), MachineDetailRoute("b"))
        replaceBackStack(stack, listOf(InboxRoute))
        assertEquals(listOf(InboxRoute), stack)
        replaceBackStack(stack, listOf(TodosRoute, TodoDetailRoute("t")))
        assertEquals(listOf(TodosRoute, TodoDetailRoute("t")), stack)
        replaceBackStack(stack, listOf(TodosRoute, TodoDetailRoute("t")))
        assertEquals(listOf(TodosRoute, TodoDetailRoute("t")), stack)
    }

    @Test
    fun aRootSwapIsOnlyARegularPersonaStack() {
        assertTrue(personaRootSwapped(regular = true, persona = true, from = InboxRoute, to = TodosRoute))
        assertFalse(personaRootSwapped(regular = false, persona = true, from = InboxRoute, to = TodosRoute))
        assertFalse(personaRootSwapped(regular = true, persona = false, from = InboxRoute, to = TodosRoute))
        assertFalse(personaRootSwapped(regular = true, persona = true, from = InboxRoute, to = InboxRoute))
        assertFalse(personaRootSwapped(regular = true, persona = true, from = null, to = InboxRoute))
    }

    @Test
    fun aPlaceRootShowsTheSidebarButton() {
        assertTrue(showsPlaceRootLeading(regular = true, persona = true, primary = false, entryIsStackRoot = true))
        assertFalse(showsPlaceRootLeading(regular = false, persona = true, primary = false, entryIsStackRoot = true))
        assertFalse(showsPlaceRootLeading(regular = true, persona = true, primary = true, entryIsStackRoot = true))
        assertFalse(showsPlaceRootLeading(regular = true, persona = true, primary = false, entryIsStackRoot = false))
        assertFalse(showsPlaceRootLeading(regular = true, persona = false, primary = false, entryIsStackRoot = true))
    }

    @Test
    fun unfoldingPromotesOnlyAPlaceOpenedAsARoot() {
        assertNull(regularPromotion(listOf(AssistantRoute, TodoDetailRoute("from-chat"))) { false })
        assertEquals(
            listOf(InboxRoute, InboxItemRoute("i")),
            regularPromotion(listOf(AssistantRoute, InboxRoute, InboxItemRoute("i"))) { it == InboxRoute },
        )
        val roots = SidebarPageRoots()
        assertFalse(roots.openedAsRoot(TodoDetailRoute("from-chat")))
        roots.markOpenedAsRoot(InboxRoute)
        assertTrue(roots.openedAsRoot(InboxRoute))
        roots.forget()
        assertFalse(roots.openedAsRoot(InboxRoute))
    }

    @Test
    fun placeRoutesSwitchAndConversationDrillsDoNot() {
        assertEquals(InboxRoute, placeRootFor(InboxItemRoute("i")))
        assertEquals(InboxRoute, placeRootFor(InboxRoute))
        assertEquals(TodosRoute, placeRootFor(TodosRoute))
        assertEquals(KnowledgeRoute, placeRootFor(KnowledgeRoute))
        assertEquals(MachinesRoute, placeRootFor(MachinesRoute))
        assertNull(placeRootFor(TodoDetailRoute("from-chat")))
        assertNull(placeRootFor(MachineDetailRoute("m")))
        assertNull(placeRootFor(ExtensionUiRoute("ext", "page", "Page")))
        assertEquals(SidebarRootHosts.CONVERSATION, sidebarHostId(AssistantRoute))
        assertEquals("inbox", sidebarHostId(InboxRoute))
        assertEquals("todo:abc", sidebarHostId(TodoDetailRoute("abc")))
        assertEquals("extension:ext/page", sidebarHostId(ExtensionUiRoute("ext", "page", "Page")))
    }

    @Test
    fun anotherRootKeepsTheConversationList() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val hosts = SidebarRootHosts(conversation)
        hosts.activate(InboxRoute, listOf(InboxRoute, InboxItemRoute("i")))
        assertTrue(hosts.stack(SidebarRootHosts.CONVERSATION) === conversation)
        assertEquals(listOf(AssistantRoute), conversation.toList())
        hosts.activate(AssistantRoute, listOf(AssistantRoute))
        assertEquals(listOf(InboxRoute, InboxItemRoute("i")), hosts.stack("inbox")!!.toList())
        hosts.activate(InboxRoute, listOf(InboxRoute, InboxItemRoute("i")))
        hosts.foldOntoConversation()
        assertEquals(listOf(AssistantRoute, InboxRoute, InboxItemRoute("i")), conversation.toList())
        assertEquals(listOf(SidebarRootHosts.CONVERSATION), hosts.visited.toList())
        assertTrue(hosts.current === conversation)
    }

    @Test
    fun aPlaceRouteAtRegularWidthDoesNotPushUnderAnotherRoot() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val hosts = SidebarRootHosts(conversation)
        val roots = SidebarPageRoots()
        hosts.activate(TodosRoute, listOf(TodosRoute))
        roots.adoptVisible(TodosRoute)
        roots.markOpenedAsRoot(TodosRoute)
        val todos = hosts.stack("todos")!!
        val stacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to hosts.current,
            AppTab.Agents to mutableStateListOf(AgentsRoute),
        )
        val navigator = AppNavigator(stacks, { AppTab.Persona }, {}, openPlace = { key ->
            val place = placeRootFor(key) ?: return@AppNavigator false
            if (roots.current == place && key != place) return@AppNavigator false
            roots.markOpenedAsRoot(place)
            val step = roots.select(place, hosts.current.drop(1))
            val next = if (key == place) step.stack else listOf(place, key)
            hosts.activate(place, next)
            stacks[AppTab.Persona] = hosts.current
            true
        }, leavePlaceRoot = {
            if (hosts.current.size != 1 || hosts.current.first() == AssistantRoute) return@AppNavigator false
            val step = roots.select(AssistantRoute, emptyList())
            hosts.activate(step.root, step.stack)
            stacks[AppTab.Persona] = hosts.current
            true
        })
        navigator.navigate(InboxItemRoute("item-1"))
        assertEquals(listOf(TodosRoute), todos.toList())
        assertEquals(listOf(InboxRoute, InboxItemRoute("item-1")), hosts.current.toList())
        assertEquals(listOf(AssistantRoute), conversation.toList())
        navigator.back()
        assertEquals(listOf(InboxRoute), hosts.current.toList())
        navigator.back()
        assertEquals(listOf(AssistantRoute), hosts.current.toList())
        assertEquals(listOf(InboxRoute), hosts.stack("inbox")!!.toList())
    }

    @Test
    fun aDrillOnThePlaceInFrontStillPushes() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val hosts = SidebarRootHosts(conversation)
        val roots = SidebarPageRoots()
        hosts.activate(InboxRoute, listOf(InboxRoute))
        roots.adoptVisible(InboxRoute)
        val stacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to hosts.current,
            AppTab.Agents to mutableStateListOf(AgentsRoute),
        )
        val navigator = AppNavigator(stacks, { AppTab.Persona }, {}, openPlace = { key ->
            val place = placeRootFor(key) ?: return@AppNavigator false
            if (roots.current == place && key != place) return@AppNavigator false
            true
        })
        navigator.navigate(InboxItemRoute("item-1"))
        assertEquals(listOf(InboxRoute, InboxItemRoute("item-1")), hosts.current.toList())
    }

    private fun personaRootSwapped(regular: Boolean, persona: Boolean, from: NavKey?, to: NavKey?): Boolean =
        regular && persona && from != null && to != null && from != to
}
