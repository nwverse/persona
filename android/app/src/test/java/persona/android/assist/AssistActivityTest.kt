package persona.android.assist

import android.content.Intent
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import persona.feature.chat.AssistDraftBus

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class AssistActivityTest {
    @After
    fun cleanUp() {
        AssistDraftBus.clear()
    }

    @Test
    fun actionAssistHostsOverlayWithoutImmediatelySwitchingToMainOrAutoPublishingDraft() {
        val intent = Intent(Intent.ACTION_ASSIST).apply {
            putExtra(Intent.EXTRA_ASSIST_PACKAGE, "com.android.chrome")
            putExtra(Intent.EXTRA_TITLE, "Release Notes")
        }
        val controller = Robolectric.buildActivity(AssistActivity::class.java, intent).create()
        val activity = controller.get()

        // Must remain hosted as an in-place overlay over the current app instead of jumping away immediately.
        assertFalse(activity.isFinishing)
        assertNull(shadowOf(activity).nextStartedActivity)
        // Must never auto-attach or auto-publish assist context into MainActivity's draft bus.
        assertNull(AssistDraftBus.current())

        controller.destroy()
    }

    @Test
    fun actionAssistWithoutExtrasStartsAndDestroysCleanly() {
        val controller = Robolectric.buildActivity(AssistActivity::class.java, Intent(Intent.ACTION_ASSIST)).create()
        val activity = controller.get()

        assertFalse(activity.isFinishing)
        assertNull(shadowOf(activity).nextStartedActivity)
        assertNull(AssistDraftBus.current())

        controller.destroy()
    }
}
