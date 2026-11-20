package persona.feature.files

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Typeface
import android.text.Editable
import android.text.InputFilter
import android.text.InputType
import android.text.Spannable
import android.text.TextPaint
import android.text.TextWatcher
import android.text.style.CharacterStyle
import android.text.style.UpdateAppearance
import android.util.TypedValue
import android.view.Gravity
import android.widget.EditText
import androidx.compose.ui.graphics.toArgb
import persona.core.designsystem.CodeSyntax
import persona.core.designsystem.PersonaPalette
import kotlin.math.roundToInt

/** An editable native text surface. Only visible line numbers and syntax need drawing, even for a 1 MB file. */
internal class FileCodeInput(context: Context, val initial: String, private val language: String) : EditText(context) {
    private data class Undo(val start: Int, val removed: String, val added: Int)
    private val undo = ArrayDeque<Undo>()
    private var undoBytes = 0
    private var undoing = false
    private var removed = ""
    private var gutter = 0
    private var inset = 0
    private val numbers = Paint(Paint.ANTI_ALIAS_FLAG).apply { textAlign = Paint.Align.RIGHT; typeface = Typeface.MONOSPACE }
    private var colors: PersonaPalette? = null
    private var updateQueued = false
    private var oldFirst = -1
    private var oldLast = -1
    var changed: (Boolean, Boolean) -> Unit = { _, _ -> }
    var tooLarge: () -> Unit = {}

