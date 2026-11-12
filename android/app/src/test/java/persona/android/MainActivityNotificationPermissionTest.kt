package persona.android

import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Test
import java.io.File

/** Notification permission is an explicit readiness action, never an Activity launch side effect. */
class MainActivityNotificationPermissionTest {
    private val sourcePath = "android/app/src/main/java/persona/android/MainActivity.kt"

    private fun source(start: File): File? = generateSequence(start.canonicalFile) { it.parentFile }
        .map { File(it, sourcePath) }
        .firstOrNull { it.isFile }

    @Test
    fun noActivityEntryPointRequestsNotificationPermission() {
        val file = source(File("."))
        assertNotNull("MainActivity.kt must exist in the current checkout", file)
        val content = requireNotNull(file).readText()

        // Absence across the whole Activity covers first launch, paired restore, Demo, automation, every API level,
        // saved-state recreation, onNewIntent, password login and notification deep links without a Hilt activity fixture.
        for (forbidden in listOf("POST_NOTIFICATIONS", "RequestPermission", "requestPermissions(")) {
            assertFalse("MainActivity must leave notification permission to ReadinessScreen: $forbidden", forbidden in content)
        }
    }
}
