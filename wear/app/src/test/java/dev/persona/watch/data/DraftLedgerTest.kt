package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test

class DraftLedgerTest {
    private class Storage : DraftStorage {
        val values = mutableMapOf<String, String>()
        override fun get(key: String) = values[key]
        override fun put(values: Map<String, String?>) { values.forEach { (key, value) -> if (value == null) this.values.remove(key) else this.values[key] = value } }
    }
    private val store = Storage()
    private var sequence = 0
    private fun ledger() = DraftLedger(store) { "sample-${++sequence}" }

    @Test fun lateSendCannotEraseNewerDraftOrItsOperation() {
        val ledger = ledger()
        ledger.save("hub-a", "First")
        val first = ledger.submit("hub-a", "legacy-first")
        ledger.save("hub-a", "Second")
        val second = ledger.submit("hub-a", "legacy-second")
        assertFalse(ledger.acknowledge(first))
        assertEquals("Second", ledger.text("hub-a"))
        assertEquals(second.operationId, ledger.submit("hub-a", "legacy-second").operationId)
        assertTrue(ledger.acknowledge(second))
        assertEquals("", ledger.text("hub-a"))
    }
    @Test fun editingAwayAndBackIsStillANewRevision() {
        val ledger = ledger()
        ledger.save("hub-a", "First")
        val first = ledger.submit("hub-a", "legacy")
        ledger.save("hub-a", "Other")
        ledger.save("hub-a", "First")
        assertFalse(ledger.acknowledge(first))
        assertNotEquals(first.operationId, ledger.submit("hub-a", "legacy").operationId)
    }
    @Test fun retryAfterUnconfirmedReceiptOrRecreationKeepsTextAndOperation() {
        val first = ledger()
        first.save("hub-a", "Keep me")
        val sent = first.submit("hub-a", "legacy")
        // A failed/pending/unknown reply has no acknowledge call.
        val recreated = ledger()
        assertEquals("Keep me", recreated.text("hub-a"))
        assertEquals(sent.operationId, recreated.submit("hub-a", "legacy").operationId)
    }
    @Test fun legacyDraftAndOperationKeepTheirIdentityOnFirstRetry() {
        store.values["draft:hub-a"] = "Old draft"
        store.values["legacy"] = "sample-previous-operation"
        val ledger = ledger()
        val sent = ledger.submit("hub-a", "legacy")
        assertEquals("sample-previous-operation", sent.operationId)
        assertTrue(ledger.acknowledge(sent))
        assertNull(store.values["legacy"])
    }
    @Test fun asynchronousSuggestionDoesNotReplaceNewEditsButCanBeExplicitlyApplied() {
        val ledger = ledger()
        ledger.save("hub-a", "Before")
        val version = ledger.version("hub-a")
        ledger.save("hub-a", "New edit")
        assertFalse(ledger.suggest(version, "Generated"))
        assertEquals("New edit", ledger.text("hub-a"))
        ledger.save("hub-a", "Generated") // Explicitly confirmed replacement.
        assertEquals("Generated", ledger.text("hub-a"))
    }
    @Test fun unchangedDraftAcceptsSuggestionAndCapturedOwnerNeverMovesToAnotherHub() {
        val ledger = ledger()
        ledger.save("hub-a", "Before")
        ledger.save("hub-b", "Other")
        assertTrue(ledger.suggest(ledger.version("hub-a"), "Generated"))
        val sent = ledger.submit("hub-a", "operation-a")
        assertTrue(ledger.acknowledge(sent))
        assertEquals("Other", ledger.text("hub-b"))
    }

    @Test fun generatingFromAnEmptyComposerPersistsANonemptyDraftBeforeRecreation() {
        val ledger = ledger()
        val empty = ledger.version("hub-a")
        assertEquals("", empty.text)
        assertTrue(ledger.suggest(empty, "Generated reply"))
        assertEquals("Generated reply", store.values["draft:hub-a"])
        assertNotNull(store.values["revision:hub-a"])
        assertEquals("Generated reply", ledger().text("hub-a"))
    }

    @Test fun acceptingEqualTextIsAlreadyPersistedAndDoesNotDiscardItsSendOperation() {
        val ledger = ledger()
        ledger.save("hub-a", "Generated reply")
        val before = ledger.submit("hub-a", "legacy")
        assertTrue(ledger.acceptSuggestion("hub-a", "Generated reply"))
        assertEquals(before.version, ledger.version("hub-a"))
        assertEquals(before.operationId, ledger.submit("hub-a", "legacy").operationId)
        assertEquals("Generated reply", ledger().text("hub-a"))
    }

    @Test fun lateSuggestionCanBeExplicitlyAcceptedIntoAnEmptyComposerWithoutReplacementConsent() {
        val ledger = ledger()
        val initial = ledger.version("hub-a")
        ledger.save("hub-a", "Edited while generating")
        ledger.save("hub-a", "")
        assertFalse(ledger.suggest(initial, "Generated reply"))
        assertTrue(ledger.acceptSuggestion("hub-a", "Generated reply"))
        assertEquals("Generated reply", ledger().text("hub-a"))
    }

    @Test fun replacingDifferentNonemptyTextRequiresTheExactConfirmedRevision() {
        val ledger = ledger()
        ledger.save("hub-a", "Existing")
        val confirmed = ledger.version("hub-a")
        assertFalse(ledger.acceptSuggestion("hub-a", "Generated reply"))
        ledger.save("hub-a", "New edit")
        assertFalse(ledger.acceptSuggestion("hub-a", "Generated reply", confirmed))
        assertEquals("New edit", ledger.text("hub-a"))
        assertTrue(ledger.acceptSuggestion("hub-a", "Generated reply", ledger.version("hub-a")))
    }

    @Test fun blankOrUnstoredSuggestionCannotReportAcceptance() {
        val ledger = ledger()
        assertFalse(ledger.acceptSuggestion("hub-a", "  "))
        val missingWrite = DraftLedger(object : DraftStorage {
            override fun get(key: String): String? = null
            override fun put(values: Map<String, String?>) = Unit
        })
        assertFalse(missingWrite.acceptSuggestion("hub-a", "Generated reply"))
        val failingWrite = DraftLedger(object : DraftStorage {
            override fun get(key: String): String? = null
            override fun put(values: Map<String, String?>) { throw IllegalStateException("sample storage failure") }
        })
        assertThrows(IllegalStateException::class.java) { failingWrite.acceptSuggestion("hub-a", "Generated reply") }
    }
}
