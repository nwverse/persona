package persona.feature.settings

import androidx.compose.runtime.Immutable

/**
 * Immutable app-owned metadata for the independent generated Build-row comparison (#13001). A nullable snapshot
 * crosses the generator's supported optional scalar-input boundary; a general nullable model protocol is not
 * claimed to be supported. PackageManager reads remain with the existing handwritten About implementation.
 */
@Immutable
internal data class AboutBuildSnapshot(val buildNumber: String?)
