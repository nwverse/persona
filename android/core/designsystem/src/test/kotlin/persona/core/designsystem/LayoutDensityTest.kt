package persona.core.designsystem

import android.content.Context
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** Settings › Appearance › Layout density (#5564): Compact tightens the spacing tokens, Standard draws the spec's values. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class LayoutDensityTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    @After
    fun reset() {
        context.getSharedPreferences("persona-appearance", Context.MODE_PRIVATE).edit().clear().commit()
        LayoutDensity.spaceScale = LayoutDensity.Default.scale
    }

    @Test
    fun standardIsTheDefaultAndKeepsTheSpecValues() {
        assertEquals(LayoutDensity.Standard, LayoutDensity.fromKey(null))
        assertEquals(LayoutDensity.Standard, LayoutDensity.fromKey("unknown"))
        val store = AppStyleStore(context)
        assertEquals(LayoutDensity.Standard, store.density.value)
        assertEquals(16.dp, Tokens.Space.l)
        assertEquals(Tokens.Space.Standard.m, Tokens.Space.m)
    }

    @Test
    fun compactTightensSpacingAndIsKept() {
        AppStyleStore(context).setDensity(LayoutDensity.Compact)
        assertEquals(12.dp, Tokens.Space.l)
        assertEquals(6.dp, Tokens.Space.s)
        // what sizes things with a spacing token keeps its size
        assertEquals(16.dp, Tokens.Space.Standard.l)

        LayoutDensity.spaceScale = LayoutDensity.Default.scale
        val reopened = AppStyleStore(context)
        assertEquals(LayoutDensity.Compact, reopened.density.value)
        assertEquals(12.dp, Tokens.Space.l)

        reopened.setDensity(LayoutDensity.Standard)
        assertEquals(16.dp, Tokens.Space.l)
    }
}
