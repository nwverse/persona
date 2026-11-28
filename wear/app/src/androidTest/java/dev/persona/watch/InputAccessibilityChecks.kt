package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.TextView
import dev.persona.watch.ui.WatchTheme

/** Structural assertions on actual native views; this is not a TalkBack or largest-font acceptance. */
internal object InputAccessibilityChecks {
    fun verifyOnMain(instrumentation: Instrumentation, activity: Activity, expectedFields: Int) {
        var result: Result<Unit>? = null
        instrumentation.runOnMainSync { result = runCatching { verify(activity, expectedFields) } }
        // Let the runner record a failed check instead of crashing the application's main thread.
        checkNotNull(result).getOrThrow()
    }

    private fun verify(activity: Activity, expectedFields: Int) {
        val views = mutableListOf<View>()
        fun visit(view: View) {
            views += view
            if (view is ViewGroup) for (index in 0 until view.childCount) visit(view.getChildAt(index))
        }
        visit(activity.window.decorView)
        val fields = views.filterIsInstance<EditText>()
        check(fields.size == expectedFields) { "form_field_count" }
        for (field in fields) {
            check(field.id != View.NO_ID && !field.hint.isNullOrBlank()) { "form_field_name" }
            check(views.filterIsInstance<TextView>().any { it !== field && it.labelFor == field.id && it.text.isNotBlank() }) {
                "form_field_label_association"
            }
            val minimum = WatchTheme.dp(activity, WatchTheme.touch)
            // setInputType can change TextView's minimum from pixels to lines, making minHeight
            // return -1. View.minimumHeight and the laid-out bounds still enforce the touch target.
            check(field.minimumHeight >= minimum) {
                "form_field_touch_height minimum=${field.minimumHeight} required=$minimum"
            }
            check(field.width >= minimum && field.height >= minimum) {
                "form_field_measured_touch_bounds width=${field.width} height=${field.height} required=$minimum"
            }
        }
    }
}
