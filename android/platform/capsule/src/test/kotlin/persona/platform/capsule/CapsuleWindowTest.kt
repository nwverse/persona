package persona.platform.capsule

import android.content.Context
import android.view.MotionEvent
import android.view.View
import android.widget.FrameLayout
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.util.ReflectionHelpers

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class CapsuleWindowTest {
    private class Fixture {
        val context = ApplicationProvider.getApplicationContext<Context>()
        var clicks = 0
        var moves = 0
        var docks = 0
        var canceled = 0
        var collapsed = 0
        var focused = false
        var windowVisible = false
        var interactions = 0
        val frame = CapsuleWindow(context, { 80 }, { _, _ -> moves++ }, { docks++ }, { collapsed++ }, { focused = it }, { windowVisible = it }, { interactions++ })
        init {
            frame.addView(object : View(context) {
                override fun onTouchEvent(event: MotionEvent): Boolean {
                    if (event.actionMasked == MotionEvent.ACTION_CANCEL) canceled++
                    if (event.actionMasked == MotionEvent.ACTION_UP) performClick()
                    return true
                }
            }.apply { setOnClickListener { clicks++ } }, FrameLayout.LayoutParams(300, 500))
            frame.measure(View.MeasureSpec.makeMeasureSpec(300, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(500, View.MeasureSpec.EXACTLY))
            frame.layout(0, 0, 300, 500)
        }
        fun touch(action: Int, x: Float, y: Float) {
            val event = MotionEvent.obtain(0, 100, action, x, y, 0)
            frame.dispatchTouchEvent(event)
            event.recycle()
        }
    }

    @Test fun aHeaderTapClicksOnceWhileDraggingCancelsTheClickAndDocksOnce() {
        val f = Fixture()
        f.touch(MotionEvent.ACTION_DOWN, 20f, 20f)
        f.touch(MotionEvent.ACTION_UP, 20f, 20f)
        assertEquals(1, f.clicks)
        f.touch(MotionEvent.ACTION_DOWN, 20f, 20f)
        f.touch(MotionEvent.ACTION_MOVE, 120f, 120f)
        f.touch(MotionEvent.ACTION_UP, 120f, 120f)
        assertEquals(1, f.clicks)
        assertEquals(1, f.canceled)
        assertEquals(1, f.moves)
        assertEquals(1, f.docks)
    }

    @Test fun gesturesInTheComposerOrScrollableBodyNeverMoveOrDockTheWindow() {
        val f = Fixture()
        f.touch(MotionEvent.ACTION_DOWN, 20f, 200f)
        f.touch(MotionEvent.ACTION_MOVE, 20f, 350f)
        f.touch(MotionEvent.ACTION_UP, 20f, 350f)
        assertEquals(0, f.moves)
        assertEquals(0, f.docks)
        assertEquals(0, f.canceled)
    }

    @Test fun outsideAndWindowFocusChangesAreForwardedWithoutInventingAClick() {
        val f = Fixture()
        f.frame.onWindowFocusChanged(true)
        assertTrue(f.focused)
        f.frame.onWindowFocusChanged(false)
        assertFalse(f.focused)
        ReflectionHelpers.callInstanceMethod<Void>(f.frame, "onWindowVisibilityChanged",
            ReflectionHelpers.ClassParameter.from(Int::class.javaPrimitiveType!!, View.VISIBLE))
        assertTrue(f.windowVisible)
        ReflectionHelpers.callInstanceMethod<Void>(f.frame, "onWindowVisibilityChanged",
            ReflectionHelpers.ClassParameter.from(Int::class.javaPrimitiveType!!, View.INVISIBLE))
        assertFalse(f.windowVisible)
        f.touch(MotionEvent.ACTION_OUTSIDE, 400f, 600f)
        assertEquals(1, f.collapsed)
        assertEquals(0, f.clicks)
        assertEquals(0, f.interactions)
    }

    @Test fun aTouchInsideTheCapsuleSignalsOneUserInteractionPerGesture() {
        val f = Fixture()
        f.touch(MotionEvent.ACTION_DOWN, 20f, 200f)
        f.touch(MotionEvent.ACTION_MOVE, 20f, 350f)
        f.touch(MotionEvent.ACTION_UP, 20f, 350f)
        assertEquals(1, f.interactions)
        f.touch(MotionEvent.ACTION_DOWN, 20f, 20f)
        f.touch(MotionEvent.ACTION_CANCEL, 20f, 20f)
        assertEquals(2, f.interactions)
    }
}
