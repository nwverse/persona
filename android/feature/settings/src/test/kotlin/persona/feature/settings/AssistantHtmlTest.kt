package persona.feature.settings

import android.webkit.WebView
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

// The HTML sandbox's own rules (docs/assistant/assistant-views.md "Security boundaries each platform must verify"). These check what the app sets and
// takes; they are not the acceptance: Robolectric's WebView runs no page, makes no request and follows no link, so
// none of the network, navigation or loop cases is observed here (AssistantHtml.kt says what is still to be done).
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class AssistantHtmlTest {
    @Test fun notAcceptedYetSoEveryPageIsItsFallback() {
        assertFalse(AssistantHtmlSandbox.Accepted)
        assertFalse(AssistantHtmlSandbox.runnable())
    }

    @Test fun thePolicyComesBeforeThePage() {
        val doc = AssistantHtmlSandbox.document("<meta http-equiv=\"refresh\" content=\"0;url=https://example.com\"><script>fetch('https://example.com')</script>", bridge = true)
        val policy = doc.indexOf("Content-Security-Policy")
        assertTrue(policy in 0 until doc.indexOf("refresh"))
        assertTrue(policy < doc.indexOf("persona"))
        for (rule in listOf("default-src 'none'", "connect-src 'none'", "frame-src 'none'", "form-action 'none'", "base-uri 'none'", "img-src data:"))
            assertTrue(rule, AssistantHtmlSandbox.Policy.contains(rule))
        assertFalse("No bridge without a key", AssistantHtmlSandbox.document("x", bridge = false).contains(AssistantHtmlSandbox.BridgeObject))
    }

    @Test fun theWebViewKeepsNothingAndGoesNowhere() {
        val view = WebView(ApplicationProvider.getApplicationContext())
        var blocked = 0
        AssistantHtmlSandbox.configure(view, blocked = { blocked++ }, gone = {})
        with(view.settings) {
            assertTrue(javaScriptEnabled)
            assertFalse(domStorageEnabled)
            assertFalse(allowFileAccess)
            assertFalse(allowContentAccess)
            assertTrue(blockNetworkLoads)
            assertFalse(javaScriptCanOpenWindowsAutomatically)
        }
        val client = AssistantHtmlSandbox.SandboxClient(blocked = { blocked++ }, gone = {})
        val response = client.shouldInterceptRequest(view, null as android.webkit.WebResourceRequest?)
        assertEquals(403, response.statusCode)
        assertEquals(-1, response.data.read())
        assertTrue(client.shouldOverrideUrlLoading(view, null as android.webkit.WebResourceRequest?))
        assertEquals(2, blocked)
        assertFalse(AssistantHtmlSandbox.SandboxChrome.onCreateWindow(view, false, true, null))
    }

    @Test fun theBridgeTakesOneCheckedMessageFromTheMainFrameOfThisInstance() {
        val policy = HtmlBridgePolicy()
        val reply = """{"kind":"reply","text":"继续讨论这个方案"}"""
        assertNull("A subframe", policy.read(reply, mainFrame = false, instance = 1, current = 1))
        assertNull("An instance replaced since", policy.read(reply, mainFrame = true, instance = 1, current = 2))
        assertNull("Not JSON", policy.read("persona.reply('x')", true, 1, 1))
        assertNull("Too long", policy.read("""{"kind":"reply","text":"${"字".repeat(2001)}"}""", true, 1, 1))
        assertNull("Another kind", policy.read("""{"kind":"eval","text":"x"}""", true, 1, 1))
        assertEquals(HtmlBridgeRequest.Reply("继续讨论这个方案"), policy.read(reply, true, 1, 1))
        assertNull("One confirm at a time", policy.read(reply, true, 1, 1))
        policy.answered(confirmed = false)
        assertEquals(HtmlBridgeRequest.Open("https://example.com"), policy.read("""{"kind":"open","url":"https://example.com"}""", true, 1, 1))
        policy.answered(confirmed = false)
        assertTrue(policy.stopped)
        assertNull("Turned down twice: not asked again", policy.read(reply, true, 1, 1))
    }
}
