package persona.feature.chat

import android.content.Context
import android.webkit.CookieManager
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebSettings
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** The pool owns page history; the shared WebView cookie and disk stores outlive every renderer it replaces. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatWebViewPoolTest {
    private val link = "https://example.invalid/articles/original"
    private val next = "https://example.invalid/articles/linked"
    private val dev = "http://127.0.0.1:4173/preview"
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val created = mutableListOf<RecordingWebView>()
    private lateinit var pool: ChatWebViewPool

    @Before fun setUp() {
        CookieManager.getInstance().setCookie(link, "session=kept")
        pool = ChatWebViewPool(context) { viewContext ->
            RecordingWebView(viewContext).also(created::add)
        }
    }

    @After fun sharedDataSurvivesEveryScenario() {
        assertEquals("session=kept", CookieManager.getInstance().getCookie(link))
        assertTrue(created.all { it.cacheClearCalls == 0 })
        pool.destroy()
    }

    @Test fun firstAcquireUsesPreloadAndOrdinaryReleaseCreatesFreshPreload() {
        pool.preload(link)
        val preloaded = created.single()
        assertEquals(listOf(link), preloaded.loads)

        val shown = pool.acquire(link) as RecordingWebView
        assertSame(preloaded, shown)
        pool.release(shown)

        val fresh = pool.existing(link) as RecordingWebView
        assertNotSame(shown, fresh)
        assertEquals(1, shown.destroyCalls)
        assertEquals(listOf(link), fresh.loads)
        assertSame(fresh, pool.acquire(link))
        assertEquals(listOf(link), fresh.loads)
    }

    @Test fun ordinaryReleaseDropsNavigationMetadataAndBothHistoryDirections() {
        val shown = pool.acquire(link) as RecordingWebView
        shown.loadUrl(next)
        shown.back = true
        shown.forward = true
        shown.currentUrl = next
        shown.pageTitle = "Linked page"
        shown.failure = "Earlier failure"
        shown.loadProgress = 47

        pool.release(shown)

        val fresh = pool.existing(link) as RecordingWebView
        assertNotSame(shown, fresh)
        assertEquals(listOf(link), fresh.loads)
        assertEquals(link, fresh.currentUrl)
        assertFalse(fresh.canGoBack())
        assertFalse(fresh.canGoForward())
        assertEquals("", fresh.pageTitle)
        assertNull(fresh.failure)
        assertEquals(0, fresh.loadProgress)
    }

    @Test fun repeatedReleaseIsIdempotentAndKeepsTheReplacementPreload() {
        val shown = pool.acquire(link) as RecordingWebView
        pool.release(shown)
        val fresh = pool.existing(link) as RecordingWebView

        pool.release(shown)
        pool.release(shown)

        assertEquals(1, shown.destroyCalls)
        assertEquals(0, fresh.destroyCalls)
        assertSame(fresh, pool.existing(link))
        assertEquals(listOf(link), fresh.loads)
    }

    @Test fun releaseAfterMemoryTrimCannotDeleteANewPreload() {
        val shown = pool.acquire(link) as RecordingWebView
        pool.acquire("https://example.invalid/other")
        pool.trimMemory()
        assertEquals(1, shown.destroyCalls)

        pool.preload(link)
        val fresh = pool.existing(link) as RecordingWebView
        pool.release(shown)

        assertEquals(1, shown.destroyCalls)
        assertEquals(0, fresh.destroyCalls)
        assertSame(fresh, pool.existing(link))
    }

    @Test fun releaseAfterRendererLossCannotDeleteTheRetryView() {
        val shown = pool.acquire(link) as RecordingWebView
        simulateChatRendererGone(shown)
        assertEquals(1, shown.destroyCalls)

        val retry = pool.acquire(link) as RecordingWebView
        pool.release(shown)

        assertNotSame(shown, retry)
        assertSame(retry, pool.existing(link))
        assertEquals(0, retry.destroyCalls)
        assertEquals(listOf(link), retry.loads)
    }

    @Test fun releaseAndPreloadKeepTheRendererCapAndNeverTrimTheActiveView() {
        val activeUrl = "https://example.invalid/active"
        val active = pool.acquire(activeUrl) as RecordingWebView
        repeat(7) { index -> pool.preload("https://example.invalid/preload/$index") }

        assertTrue(created.count { it.destroyCalls == 0 } <= 4)
        assertEquals(0, active.destroyCalls)
        assertSame(active, pool.existing(activeUrl))

        pool.release(active)
        assertTrue(created.count { it.destroyCalls == 0 } <= 4)
        assertEquals(1, active.destroyCalls)
        assertNotSame(active, pool.existing(activeUrl))
    }

    @Test fun activePortForgetReloadsWithoutCreatingOrPreloadingAnotherView() {
        pool.forget(dev)
        val shown = pool.acquire(dev) as RecordingWebView
        shown.loadProgress = 100
        val count = created.size

        pool.forget(dev)
        assertSame(shown, pool.existing(dev))
        assertEquals(1, shown.reloadCalls)
        assertEquals(0, shown.destroyCalls)
        assertEquals(count, created.size)

        pool.release(shown)
        assertSame(shown, pool.existing(dev))
        assertEquals(count, created.size)
        assertEquals(0, shown.destroyCalls)
    }

    @Test fun inactivePortForgetCreatesFreshOnNextOpenAndKeepsNoCacheAcrossRelease() {
        pool.forget(dev)
        val shown = pool.acquire(dev) as RecordingWebView
        pool.acquire("https://example.invalid/elsewhere")

        pool.forget(dev)
        assertEquals(1, shown.destroyCalls)
        assertNull(pool.existing(dev))
        pool.release(shown)

        val fresh = pool.acquire(dev) as RecordingWebView
        assertNotSame(shown, fresh)
        assertEquals(WebSettings.LOAD_NO_CACHE, fresh.settings.cacheMode)
        assertEquals(listOf(dev), fresh.loads)
        val count = created.size

        pool.release(fresh)
        assertSame(fresh, pool.existing(dev))
        assertEquals(count, created.size)
        assertEquals(0, fresh.destroyCalls)
    }

    @Test fun loopbackReleasedAsAnOrdinaryLinkIsDestroyedAndNeverPreloaded() {
        val local = "http://localhost:5173/app"
        val shown = pool.acquire(local) as RecordingWebView
        val count = created.size

        pool.release(shown)

        assertEquals(1, shown.destroyCalls)
        assertNull(pool.existing(local))
        assertEquals(count, created.size)
    }

    @Test fun freshPreloadRetainsSharedLoginAndPageConfiguration() {
        val shown = pool.acquire(link) as RecordingWebView
        pool.release(shown)
        val fresh = pool.existing(link) as RecordingWebView

        assertTrue(fresh.settings.javaScriptEnabled)
        assertTrue(fresh.settings.domStorageEnabled)
        assertFalse(fresh.settings.allowFileAccess)
        assertFalse(fresh.settings.allowContentAccess)
        assertEquals(WebSettings.LOAD_CACHE_ELSE_NETWORK, fresh.settings.cacheMode)
        assertTrue(CookieManager.getInstance().acceptThirdPartyCookies(fresh))
        assertEquals("session=kept", CookieManager.getInstance().getCookie(link))
    }

    private class RecordingWebView(context: Context) : ChatSelectionWebView(context) {
        val loads = mutableListOf<String>()
        var destroyCalls = 0
        var reloadCalls = 0
        var cacheClearCalls = 0
        var back = false
        var forward = false

        override fun loadUrl(url: String) {
            loads += url
            currentUrl = url
        }

        override fun reload() {
            reloadCalls++
        }

        override fun destroy() {
            destroyCalls++
        }

        override fun clearCache(includeDiskFiles: Boolean) {
            cacheClearCalls++
        }

        override fun canGoBack() = back

        override fun canGoForward() = forward
    }
}

/** The pool ignores the detail, but the client's parameter is non-null, so a real one is passed. */
internal fun simulateChatRendererGone(view: ChatSelectionWebView) {
    val detail = object : RenderProcessGoneDetail() {
        override fun didCrash() = true
        override fun rendererPriorityAtExit() = 0
    }
    view.webViewClient.onRenderProcessGone(view, detail)
}
