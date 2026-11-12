package persona.core.designsystem

import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Dp

/**
 * Settings › Appearance › Layout density (#5564): how much room goes around and between things. Compact tightens every
 * [Tokens.Space] value by its [scale]; text, icons, radii and touch targets keep their sizes. Standard is the default and
 * draws exactly the spec's values.
 */
enum class LayoutDensity(val key: String, val scale: Float, @StringRes val labelRes: Int) {
    Standard("standard", 1f, R.string.designsystem_density_standard),
    Compact("compact", 0.75f, R.string.designsystem_density_compact);

    val label: String @Composable get() = stringResource(labelRes)

    companion object {
        val Default = Standard
        fun fromKey(key: String?): LayoutDensity = entries.firstOrNull { it.key == key } ?: Default

        /**
         * What [Tokens.Space] multiplies by, for every window of the app. Snapshot state, so a composition that read a
         * spacing token draws again when the choice changes; [AppStyleStore] sets it.
         */
        var spaceScale by mutableFloatStateOf(Default.scale)
            internal set
    }
}

/** An off-scale gap (a bubble's 14 x 10 inset, say) that still follows [LayoutDensity]; on-scale gaps use [Tokens.Space]. */
internal val Dp.dense: Dp get() = this * LayoutDensity.spaceScale
