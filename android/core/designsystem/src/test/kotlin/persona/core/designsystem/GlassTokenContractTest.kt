package persona.core.designsystem

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.security.MessageDigest

/** Current iPhone standard tokens, with Android's native font metrics and glass renderer. */
class GlassTokenContractTest {
    @Test fun standardTypeUsesTheSystemFontWithoutContentLineSpacing() {
        val t = Types.Glass
        val roles = listOf(
            Triple(t.largeTitle, 28.sp, FontWeight.Bold),
            Triple(t.title, 22.sp, FontWeight.SemiBold),
            Triple(t.headline, 17.sp, FontWeight.SemiBold),
            Triple(t.body, 17.sp, FontWeight.Normal),
            Triple(t.bodyStrong, 17.sp, FontWeight.SemiBold),
            Triple(t.callout, 15.sp, FontWeight.Normal),
            Triple(t.caption, 13.sp, FontWeight.Normal),
            Triple(t.micro, 12.sp, FontWeight.Normal),
            Triple(t.label, 17.sp, FontWeight.SemiBold),
            Triple(t.labelSmall, 15.sp, FontWeight.Medium),
            Triple(t.number, 28.sp, FontWeight.Bold),
            Triple(t.voice, 17.sp, FontWeight.Normal),
        )
        for ((style, size, weight) in roles) {
            assertEquals(FontFamily.Default, style.fontFamily)
            assertEquals(size, style.fontSize)
            assertEquals(weight, style.fontWeight)
            assertEquals(TextUnit.Unspecified, style.lineHeight)
        }
        assertEquals(TabularFigures, t.number.fontFeatureSettings)
        assertEquals(TabularFigures, t.micro.fontFeatureSettings)
        assertEquals(FontFamily.Monospace, t.code.fontFamily)
    }

    @Test fun glassUsesSharedSystemSurfacesAndCurrentStateSemantics() {
        for ((dark, p) in listOf(false to Palettes.GlassLight, true to Palettes.GlassDark)) {
            assertEquals(if (dark) Color.Black else Color.White, p.background)
            assertEquals(if (dark) Tokens.Semantic.Glass.Dark.card else Tokens.Semantic.Glass.Light.card, p.surfaceOpaque)
            assertEquals(if (dark) Color.White else Color.Black, p.ink)
            assertEquals(Palettes.glassText(if (dark) Tokens.Semantic.Glass.Dark.secondaryText else Tokens.Semantic.Glass.Light.secondaryText, dark), p.ink2)
            assertEquals(p.ink2, p.ink3)
            val systemBlue = if (dark) Tokens.Semantic.Glass.Dark.accent else Tokens.Semantic.Glass.Light.accent
            assertEquals(Palettes.glassText(systemBlue, dark), p.accent)
            assertEquals(systemBlue, p.needsYou.dot)
            assertEquals(Palettes.glassText(if (dark) Color(0xFF409CFF) else Color(0xFF0064C8), dark), p.needsYou.text)
            assertEquals(if (dark) Tokens.Status.Dark.Working.dot else Tokens.Status.Light.Working.dot, p.working.dot)
            assertEquals(Palettes.glassText(if (dark) Tokens.Status.Dark.Working.text else Tokens.Status.Light.Working.text, dark), p.working.text)
            assertEquals(if (dark) Tokens.Status.Dark.Unread.dot else Tokens.Status.Light.Unread.dot, p.done.dot)
            assertEquals(Palettes.glassText(if (dark) Tokens.Status.Dark.Unread.text else Tokens.Status.Light.Unread.text, dark), p.done.text)
            val neutralTint = if (dark) Tokens.Status.Dark.Offline.tint else Tokens.Status.Light.Offline.tint
            assertEquals(neutralTint, p.needsYou.tint)
            assertEquals(neutralTint, p.working.tint)
            assertEquals(neutralTint, p.done.tint)
            assertTrue(p.surface.alpha < 1f)
            assertEquals(p.surfaceOpaque, p.statusSurface)
            assertEquals(if (dark) Tokens.Semantic.Glass.Dark.userBubble else Tokens.Semantic.Glass.Light.userBubble, p.userBubble)
            assertEquals(p.caution, p.tone(Tone.Caution).dot)
            assertEquals(p.positive, p.tone(Tone.Positive).dot)
        }
        assertEquals(14.dp, Shapes.Glass.card)
        assertEquals(22.dp, Shapes.Glass.panel)
        assertEquals(10.dp, Shapes.Glass.control)
        assertEquals(20.dp, Tokens.Radius.bubble)
    }

    /** Pin entire non-Glass definitions, including fonts, heights, statuses, source badges and shared scale helper. */
    @Test fun briefAndBoardDefinitionsRemainByteIdentical() {
        val expected = mapOf(
            "Typography.kt" to "c27fdf9ee919e9bd6a9f898d9fb2b959bfae36d5f3a82bf23bbe950df6d2ce93",
            "PersonaTheme.kt" to "33b727267bc7b07e8e4e4666c3afeeb5a7e6eab4b6f17bfff7f81f6111e679e8",
            "StatusPalette.kt" to "6269abcf94815d8ca5cbd61f121fc594e0fe41e8ca24bb7e7d997e25f6c4ccdf",
        )
        for ((name, hash) in expected) {
            val source = repositoryFile("android/core/designsystem/src/main/kotlin/persona/core/designsystem/$name").readText()
            val stable = when (name) {
                "Typography.kt" -> source.substringAfter("    private fun scale(").substringBefore("    // Native system metrics") +
                    source.substringAfter("    val Brief = scale(").substringBefore("    fun of(style:")
                "PersonaTheme.kt" -> source.substringAfter("    val Brief = PersonaShapes(").substringBefore("    fun of(style:")
                else -> source.substringAfter("    // Brief: paper and ink;").substringBefore("    fun of(style:")
            }
            val actual = MessageDigest.getInstance("SHA-256").digest(stable.toByteArray()).joinToString("") { "%02x".format(it) }
            assertEquals(name, hash, actual)
        }
    }

    private fun repositoryFile(path: String): File =
        generateSequence(File(System.getProperty("user.dir"))) { it.parentFile }
            .map { File(it, path) }.first { it.isFile }
}
