package persona.feature.settings

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.ExtensionNotificationActionStore
import persona.core.contracts.api.ExtensionNotificationScope
import persona.core.contracts.api.ExtensionUiContext
import persona.core.contracts.api.ExtensionUiResult
import persona.core.contracts.api.ExtensionView
import persona.core.contracts.api.ExtensionViewAction
import persona.core.contracts.api.ExtensionViewPush
import persona.core.contracts.generated.ExtensionNotifyAction
import persona.core.contracts.generated.ExtensionNotifyActionInput

class ExtensionNotificationPageTest {
    private val scope = ExtensionNotificationScope("hub/generation", null, "assistant")
    private var current = scope
    private var time = 1_000L
    private var tokens = 0
    private val store = ExtensionNotificationActionStore(now = { time }, currentScope = { current },
        token = { "%032x".format(++tokens) })

    @Test fun foregroundTokenClaimsItsActionAndInputOnlyOnce() {
        val record = store.register("alert", "example-gateway", "usage", "state",
            ExtensionNotifyAction("note", "Reply", foreground = true, input = ExtensionNotifyActionInput("Text")), scope)!!
        val first = resolve(record, "hello")
        assertEquals("hello", first.execution!!.values["text"])
        assertTrue(first.requiresHttp)
        val second = resolve(record, "again")
        assertNull(second.execution)
        assertNull(second.error)
        assertTrue(second.requiresHttp)
    }

    @Test fun wrongScopeAndExpiryShowErrorsWithoutExecuting() {
        val record = store.register("alert", "example-gateway", "usage", "state",
            ExtensionNotifyAction("stop", "Stop", foreground = true), scope)!!
        current = scope.copy(namespace = "other")
        assertEquals(ExtensionNotificationPageError.WrongScope, resolve(record, null).error)
        current = scope
        time += 900_001
        assertEquals(ExtensionNotificationPageError.Expired, resolve(record, null).error)
        val mismatched = store.register("alert", "example-gateway", "usage", "state",
            ExtensionNotifyAction("stop", "Stop"), scope)!!
        assertEquals(ExtensionNotificationPageError.Invalid,
            resolveExtensionNotificationPage(mismatched.token, "example-gateway", "usage", "other", null, store).error)
    }

    @Test fun missingAndBodyTokensLeaveTheOrdinaryPageAlone() {
        val missing = resolveExtensionNotificationPage("0".repeat(32), "example-gateway", "usage", null, null, store)
        assertNull(missing.record)
        assertNull(missing.execution)
        assertNull(missing.error)
        val body = store.register("alert", "example-gateway", "usage", "state", null, scope)!!
        val ordinary = resolve(body, null)
        assertNull(ordinary.execution)
        assertNull(ordinary.error)
        assertTrue(!ordinary.requiresHttp)
    }

    @Test fun foregroundPressDoesNotLoadBeforeRunningAndUsesTheReturnedView() = runBlocking {
        val record = store.register("alert", "example-gateway", "usage", "state",
            ExtensionNotifyAction("note", "Reply", foreground = true, input = ExtensionNotifyActionInput()), scope)!!
        val execution = resolve(record, "hello").execution!!
        val api = PageUi(ExtensionUiResult(view = ExtensionView(title = "After", state = "next"), toast = "Done"))
        val model = ExtensionViewModel(api, record.extension, record.ui!!, initialState = record.state)
        val outcome = model.press(ExtensionViewAction(execution.action.id), execution.values)
        assertEquals(0, api.renders)
        assertEquals(listOf(Triple("note", "state", mapOf("text" to "hello"))), api.actions)
        assertEquals("After", model.state.value.view!!.title)
        assertEquals("Done", outcome.toast)
    }

    @Test fun foregroundOutcomesKeepTheExistingToastLinkAndNavigationResults() = runBlocking {
        for (result in listOf(ExtensionUiResult(toast = "Done"), ExtensionUiResult(openUrl = "https://example.com"),
            ExtensionUiResult(push = ExtensionViewPush(ui = "next")), ExtensionUiResult(pop = true))) {
            val record = store.register("alert:${result.hashCode()}", "example-gateway", "usage", "state",
                ExtensionNotifyAction("stop", "Stop", foreground = true), scope)!!
            val execution = resolve(record, null).execution!!
            val api = PageUi(result)
            val model = ExtensionViewModel(api, record.extension, record.ui!!, initialState = record.state)
            val outcome = model.press(ExtensionViewAction(execution.action.id), execution.values)
            assertEquals(result.toast, outcome.toast)
            assertEquals(result.openUrl, outcome.openUrl)
            assertEquals(result.push, outcome.push)
            assertEquals(result.pop, outcome.pop)
            assertEquals(0, api.renders)
        }
    }

    private fun resolve(record: persona.core.contracts.api.ExtensionNotificationActionRecord, text: String?) =
        resolveExtensionNotificationPage(record.token, record.extension, record.ui!!, record.scope.manager, text, store)

    private class PageUi(private val result: ExtensionUiResult) : ExtensionApiStub() {
        var renders = 0
        val actions = mutableListOf<Triple<String, String?, Map<String, String>?>>()
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView {
            renders++
            return ExtensionView()
        }
        override suspend fun uiAction(extension: String, ui: String, action: String, context: ExtensionUiContext?, state: String?,
            values: Map<String, String>?): ExtensionUiResult {
            actions += Triple(action, state, values)
            return result
        }
    }
}
