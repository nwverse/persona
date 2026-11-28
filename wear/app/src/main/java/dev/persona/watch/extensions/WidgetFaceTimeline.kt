package dev.persona.watch.extensions

internal data class WidgetFaceProjection(val words: FaceWords, val startMs: Long, val endExclusiveMs: Long)

/** A future or expired observation uses the provider's explicit refresh default, without a fresh entry. */
internal fun CachedWidget.faceProjection(now: Long): WidgetFaceProjection? {
    if (!fresh(now)) return null
    val words = WidgetFaceText.words(view, widget.title) ?: return null
    return WidgetFaceProjection(words, savedAtMs, freshUntilMs)
}
