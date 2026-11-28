package dev.persona.watch.extensions

/**
 * Decode only the two types this provider advertises in the system configuration intent.
 * AndroidX 1.2.1 restricts ComplicationType.fromWireType to its library group. These values are
 * the documented public Wear configuration protocol, not enum ordinals or private API calls:
 * https://developer.android.com/reference/android/support/wearable/complications/ComplicationData#TYPE_SHORT_TEXT
 * https://developer.android.com/reference/android/support/wearable/complications/ComplicationData#TYPE_LONG_TEXT
 */
internal object WidgetConfigContract {
    private const val SHORT_TEXT_WIRE = 3
    private const val LONG_TEXT_WIRE = 4

    fun kind(wireType: Int): WidgetFaceKind? = when (wireType) {
        SHORT_TEXT_WIRE -> WidgetFaceKind.SHORT
        LONG_TEXT_WIRE -> WidgetFaceKind.LONG
        else -> null
    }
}
