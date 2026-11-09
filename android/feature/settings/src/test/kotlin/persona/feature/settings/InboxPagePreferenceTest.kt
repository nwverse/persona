package persona.feature.settings

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.AppStyle
import persona.core.designsystem.AppStyleStore

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
@OptIn(ExperimentalCoroutinesApi::class)
class InboxPagePreferenceTest {
    private lateinit var context: Context

    @Before fun setup() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("persona-appearance", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test fun nativeChatsDefaultOnWithoutChangingAnExistingInterfaceStyle() {
        context.getSharedPreferences("persona-appearance", Context.MODE_PRIVATE).edit().putString("style", AppStyle.Brief.key).commit()
        val store = AppStyleStore(context)
        assertTrue(store.nativeInboxChats.value)
        assertEquals(AppStyle.Brief, store.style.value)
    }

    @Test fun togglesPublishImmediatelyToTheExistingFlow() = runTest {
        val store = AppStyleStore(context)
        val values = mutableListOf<Boolean>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { store.nativeInboxChats.collect { values += it } }
        store.setNativeInboxChats(false)
        assertFalse(store.nativeInboxChats.value)
        store.setNativeInboxChats(true)
        assertTrue(store.nativeInboxChats.value)
        assertEquals(listOf(true, false, true), values)
        assertEquals(AppStyle.Default, store.style.value)
    }

    @Test fun recreatingTheStoreRestoresBothToggleValuesAndPreservesStyle() {
        val store = AppStyleStore(context)
        store.setStyle(AppStyle.Board)
        store.setNativeInboxChats(false)
        val restored = AppStyleStore(context)
        assertFalse(restored.nativeInboxChats.value)
        assertEquals(AppStyle.Board, restored.style.value)
        restored.setNativeInboxChats(true)
        val enabled = AppStyleStore(context)
        assertTrue(enabled.nativeInboxChats.value)
        assertEquals(AppStyle.Board, enabled.style.value)
        assertTrue(context.getSharedPreferences("persona-appearance", Context.MODE_PRIVATE).getBoolean("native-inbox-chats", false))
    }
}
