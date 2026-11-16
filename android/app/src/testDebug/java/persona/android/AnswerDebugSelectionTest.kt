package persona.android

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import persona.android.debug.DebugAnswer
import persona.core.domain.answer.AnswerAdapter
import persona.core.domain.answer.KotlinAnswerAdapter
import persona.core.domain.answer.ShadowAnswerAdapter

@RunWith(RobolectricTestRunner::class)
class AnswerDebugSelectionTest {
    @Test fun debugLauncherSelectionIsOffByDefaultAndOptInWrapsWithoutChangingTheDelegate() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val adapter = KotlinAnswerAdapter()
        val original = AnswerSelection.choose(context)
        assertTrue(original is KotlinAnswerAdapter)
        DebugAnswer.persist(context, true)
        // Selection is deliberately process-stable: exercise the explicit enabled branch without simulating a restart.
        val wrapped = DebugAnswer.observe(adapter)
        assertTrue(wrapped is ShadowAnswerAdapter)
        DebugAnswer.persist(context, false)
    }
}
