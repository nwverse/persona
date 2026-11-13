package persona.feature.settings

import android.content.Context
import android.content.pm.PackageInstaller
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.util.ReflectionHelpers

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class AppInstallRecoveryTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val installer get() = context.packageManager.packageInstaller

    private fun session(pkg: String = context.packageName, sealed: Boolean = true): Int {
        val id = installer.createSession(PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(pkg)
        })
        ReflectionHelpers.setField(installer.getSessionInfo(id), "sealed", sealed)
        return id
    }

    @Test fun cancellationOnlyTouchesConfirmedPendingSessionsForThisPackage() = runBlocking {
        val pending = session()
        val preparing = session(sealed = false)
        val otherPackage = session(pkg = "fixture.unrelated")
        assertEquals(setOf(pending), AppInstaller.pendingSessions(context))
        val addedAfterConfirmation = session()
        AppInstaller.cancelPending(context, setOf(pending, preparing, otherPackage))
        assertNull(installer.getSessionInfo(pending))
        for (id in listOf(preparing, otherPackage, addedAfterConfirmation)) assertNotNull(installer.getSessionInfo(id))
        assertEquals(setOf(addedAfterConfirmation), AppInstaller.pendingSessions(context))
    }

    @Test fun cancellationIsIdempotentAndDoesNotRemoveApplicationData() = runBlocking {
        val data = java.io.File(context.filesDir, "pairing-fixture").apply { writeText("keep") }
        val id = session()
        AppInstaller.cancelPending(context, setOf(id))
        AppInstaller.cancelPending(context, setOf(id))
        assertEquals("keep", data.readText())
        assertTrue(AppInstaller.pendingSessions(context).isEmpty())
    }
}