    init {
        background = null
        isSaveEnabled = false // A complete file does not belong in an activity-state Binder transaction.
        inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        typeface = Typeface.MONOSPACE
        gravity = Gravity.TOP or Gravity.START
        setHorizontallyScrolling(true)
        setText(initial)
        setSelection(0)
        filters = filters + InputFilter { _, from, to, current, start, end ->
            if (current.length - (end - start) + (to - from) <= FILE_WRITE_BYTES) null
            else { tooLarge(); current.subSequence(start, end) }
        }
        addTextChangedListener(object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence, start: Int, count: Int, after: Int) {
                removed = if (undoing) "" else s.subSequence(start, start + count).toString()
            }
            override fun onTextChanged(s: CharSequence, start: Int, before: Int, count: Int) {
                if (!undoing) {
                    undo.addLast(Undo(start, removed, count))
                    undoBytes += removed.length
                    while (undo.size > 64 || undoBytes > FILE_WRITE_BYTES) undoBytes -= undo.removeFirst().removed.length
                }
            }
            override fun afterTextChanged(s: Editable) {
                changed(s.length != initial.length || !initial.contentEquals(s), undo.isNotEmpty())
                oldFirst = -1
                queueColors()
            }
        })
    }

    fun appearance(palette: PersonaPalette, font: Float, padding: Int) {
        if (colors == palette && textSize == font && inset == padding) return
        colors = palette
        inset = padding
        setTextSize(TypedValue.COMPLEX_UNIT_PX, font)
        numbers.textSize = font
        numbers.color = palette.ink3.toArgb()
        setTextColor(palette.ink.toArgb())
        setBackgroundColor(palette.sunken.toArgb())
        highlightColor = palette.accentSoft.toArgb()
        if (android.os.Build.VERSION.SDK_INT >= 29) textCursorDrawable?.setTint(palette.accent.toArgb())
        oldFirst = -1
        updateGutter()
        queueColors()
    }

    fun undoOnce() {
        val last = undo.removeLastOrNull() ?: return
        undoBytes -= last.removed.length
        undoing = true
        try {
            text.replace(last.start, last.start + last.added, last.removed)
            setSelection(last.start + last.removed.length)
        } finally { undoing = false }
    }

    fun findNext(query: String): Boolean {
        if (query.isEmpty()) return false
        val contents = text
        var at = contents.indexOf(query, selectionEnd.coerceAtLeast(0), ignoreCase = true)
        if (at < 0) at = contents.indexOf(query, ignoreCase = true)
        if (at < 0) return false
        setSelection(at, at + query.length)
        bringPointIntoView(at)
        return true
    }

    fun replaceMatch(query: String, replacement: String, all: Boolean): Boolean {
        if (query.isEmpty()) return false
        val contents = text
        if (all) {
            if (!contents.contains(query, ignoreCase = true)) return false
            if (replacement.length > query.length) {
                var size = contents.length.toLong()
                var at = 0
                while (at < contents.length) {
                    val found = contents.indexOf(query, at, ignoreCase = true)
                    if (found < 0) break
                    size += replacement.length - query.length
                    if (size > FILE_WRITE_BYTES) { tooLarge(); return true }
                    at = found + query.length
                }
            }
            val replaced = contents.toString().replace(query, replacement, ignoreCase = true)
            contents.replace(0, contents.length, replaced)
        } else {
            if (selectionStart < 0 || selectionEnd < selectionStart ||
                !contents.subSequence(selectionStart, selectionEnd).toString().equals(query, ignoreCase = true)) {
                if (!findNext(query)) return false
            }
            val at = selectionStart
            contents.replace(at, selectionEnd, replacement)
            setSelection((at + replacement.length).coerceAtMost(contents.length))
        }
        return true
    }

    override fun onLayout(changed: Boolean, left: Int, top: Int, right: Int, bottom: Int) {
        super.onLayout(changed, left, top, right, bottom)
        updateGutter()
        queueColors()
    }

    private fun updateGutter() {
        val width = numbers.measureText(lineCount.coerceAtLeast(1).toString()).roundToInt() + inset * 2
        if (width == gutter) return
        gutter = width
        setPadding(gutter, inset, inset, inset)
    }

    override fun onScrollChanged(l: Int, t: Int, oldl: Int, oldt: Int) {
        super.onScrollChanged(l, t, oldl, oldt)
        queueColors()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val lines = layout ?: return
        val first = lines.getLineForVertical((scrollY - totalPaddingTop).coerceAtLeast(0))
        val last = lines.getLineForVertical(scrollY + height - totalPaddingTop)
        for (i in first..last) canvas.drawText((i + 1).toString(), (scrollX + gutter - inset).toFloat(),
            (lines.getLineBaseline(i) + totalPaddingTop).toFloat(), numbers)
    }

    private class Ink(private val tone: Int) : CharacterStyle(), UpdateAppearance {
        override fun updateDrawState(paint: TextPaint) { paint.color = tone }
    }

    private fun queueColors() {
        if (updateQueued || colors == null || language.isEmpty()) return
        updateQueued = true
        post {
            updateQueued = false
            val lines = layout ?: return@post
            val palette = colors ?: return@post
            val first = lines.getLineForVertical((scrollY - totalPaddingTop).coerceAtLeast(0))
            val last = lines.getLineForVertical(scrollY + height - totalPaddingTop)
            var start = lines.getLineStart(first)
            var end = lines.getLineEnd(last)
            if (end - start > 32_768) {
                // A single minified line may span the entire file. Limit scanning to the visible horizontal window.
                start = lines.getOffsetForHorizontal(first, scrollX.toFloat()).coerceIn(start, end)
                end = lines.getOffsetForHorizontal(last, (scrollX + width).toFloat()).coerceIn(start, minOf(end, start + 32_768))
            }
            if (oldFirst == start && oldLast == end) return@post
            oldFirst = start
            oldLast = end
            val editable = text
            editable.getSpans(0, editable.length, Ink::class.java).forEach(editable::removeSpan)
            for (span in CodeSyntax.spans(editable.subSequence(start, end).toString(), language)) {
                editable.setSpan(Ink(CodeSyntax.color(span.kind, palette).toArgb()), start + span.start, start + span.end,
                    Spannable.SPAN_EXCLUSIVE_EXCLUSIVE)
            }
        }
    }
}
