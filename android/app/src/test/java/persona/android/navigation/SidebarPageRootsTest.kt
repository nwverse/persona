package persona.android.navigation

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import androidx.navigation3.runtime.NavKey
import persona.core.navigation.AssistantRoute
import persona.core.navigation.ExtensionUiRoute
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

    private fun personaRootSwapped(regular: Boolean, persona: Boolean, from: NavKey?, to: NavKey?): Boolean =
        regular && persona && from != null && to != null && from != to
}
