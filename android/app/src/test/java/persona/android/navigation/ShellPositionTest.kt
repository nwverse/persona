package persona.android.navigation

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import persona.core.navigation.AppTab
import persona.core.navigation.ChatRoute
import java.io.File

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ShellPositionTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val scope = "navigation-test"
    private val file = File(context.noBackupFilesDir, "hub-data/$scope/shell-position.json")

    @Test fun defaultAndInvalidPositionsOpenPersona() {
        assertEquals(AppTab.Persona, ShellPosition().tab)
        file.parentFile!!.mkdirs()
        for (json in listOf("not json", """{"tab":"Inbox","machine":"example"}""",
            """{"tab":"Settings","machine":"example"}""",
            """{"tab":"Agents","chats":{"Inbox":{"machineId":"example","sessionId":"old"}}}""")) {
            file.writeText(json)
            assertEquals(json, ShellPosition(), ShellPositionStore(context, scope).load())
            assertEquals(json, json, file.readText())
        }
    }

    @Test fun currentTwoTabPositionsRoundTrip() {
        file.parentFile!!.mkdirs()
        val store = ShellPositionStore(context, scope)
        for (tab in AppTab.entries) {
            val position = ShellPosition(tab, "example", mapOf(tab to ChatRoute("example", "session")))
            store.save(position)
            assertEquals(position, store.load())
        }
    }
}
