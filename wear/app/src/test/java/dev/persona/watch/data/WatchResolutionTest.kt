package dev.persona.watch.data

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class WatchResolutionTest {
    @Test fun failedOwnerBlocksControlsAndFinalResolutionEvenBeforeItsCachedFlagsChange() {
        for (kind in listOf("question", "plan", "permission", "screen")) {
            val primary = WatchInteraction("same", "m", "s", kind, "", "", canAnswer = true, canCancel = true)
            val secondary = primary.copy(groupId = "secondary")
            assertTrue(WatchResolution.canResolve(secondary, emptySet()))
            // An open confirmation may still hold this exact original object after reachability changes.
            assertFalse(WatchResolution.canResolve(secondary, setOf("secondary")))
            assertTrue(WatchResolution.canResolve(primary, setOf("secondary")))
            assertTrue(WatchResolution.canResolve(secondary, emptySet()))
        }
    }

    @Test fun reachableOwnerDoesNotOverrideInteractionReadOnlyOrProtectedState() {
        val item = WatchInteraction("q", "m", "s", "permission", "", "", canAnswer = true)
        assertFalse(WatchResolution.canResolve(item.copy(canAnswer = false), emptySet()))
        assertFalse(WatchResolution.canResolve(item.copy(muted = true), emptySet()))
        assertFalse(WatchResolution.canResolve(item.copy(delegating = true), emptySet()))
        assertFalse(WatchResolution.canResolve(item.copy(kind = "unknown"), emptySet()))
    }

    private val question = WatchInteraction("q", "m", "s", "question", "", "", questions = listOf(
        WatchQuestion("Choose first", listOf(QuestionOption("One (Recommended)"), QuestionOption("Two"))),
        WatchQuestion("Choose many", listOf(QuestionOption("Red, green"), QuestionOption("Blue")), true)
    ), canAnswer = true)

    @Test fun requiresEveryQuestionAndPreservesExactOrderedLabels() {
        assertThrows(HubException::class.java) { WatchResolution.question(question, mapOf("Choose first" to setOf("One (Recommended)"))) }
        val answer = WatchResolution.question(question, mapOf("Choose first" to setOf("One (Recommended)"),
            "Choose many" to setOf("Blue", "Red, green")))
        assertEquals("Red, green, Blue", answer.getJSONObject("answers").getString("Choose many"))
        assertEquals("One (Recommended)", answer.getJSONObject("answers").getString("Choose first"))
        WatchResolution.validate(question, answer)
    }

    @Test fun sensitivePermissionNeverAllowsButCanDeny() {
        val ix = WatchInteraction("s", "m", "s", "permission", "", "", requiresInput = true, canAnswer = true)
        assertEquals("requires_phone", assertThrows(HubException::class.java) { WatchResolution.permission(ix, true) }.code)
        WatchResolution.validate(ix, WatchResolution.permission(ix, false))
        assertThrows(HubException::class.java) { WatchResolution.validate(ix, JSONObject("""{"allow":true}""")) }
    }

    @Test fun unknownKindHasNoApprovalFallback() {
        val ix = WatchInteraction("new", "m", "s", "new_approval", "", "", canAnswer = true)
        assertThrows(HubException::class.java) { WatchResolution.validate(ix, JSONObject("""{"allow":true}""")) }
        assertThrows(HubException::class.java) { WatchResolution.dismiss(ix) }
    }

    @Test fun planCannotEnableBypassAndPermissionCannotAlwaysAllow() {
        val plan = WatchInteraction("p", "m", "s", "plan", "", "", canAnswer = true)
        val body = WatchResolution.plan(plan, true)
        assertEquals("acceptEdits", body.getString("mode"))
        assertThrows(HubException::class.java) { WatchResolution.validate(plan, JSONObject("""{"approve":true,"mode":"bypassPermissions"}""")) }
        val permission = plan.copy(kind = "permission")
        assertThrows(HubException::class.java) { WatchResolution.validate(permission, JSONObject("""{"allow":true,"always":true}""")) }
    }

    @Test fun screenOnlySendsListedOptionsAndNeverArbitraryText() {
        val ix = WatchInteraction("s", "m", "s", "screen", "", "", options = listOf(DialogOption("1", "Continue")), canAnswer = true)
        assertEquals("1", WatchResolution.screen(ix, setOf("1")).getString("option"))
        assertThrows(HubException::class.java) { WatchResolution.screen(ix, setOf("2")) }
        assertThrows(HubException::class.java) { WatchResolution.validate(ix, JSONObject("""{"text":"shell command"}""")) }
    }
}
