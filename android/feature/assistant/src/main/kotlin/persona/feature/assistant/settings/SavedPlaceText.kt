package persona.feature.assistant.settings

import persona.core.contracts.api.SavedPlace
import java.util.Locale

/**
 * What the place editor decides without the hub or a geocoder request: when it may save, the radius choices,
 * how a place is described under its name (same rules as iOS `SavedPlaceText`).
 */
internal object SavedPlaceText {
    val radii: List<Int> get() = SavedPlace.PRESET_RADII

    /** A name and a location: what the hub needs. */
    fun canSave(place: SavedPlace, located: Boolean): Boolean =
        located && place.name.trim().isNotEmpty()

    /** The choices with [current] among them (a radius the assistant saved that is not one of the presets). */
    fun radiiWith(current: Int): List<Int> =
        if (current in radii) radii else (radii + current).sorted()

    /** "150 m", "1 km": a short road distance, matching iOS Measurement abbreviated. */
    fun radius(meters: Int, locale: Locale = Locale.getDefault()): String = when {
        meters >= 1000 && meters % 1000 == 0 ->
            String.format(locale, "%d km", meters / 1000)
        meters >= 1000 ->
            String.format(locale, "%.1f km", meters / 1000.0)
        else -> String.format(locale, "%d m", meters)
    }

    /** The line under a place's name: its address, else its coordinates. */
    fun detail(place: SavedPlace): String =
        if (place.address.isEmpty()) coordinates(place) else place.address

    /** "31.23040, 121.47370". */
    fun coordinates(place: SavedPlace): String =
        String.format(Locale.US, "%.5f, %.5f", place.latitude, place.longitude)

    enum class SearchOutcome { Found, Nothing, Failed }

    /** What the location section's footer says after a search for [query]; null when it found something. */
    fun searchNote(outcome: SearchOutcome, query: String, nothing: (String) -> String, failed: String): String? =
        when (outcome) {
            SearchOutcome.Found -> null
            SearchOutcome.Nothing -> nothing(query)
            SearchOutcome.Failed -> failed
        }
}
