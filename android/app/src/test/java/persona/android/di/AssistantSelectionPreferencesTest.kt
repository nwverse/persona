package persona.android.di

import android.app.Application
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.ManagerRoster

@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [35])
class AssistantSelectionPreferencesTest {
    private val roster = ManagerRoster(listOf(HubManager("assistant"), HubManager("work")), "assistant")
    private val preferences get() = RuntimeEnvironment.getApplication().getSharedPreferences(
        AssistantSelectionPreferences.FILE, Application.MODE_PRIVATE,
    )

    @Before fun clear() {
        check(preferences.edit().clear().commit())
        AssistantManagerChoice.reset()
    }

    @After fun reset() = AssistantManagerChoice.reset()

    private fun selectWork(store: AssistantSelectionPreferences, hub: String = "hub-a") {
        store.atHub(hub)
        store.reconcile(hub, roster)
        AssistantManagerChoice.pick("work")
        store.remember(AssistantManagerChoice.current.value)
    }

    @Test fun selectedAssistantSurvivesProcessStateResetAndPreferencesReopen() {
        selectWork(AssistantSelectionPreferences(preferences))
        AssistantManagerChoice.reset()
        val restarted = AssistantSelectionPreferences(preferences)
        restarted.atHub("hub-a")
        assertEquals("work", AssistantManagerChoice.current.value.shown)
        restarted.reconcile("hub-a", roster)
        assertEquals("work", AssistantManagerChoice.header("hub-a"))
    }

    @Test fun hubsKeepIndependentChoicesAndDefaultChoiceIsSaved() {
        val store = AssistantSelectionPreferences(preferences)
        selectWork(store)
        store.atHub("hub-b")
        store.reconcile("hub-b", roster)
        assertEquals("assistant", AssistantManagerChoice.current.value.shown)
        store.atHub("hub-a")
        assertEquals("work", AssistantManagerChoice.current.value.shown)
        store.reconcile("hub-a", roster)
        AssistantManagerChoice.pick(null)
        store.remember(AssistantManagerChoice.current.value)
        store.atHub("hub-b")
        store.atHub("hub-a")
        assertEquals("assistant", AssistantManagerChoice.current.value.shown)
    }

    @Test fun disconnectAndUnvalidatedDefaultCannotEraseRememberedSelection() {
        val store = AssistantSelectionPreferences(preferences)
        selectWork(store)
        store.atHub(null)
        store.remember(AssistantManagerChoice.current.value)
        store.atHub("hub-a")
        assertEquals("work", AssistantManagerChoice.current.value.shown)
        AssistantManagerChoice.atHub("hub-a")
        store.remember(AssistantManagerChoice.current.value)
        AssistantSelectionPreferences(preferences).atHub("hub-a")
        assertEquals("work", AssistantManagerChoice.current.value.shown)
    }

    @Test fun deletedOrInstallingManagerFallsBackAndStaleRosterCannotChangeOtherHub() {
        val store = AssistantSelectionPreferences(preferences)
        selectWork(store)
        store.reconcile("hub-a", ManagerRoster(listOf(HubManager("assistant"), HubManager("work", installing = "pending")), "assistant"))
        assertEquals("assistant", AssistantManagerChoice.current.value.shown)
        store.atHub("hub-a")
        assertEquals("assistant", AssistantManagerChoice.current.value.shown)
        store.atHub("hub-b")
        store.reconcile("hub-a", roster)
        assertEquals("hub-b", AssistantManagerChoice.current.value.hubId)
    }

    @Test fun delayedChoiceCannotOverwriteNewerSelectionAndHubChangeFlushesLatestPick() {
        val store = AssistantSelectionPreferences(preferences)
        selectWork(store)
        val delayed = AssistantManagerChoice.current.value
        AssistantManagerChoice.pick(null)
        store.remember(delayed)
        store.atHub("hub-b")
        store.atHub("hub-a")
        assertEquals("assistant", AssistantManagerChoice.current.value.shown)
    }
}
