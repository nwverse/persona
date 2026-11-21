package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class ExtensionCacheTest {
    @get:Rule val temporary = TemporaryFolder()
    private val config = HubConfig("https://hub.example.com", "fixture-private-token", deviceId = "fixture-watch")
    private val widget = ExtensionWidget("sample", "count", "Sample count", "Sample", listOf("large", "accessory_circular", "accessory_rectangular"), "overview")
    private val view = ExtensionWidgetView("Sample", listOf(WidgetItem.Metric(WidgetMetric("Count", "3"))), 120_000)

    @Test fun snapshotsAndBindingsSurviveNewStoreInstancesWithoutPersistingCredentials() {
        val root = temporary.newFolder()
        val store = ExtensionCacheStore(root) { 60_000 }
        store.saveCatalog("", config, listOf(widget))
        assertTrue(store.saveProjection("", config, widget, "accessory_circular", view))
        store.bind(7, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        val reopened = ExtensionCacheStore(root) { 61_000 }
        assertEquals(listOf(widget), reopened.catalog("", config))
        assertEquals("3", (reopened.projection("", config, widget, "accessory_circular")!!.view.items.single() as WidgetItem.Metric).metric.value)
        assertEquals("sample", reopened.binding(7)!!.owner)
        val stored = File(root, "widgets.json").readText()
        assertFalse(stored.contains(config.token))
        assertFalse(stored.contains(config.baseUrl))
    }

    @Test fun tokenChangesCannotReusePrivateContentOrBindings() {
        val store = ExtensionCacheStore(temporary.newFolder()) { 60_000 }
        store.saveCatalog("", config, listOf(widget))
        store.saveProjection("", config, widget, "accessory_circular", view)
        store.bind(7, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        val other = config.copy(token = "different-fixture-token")
        assertNull(store.catalog("", other))
        assertNull(store.projection("", other, widget, "accessory_circular"))
        assertTrue(store.configured("", other, widget).isEmpty())
        assertNotEquals(ExtensionCacheStore.identity(other), store.binding(7)!!.identity)
        store.saveCatalog("", other, listOf(widget))
        store.invalidate("", config)
        assertNotNull(store.catalog("", other))
    }

    @Test fun differentGroupsWithSameWidgetIdsNeverShareSnapshots() {
        val store = ExtensionCacheStore(temporary.newFolder()) { 60_000 }
        val group = "11111111-1111-1111-1111-111111111111"
        store.saveCatalog("", config, listOf(widget))
        store.saveCatalog(group, config, listOf(widget))
        store.saveProjection("", config, widget, "accessory_circular", view)
        assertNull(store.projection(group, config, widget, "accessory_circular"))
        store.bind(1, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        store.bind(2, group, config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        assertEquals(listOf(1), store.configured("", config, widget).map { it.instanceId })
        assertEquals(listOf(2), store.configured(group, config, widget).map { it.instanceId })
    }

    @Test fun explicitRemovalChangesOnlyItsInstance() {
        val store = ExtensionCacheStore(temporary.newFolder()) { 60_000 }
        store.saveCatalog("", config, listOf(widget))
        store.bind(1, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        store.bind(2, "", config, widget, "accessory_rectangular", WidgetFaceKind.LONG)
        store.remove(1)
        assertNull(store.binding(1))
        assertEquals(WidgetFaceKind.LONG, store.binding(2)!!.kind)
    }

    @Test fun catalogRemovalRevokesCachedProjectionAndRejectsUnknownBinding() {
        val store = ExtensionCacheStore(temporary.newFolder()) { 60_000 }
        store.saveCatalog("", config, listOf(widget))
        store.saveProjection("", config, widget, "accessory_circular", view)
        store.saveCatalog("", config, emptyList())
        assertNull(store.projection("", config, widget, "accessory_circular"))
        assertFalse(store.saveProjection("", config, widget, "accessory_circular", view))
        assertThrows(IllegalArgumentException::class.java) { store.bind(1, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT) }
    }

    @Test fun slotsRequireAdvertisedAccessoryFamilies() {
        assertEquals("accessory_circular", ExtensionCacheStore.familyFor(widget, WidgetFaceKind.SHORT))
        assertEquals("accessory_rectangular", ExtensionCacheStore.familyFor(widget, WidgetFaceKind.LONG))
        assertNull(ExtensionCacheStore.familyFor(widget.copy(families = listOf("large")), WidgetFaceKind.SHORT))
        assertEquals("accessory_inline", ExtensionCacheStore.familyFor(widget.copy(families = listOf("accessory_inline")), WidgetFaceKind.SHORT))
        val store = ExtensionCacheStore(temporary.newFolder()) { 60_000 }
        store.saveCatalog("", config, listOf(widget))
        assertThrows(IllegalArgumentException::class.java) { store.bind(1, "", config, widget, "large", WidgetFaceKind.SHORT) }
        assertThrows(IllegalArgumentException::class.java) { store.bind(-1, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT) }
    }

    @Test fun configurationIntentAcceptsOnlyTheTwoDocumentedWireTypes() {
        assertEquals(WidgetFaceKind.SHORT, WidgetConfigContract.kind(3))
        assertEquals(WidgetFaceKind.LONG, WidgetConfigContract.kind(4))
        listOf(Int.MIN_VALUE, -1, 0, 1, 2, 5, 6, 7, 8, 9, 10, Int.MAX_VALUE).forEach {
            assertNull(WidgetConfigContract.kind(it))
        }
    }

    @Test fun freshnessEndsAtProviderExpiryAndOneHourCapAndRejectsClockRollback() {
        val cached = CachedWidget(widget, "accessory_circular", view, 60_000)
        assertTrue(cached.fresh(119_999))
        assertFalse(cached.fresh(120_000))
        assertFalse(cached.fresh(59_999))
        val excessive = cached.copy(view = view.copy(expiresAtMs = 99_999_999))
        assertEquals(3_660_000L, excessive.freshUntilMs)
        assertFalse(excessive.fresh(3_660_000))
        assertFalse(cached.copy(view = view.copy(expiresAtMs = 0)).fresh(60_000))
    }

    @Test fun sourceSummaryDoesNotInventMeaningForUnsupportedContent() {
        assertEquals(FaceWords("Count", "3", "Count: 3"), WidgetFaceText.words(view, widget.title))
        assertEquals("70%", WidgetFaceText.words(view.copy(items = listOf(WidgetItem.Progress("Sample", 0.7, ""))), widget.title)!!.text)
        assertNull(WidgetFaceText.words(view.copy(items = listOf(WidgetItem.Row("Execute", "", ""))), widget.title))
        assertNull(WidgetFaceText.words(view.copy(items = emptyList()), widget.title))
        assertNull(WidgetFaceText.words(view.copy(items = listOf(WidgetItem.Text("  "))), widget.title))
    }

    @Test fun shortTextClippingPreservesUnicodeCodePoints() {
        assertEquals("Hello world", WidgetFaceText.clip("Hello\n\tworld", 20))
        val emoji = "\uD83D\uDE00"
        val result = WidgetFaceText.clip(emoji.repeat(8), 6)
        assertEquals(6, result.codePointCount(0, result.length))
        assertEquals(emoji.repeat(6), result)
    }

    @Test fun concurrentInstancesDoNotLoseBindings() {
        val root = temporary.newFolder()
        ExtensionCacheStore(root).saveCatalog("", config, listOf(widget))
        val pool = Executors.newFixedThreadPool(4)
        try {
            val tasks = (0 until 16).map { id -> pool.submit {
                ExtensionCacheStore(root).bind(id, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT)
            } }
            tasks.forEach { it.get(5, TimeUnit.SECONDS) }
        } finally { pool.shutdownNow() }
        assertEquals(16, ExtensionCacheStore(root).configured("", config, widget).size)
    }

    @Test fun corruptAndOverlargeFilesFailClosed() {
        val root = temporary.newFolder()
        File(root, "widgets.json").writeText("{broken")
        val store = ExtensionCacheStore(root)
        assertNull(store.catalog("", config))
        assertNull(store.binding(1))
        File(root, "widgets.json").writeText(" ".repeat(1_048_577))
        assertNull(store.catalog("", config))
        assertThrows(IllegalArgumentException::class.java) { store.saveCatalog("../unsafe", config, listOf(widget)) }
    }

    @Test fun unsupportedAtomicMovesFallBackForCatalogProjectionAndBindingWrites() {
        val root = temporary.newFolder()
        val attempts = mutableListOf<List<StandardCopyOption>>()
        val store = ExtensionCacheStore(root, moveFile = { source, target, options ->
            attempts += options.map { it as StandardCopyOption }
            if (StandardCopyOption.ATOMIC_MOVE in options)
                throw AtomicMoveNotSupportedException(source.toString(), target.toString(), "Test filesystem")
            Files.move(source, target, *options)
        }, now = { 60_000 })
        store.saveCatalog("", config, listOf(widget))
        assertTrue(store.saveProjection("", config, widget, "accessory_circular", view))
        store.bind(7, "", config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        val expected = listOf(listOf(StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE),
            listOf(StandardCopyOption.REPLACE_EXISTING))
        assertEquals(expected + expected + expected, attempts)
        val reopened = ExtensionCacheStore(root) { 60_001 }
        assertEquals(listOf(widget), reopened.catalog("", config))
        assertEquals(view, reopened.projection("", config, widget, "accessory_circular")!!.view)
        assertEquals(widget.id, reopened.binding(7)!!.widget)
        assertFalse(File(root, "widgets.new").exists())
    }

    @Test fun ordinaryMoveAndFallbackFailuresPropagateWithoutReplacingTheLastValidCache() {
        for (atomicUnsupported in listOf(false, true)) {
            val root = temporary.newFolder()
            ExtensionCacheStore(root).saveCatalog("", config, listOf(widget))
            val original = File(root, "widgets.json").readBytes()
            var attempts = 0
            val failure = IOException("Test move failure")
            val store = ExtensionCacheStore(root, moveFile = { source, target, options ->
                attempts++
                if (atomicUnsupported && StandardCopyOption.ATOMIC_MOVE in options)
                    throw AtomicMoveNotSupportedException(source.toString(), target.toString(), "Test filesystem")
                throw failure
            })
            assertSame(failure, assertThrows(IOException::class.java) { store.saveCatalog("", config, emptyList()) })
            assertEquals(if (atomicUnsupported) 2 else 1, attempts)
            assertArrayEquals(original, File(root, "widgets.json").readBytes())
            assertEquals(listOf(widget), ExtensionCacheStore(root).catalog("", config))
        }
    }

    private fun group(index: Int) = if (index == 0) "" else "00000000-0000-0000-0000-${index.toString().padStart(12, '0')}"

    @Test fun refreshingEachOfFourConnectionsKeepsEveryConfiguredSlotAndProjection() {
        val root = temporary.newFolder()
        var time = 1_000L
        val store = ExtensionCacheStore(root) { time++ }
        for (id in 0..3) {
            store.saveCatalog(group(id), config, listOf(widget))
            store.saveProjection(group(id), config, widget, "accessory_circular", view)
            store.bind(id, group(id), config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        }
        for (refreshed in listOf(3, 0, 2, 1, 0)) {
            store.saveCatalog(group(refreshed), config, listOf(widget))
            val reopened = ExtensionCacheStore(root)
            for (id in 0..3) {
                assertEquals(group(id), reopened.binding(id)!!.group)
                assertEquals(listOf(widget), reopened.catalog(group(id), config))
                assertEquals(view, reopened.projection(group(id), config, widget, "accessory_circular")!!.view)
            }
        }
    }

    @Test fun overflowPrunesUnboundGroupsBeforeOlderGroupsWithConfiguredSlots() {
        var time = 1_000L
        val store = ExtensionCacheStore(temporary.newFolder()) { time++ }
        for (id in 0..3) {
            store.saveCatalog(group(id), config, listOf(widget))
            store.saveProjection(group(id), config, widget, "accessory_circular", view)
        }
        store.bind(0, group(0), config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        store.bind(2, group(2), config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        store.saveCatalog(group(4), config, listOf(widget))
        assertNull(store.catalog(group(1), config))
        for (id in listOf(0, 2, 3, 4)) assertEquals(listOf(widget), store.catalog(group(id), config))
        for (id in listOf(0, 2)) {
            assertEquals(group(id), store.binding(id)!!.group)
            assertNotNull(store.projection(group(id), config, widget, "accessory_circular"))
        }
    }

    @Test fun overflowWithFourBoundGroupsRejectsTheWriteWithoutOrphaningAnySlot() {
        val root = temporary.newFolder()
        val store = ExtensionCacheStore(root)
        for (id in 0..3) {
            store.saveCatalog(group(id), config, listOf(widget))
            store.saveProjection(group(id), config, widget, "accessory_circular", view)
            store.bind(id, group(id), config, widget, "accessory_circular", WidgetFaceKind.SHORT)
        }
        val previous = File(root, "widgets.json").readBytes()
        val failure = assertThrows(IllegalStateException::class.java) { store.saveCatalog(group(4), config, listOf(widget)) }
        assertEquals("widget_cache_capacity", failure.message)
        assertArrayEquals(previous, File(root, "widgets.json").readBytes())
        assertNull(store.catalog(group(4), config))
        for (id in 0..3) {
            assertNotNull(store.binding(id))
            assertNotNull(store.projection(group(id), config, widget, "accessory_circular"))
        }
    }
}
