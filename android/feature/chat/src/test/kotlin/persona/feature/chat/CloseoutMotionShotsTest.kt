package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Closeout
import persona.core.contracts.CloseoutChecked
import persona.core.contracts.WorkArtifact
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import java.io.File

/**
 * Closeout card fold uses [persona.core.designsystem.SharedMotion.reveal] as iOS `CloseoutCard.swift:48`
 * (`Theme.Motion.reveal`): fade + vertical expand/shrink (default opacity + height under withAnimation).
 * Mid-reveal frames and resting captures go to `persona.shots` (default `build/closeout-motion-shots`).
 * Resting open/folded screens are pixel-identical with Reduce Motion on a fresh card.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class CloseoutMotionShotsTest {
    @get:Rule val rule = createComposeRule()

    private val out = File(System.getProperty("persona.shots") ?: "build/closeout-motion-shots").also { it.mkdirs() }
    private val closeout = Closeout(
        "done",
        did = listOf("Retry the import once when the hub restarts", "Added a test for the restart path"),
        merged = "yes",
        deployed = "no",
        verified = "Unit tests pass",
        checked = CloseoutChecked(merged = "yes"),
    )
    private val artifacts = listOf(WorkArtifact("pr", "https://example.com/acme/app/pull/128"))
    private var lightOpenCorner = 0

    @Test fun revealMidFrameAndRestingMatchAcrossStyles() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var reduce by mutableStateOf(false)
        var generation by mutableIntStateOf(0)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = reduce, style = style) {
                    Box(
                        Modifier.fillMaxWidth().background(PersonaTheme.palette.background)
                            .padding(Tokens.Space.m).testTag("closeout-shot"),
                    ) {
                        key(generation) { CloseoutCard(closeout, artifacts, sessionKey = "motion-$generation") }
                    }
                }
            }
        }
        settle()
        for (s in AppStyle.entries) {
            for (d in listOf(false, true)) {
                style = s
                dark = d
                reduce = false
                generation++
                Snapshot.sendApplyNotifications()
                settle()
                rule.onNodeWithTag("closeout-fields").assertIsDisplayed()
                val openMotion = capture("closeout-open-${name(s, d)}")

                // Fold, then reopen and catch a mid-reveal frame (Theme.Motion.revealTime = 0.28 s).
                rule.onNodeWithTag("closeout-head").performClick()
                settle()
                assertEquals(0, rule.onAllNodesWithTag("closeout-fields").fetchSemanticsNodes().size)
                val foldedMotion = capture("closeout-folded-${name(s, d)}")

                rule.onNodeWithTag("closeout-head").performClick()
                Snapshot.sendApplyNotifications()
                rule.mainClock.advanceTimeByFrame()
                rule.mainClock.advanceTimeBy(140)
                val mid = capture("closeout-mid-reveal-${name(s, d)}")
                assertTrue("mid reveal should differ from folded", differs(mid, foldedMotion))
                settle()
                val openAgain = capture("closeout-open-settled-${name(s, d)}")
                assertTrue("settled open should differ from mid", differs(openAgain, mid))

                // Fresh cards: Reduce Motion open matches the first open (neither ran an enter animation),
                // and Reduce Motion folded matches the animated resting fold.
                reduce = true
                generation++
                Snapshot.sendApplyNotifications()
                settle()
                rule.onNodeWithTag("closeout-fields").assertIsDisplayed()
                assertTrue("resting open pixel-identical", same(openMotion, capture("closeout-open-reduced-${name(s, d)}")))
                rule.onNodeWithTag("closeout-head").performClick()
                settle()
                assertEquals(0, rule.onAllNodesWithTag("closeout-fields").fetchSemanticsNodes().size)
                assertTrue("resting folded pixel-identical", same(foldedMotion, capture("closeout-folded-reduced-${name(s, d)}")))

                if (!d) lightOpenCorner = openMotion.getPixel(2, 2)
                else assertNotEquals("light and dark differ", lightOpenCorner, openMotion.getPixel(2, 2))
            }
        }
    }

    private fun name(style: AppStyle, dark: Boolean) = "${style.name.lowercase()}-${if (dark) "dark" else "light"}"

    private fun settle() {
        Snapshot.sendApplyNotifications()
        repeat(5) { rule.mainClock.advanceTimeByFrame() }
        rule.mainClock.advanceTimeBy(1_200)
        rule.waitForIdle()
    }

    private fun capture(name: String): Bitmap {
        val image = rule.onNodeWithTag("closeout-shot").captureToImage().asAndroidBitmap()
        File(out, "$name.png").outputStream().use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        return image
    }

    private fun differs(a: Bitmap, b: Bitmap): Boolean {
        if (a.width != b.width || a.height != b.height) return true
        var y = 0
        while (y < a.height) {
            var x = 0
            while (x < a.width) {
                if (a.getPixel(x, y) != b.getPixel(x, y)) return true
                x += 8
            }
            y += 8
        }
        return false
    }

    private fun same(a: Bitmap, b: Bitmap): Boolean = !differs(a, b)
}
