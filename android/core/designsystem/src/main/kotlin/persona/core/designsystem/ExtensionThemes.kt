package persona.core.designsystem

import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import persona.core.contracts.api.ExtensionQuickPrompt
import persona.core.contracts.api.ExtensionTheme
import persona.core.contracts.api.ExtensionTriageTemplate
import kotlin.math.pow

val LocalExtensionPrompts = staticCompositionLocalOf<List<ExtensionQuickPrompt>> { emptyList() }
val LocalExtensionTriageTemplates = staticCompositionLocalOf<List<ExtensionTriageTemplate>> { emptyList() }
val LocalExtensionThemeOptions = staticCompositionLocalOf<List<ExtensionTheme>> { emptyList() }
val LocalExtensionThemeScope = staticCompositionLocalOf { "" }
val LocalExtensionRoleTheme = staticCompositionLocalOf<ExtensionTheme?> { null }

object ExtensionThemeColors {
    private val roles = setOf("accent", "positive", "caution", "danger", "ai")
    private val hex = Regex("#[0-9a-fA-F]{6}")
    private const val TextFloor = 4.5
    private const val AccentFloor = 4.5
    private const val SoftAlpha = 0.12f

    private fun parse(value: String): Color? = if (value.matches(hex)) Color(0xFF000000L or value.drop(1).toLong(16)) else null
    private fun channel(value: Float): Double = if (value <= 0.04045f) value / 12.92 else ((value + 0.055) / 1.055).pow(2.4)
    private fun light(color: Color) = 0.2126 * channel(color.red) + 0.7152 * channel(color.green) + 0.0722 * channel(color.blue)
    fun contrast(front: Color, back: Color): Double {
        val a = light(front.compositeOn(back)) + 0.05
        val b = light(back) + 0.05
        return maxOf(a, b) / minOf(a, b)
    }
    internal fun on(color: Color): Color = if (contrast(Color.White, color) >= contrast(Color.Black, color)) Color.White else Color.Black

    fun problems(theme: ExtensionTheme): List<String> {
        if (theme.problems.isNotEmpty()) return theme.problems
        val problems = mutableListOf<String>()
        for (dark in listOf(false, true)) {
            val values = if (dark) theme.dark else theme.light
            val mode = if (dark) "dark" else "light"
            if ("accent" !in values) problems += "$mode 缺少 accent" // i18n-ignore: diagnostics, only checked for emptiness
            for ((role, raw) in values) {
                val color = parse(raw)
                if (role !in roles || color == null) { problems += "$mode.$role 不是可用的角色色"; continue } // i18n-ignore: diagnostics, only checked for emptiness
                for (style in AppStyle.entries) {
                    val p = Palettes.of(style, dark)
                    val surfaces = listOf(p.background, p.surface.compositeOn(p.background), p.surfaceOpaque,
                        p.surfaceStrong.compositeOn(p.surfaceOpaque), p.sunken.compositeOn(p.surfaceOpaque))
                    val floor = if (role == "accent") AccentFloor else TextFloor
                    if (surfaces.any { contrast(color, it) < floor }) problems += "${style.key} $mode.$role 对比度不足" // i18n-ignore: diagnostics, only checked for emptiness
                    if (role in setOf("positive", "caution", "danger")) {
                        if (surfaces.any { contrast(color, color.copy(alpha = SoftAlpha).compositeOn(it)) < TextFloor })
                            problems += "${style.key} $mode.$role 提示底色上的对比度不足" // i18n-ignore: diagnostics, only checked for emptiness
                    }
                }
            }
        }
        return problems
    }

    fun palette(theme: ExtensionTheme, style: AppStyle, dark: Boolean): PersonaPalette {
        val p = Palettes.of(style, dark)
        val values = (if (dark) theme.dark else theme.light).mapNotNull { (role, raw) -> parse(raw)?.let { role to it } }.toMap()
        val accent = values["accent"] ?: p.accent
        fun status(role: String): StatusColors? = values[role]?.let { StatusColors(it, it, it.copy(alpha = SoftAlpha), on(it)) }
        return p.copy(accent = accent, accentSoft = accent.copy(alpha = SoftAlpha),
            positive = values["positive"] ?: p.positive,
            caution = values["caution"] ?: p.caution, cautionText = values["caution"] ?: p.cautionText,
            codeNumberOverride = values["caution"] ?: p.codeNumberOverride,
            danger = status("danger") ?: p.danger, ai = values["ai"] ?: p.ai,
            positiveRole = status("positive"), cautionRole = status("caution"), customRoles = true)
    }

    fun selection(scope: String, theme: ExtensionTheme): String = "$scope|${theme.key}"
    fun selected(scope: String, key: String?, themes: List<ExtensionTheme>): ExtensionTheme? =
        themes.firstOrNull { selection(scope, it) == key }
}
