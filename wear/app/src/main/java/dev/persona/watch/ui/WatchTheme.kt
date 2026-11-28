package dev.persona.watch.ui

import android.content.Context
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.content.res.ColorStateList
import android.text.TextUtils
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView

/** Wrist-sized counterparts of Theme.Watch and the shared status palette. */
object WatchTheme {
    val background = Color.BLACK
    val card = Color.rgb(0x22, 0x22, 0x23)
    val ink = Color.WHITE
    val secondary = Color.rgb(0xB8, 0xB8, 0xBF)
    val accent = Color.rgb(0x40, 0x9C, 0xFF)
    val primaryFill = Color.rgb(0x0A, 0x60, 0xB8)
    val positive = Color.rgb(0x4C, 0xC3, 0x8A)
    val working = Color.rgb(0xFF, 0xB2, 0x24)
    val danger = Color.rgb(0xFF, 0x5F, 0x57)
    val brand = Color.rgb(0xE8, 0x83, 0x5C)
    const val body = 15f
    const val title = 22f
    const val heading = 17f
    const val caption = 12f
    const val action = 14f
    const val screenInset = 22
    const val roundEndInset = 34
    const val gap = 10
    const val padding = 12
    const val radius = 18
    const val touch = 48
    const val lineGap = 4
    const val zero = 0
    const val cardPreviewLines = 3
    const val clockPattern = "HH:mm"
    fun dp(context: Context, value: Int) = (value * context.resources.displayMetrics.density).toInt()
    fun surface(context: Context, color: Int = card) = GradientDrawable().apply {
        setColor(color)
        cornerRadius = dp(context, radius).toFloat()
    }
    fun text(context: Context, value: CharSequence, size: Float = body, color: Int = ink, bold: Boolean = false) = TextView(context).apply {
        text = value
        textSize = size
        setTextColor(color)
        setLineSpacing(dp(context, lineGap).toFloat(), 1f)
        if (bold) setTypeface(typeface, Typeface.BOLD)
        importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
    }
    fun column(context: Context) = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        layoutParams = LinearLayout.LayoutParams(-1, -2)
    }
    fun button(context: Context, label: CharSequence, primary: Boolean = false, destructive: Boolean = false, onClick: () -> Unit) = Button(context).apply {
        text = label
        textSize = action
        isAllCaps = false
        isSingleLine = true
        // Single-line mode enables horizontal scrolling; disable it so auto-size uses the real width.
        setHorizontallyScrolling(false)
        setAutoSizeTextTypeUniformWithConfiguration(caption.toInt(), action.toInt(), 1, TypedValue.COMPLEX_UNIT_SP)
        ellipsize = TextUtils.TruncateAt.END
        setTextColor(if (destructive) danger else ink)
        minHeight = dp(context, touch)
        minimumHeight = dp(context, touch)
        insetTopCompat()
        gravity = Gravity.CENTER
        setPadding(dp(context, padding), dp(context, lineGap), dp(context, padding), dp(context, lineGap))
        val fill = surface(context, if (primary) primaryFill else card)
        background = RippleDrawable(ColorStateList.valueOf(Color.DKGRAY), fill, null)
        layoutParams = LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(context, gap) }
        setOnClickListener { onClick() }
    }
    private fun Button.insetTopCompat() { stateListAnimator = null }
}
