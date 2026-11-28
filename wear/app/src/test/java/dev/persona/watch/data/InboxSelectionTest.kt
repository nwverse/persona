package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test

class InboxSelectionTest {
    private val item = InboxItem("sample-item", "Original", "Earlier text", "mail", true, manager = "sample-manager")
    private fun snapshot(items: List<InboxItem> = listOf(item), unavailable: Boolean = false) =
        WatchSnapshot(emptyList(), emptyList(), emptyList(), items, 100, unavailable)

    @Test fun refreshedDetailsAndReplyCapabilityReplaceTheSelectedItem() {
        val changed = item.copy(title = "Updated", preview = "Latest text", accountName = "Sample account", canReply = false)
        val review = InboxSelection.review(item, snapshot(listOf(changed)), true)
        assertEquals(InboxSelection.State.CHANGED, review.state)
        assertEquals(changed, review.item)
        assertTrue(review.shouldRedraw(composerFocused = true))
    }

    @Test fun anAuthoritativeRemovalReturnsNoSelectedItemEvenWhenAnotherManagerReusesItsId() {
        for (items in listOf(emptyList(), listOf(item.copy(manager = "other-manager")))) {
            val review = InboxSelection.review(item, snapshot(items), true)
            assertEquals(InboxSelection.State.REMOVED, review.state)
            assertNull(review.item)
            assertTrue(review.shouldRedraw(composerFocused = true))
        }
    }

    @Test fun anUnavailableInboxRetainsCachedDetailWithoutTreatingItAsCurrentOrRemoved() {
        for (fresh in listOf(null, snapshot(emptyList(), unavailable = true), snapshot(listOf(item), unavailable = true))) {
            val review = InboxSelection.review(item, fresh, true)
            assertEquals(InboxSelection.State.UNAVAILABLE, review.state)
            assertEquals(item, review.item)
            assertTrue(review.shouldRedraw(composerFocused = true))
        }
        val failedPrimary = InboxSelection.review(item, snapshot(), false)
        assertEquals(InboxSelection.State.UNAVAILABLE, failedPrimary.state)
        assertEquals(item, failedPrimary.item)
    }

    @Test fun unchangedFocusedComposerIsNotRebuiltButAnUnfocusedDetailCanRefresh() {
        val review = InboxSelection.review(item, snapshot(), true)
        assertEquals(InboxSelection.State.CURRENT, review.state)
        assertEquals(item, review.item)
        assertFalse(review.shouldRedraw(composerFocused = true))
        assertTrue(review.shouldRedraw(composerFocused = false))
    }

    @Test fun finalReplyDraftAndDoneGuardsRejectChangedRemovedAndUnavailableItemsBeforeAnyRequest() {
        var requests = 0
        val client = HubClient(HubConfig("https://hub.example.com", "fixture-watch-only"), transport = HubTransport { _, _, _, _, _ ->
            requests++
            throw AssertionError("A stale Inbox action must not reach the Hub")
        })
        val mutations = listOf<(HubClient) -> Unit>(
            { it.reply(item, "Keep this draft", "sample-operation") },
            { it.draftReply(item) },
            { it.markDone(item) },
        )
        val states = listOf(
            snapshot(listOf(item.copy(preview = "Changed elsewhere"))) to true,
            snapshot(listOf(item.copy(canReply = false))) to true,
            snapshot(emptyList()) to true,
            snapshot(emptyList(), unavailable = true) to true,
            snapshot() to false,
        )
        for ((fresh, available) in states) for (mutate in mutations) {
            var rejected: InboxSelection.Review? = null
            InboxSelection.act(item, fresh, available, { rejected = it }) { mutate(client) }
            assertNotNull(rejected)
            assertNotEquals(InboxSelection.State.CURRENT, rejected!!.state)
        }
        assertEquals(0, requests)
    }

    @Test fun aCurrentPrimaryItemAllowsEachSupportedMutationExactlyOnce() {
        val requests = mutableListOf<String>()
        val client = HubClient(HubConfig("https://hub.example.com", "fixture-watch-only"), transport = HubTransport { _, method, path, _, _ ->
            assertEquals("POST", method)
            requests += path
            HubResponse(200, if (path.contains("/draft?")) """{"text":"Suggested reply"}""" else "{}")
        })
        for (mutation in listOf<(HubClient) -> Unit>(
            { it.reply(item, "Reply", "sample-operation") }, { it.draftReply(item) }, { it.markDone(item) },
        )) {
            InboxSelection.act(item, snapshot(), true, { fail("A current item must remain actionable") }) { mutation(client) }
        }
        assertEquals(listOf("reply", "draft", "done").map { "/api/v1/inbox/sample-item/$it?manager=sample-manager" }, requests)
    }

    @Test fun blockedConfirmationPreservesTheCapturedDraftRevisionAndRetryOperation() {
        val stored = mutableMapOf<String, String>()
        val ledger = DraftLedger(object : DraftStorage {
            override fun get(key: String) = stored[key]
            override fun put(values: Map<String, String?>) {
                values.forEach { (key, value) -> if (value == null) stored.remove(key) else stored[key] = value }
            }
        }) { "sample-operation" }
        ledger.save("primary-item", "Keep the user's edit")
        val captured = ledger.submit("primary-item", "legacy-item-operation")
        var fresh = snapshot()
        val confirmReply = {
            InboxSelection.act(item, fresh, true, {}) {
                fail("The final confirmation must recheck the new snapshot")
                ledger.acknowledge(captured)
            }
        }
        fresh = snapshot(emptyList())
        confirmReply()
        assertEquals(captured.version, ledger.version("primary-item"))
        assertEquals(captured.operationId, ledger.submit("primary-item", "legacy-item-operation").operationId)
    }

    @Test fun healthyAdditionalHubsCannotMakeAnUnavailablePrimaryInboxActionable() {
        val merged = HubGroups.merge(listOf(
            HubGroupSnapshot("", "Primary", snapshot(), reachable = false),
            HubGroupSnapshot("additional", "Additional", snapshot(listOf(item.copy(preview = "Other Hub"))), reachable = true),
        ), 200)
        val review = InboxSelection.review(item, merged, true)
        assertEquals(InboxSelection.State.UNAVAILABLE, review.state)
        assertEquals(item, review.item)
    }
}
