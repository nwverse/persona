package dev.persona.watch.data

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class WatchTextTest {
    private val labels = WatchTextLabels("[Photo]", "[Video]", "[File]", "[Card]", { "[$it contexts]" }, "[Context]")
    private fun item(raw: String) = JSONObject().put("kind", "text").put("role", "user").put("text", raw)
    private fun sourceBlock(json: String) = "<persona-context kind=\"message-source\" title=\"\">$json</persona-context>\n"

    @Test fun explicitExternalSourceWinsWithoutLosingUnknownMetadata() {
        val value = item("Ready for review.").put("client_msg_id", "sample-id")
            .put("source", JSONObject().put("kind", "future-source").put("title", "Sample review").put("future", true))
        val result = WatchText.message(value, labels)
        assertFalse(result.own)
        assertEquals("future-source", result.source?.kind)
        assertEquals("Sample review", result.source?.title)
        assertEquals("Ready for review.", result.text)
    }

    @Test fun persistedProvenanceIsRemovedFromDisplayAndClassifiesOnlyUnmarkedRuntimeInput() {
        val raw = sourceBlock("""{"kind":"patrol","title":"Sample check","conversation_id":"sample-source"}""") + "Ready"
        val inferred = WatchText.message(item(raw), labels)
        assertEquals("patrol", inferred.source?.kind)
        assertFalse(inferred.own)
        assertEquals("Ready", inferred.text)
        val sent = WatchText.message(item(raw).put("client_msg_id", "sent-by-person"), labels)
        assertTrue(sent.own)
        assertNull(sent.source)
        assertEquals("Ready", sent.text)
    }

    @Test fun firstUserSentinelOrMalformedSourcePreventsDownstreamAutomationInference() {
        val later = sourceBlock("""{"kind":"patrol","title":"Sample task"}""") + "Please read this example."
        for (first in listOf("""{"author":"user"}""", """{"kind":42}""", "not json")) {
            val value = item(sourceBlock(first) + later).put("injected", JSONObject().put("kind", "followup").put("line", "Wrong replacement"))
            val shown = WatchText.message(value, labels)
            assertTrue(shown.own)
            assertFalse(shown.injected)
            assertNull(shown.source)
            assertEquals("Please read this example.", shown.text)
        }
    }

    @Test fun structuredInjectionUsesItsReadableDetailInsteadOfModelInstructions() {
        val value = item("PRIVATE MODEL INSTRUCTIONS").put("injected", JSONObject()
            .put("kind", "followup").put("state", "interval").put("title", "Sample reminder")
            .put("line", "Scheduled run").put("detail", "Check the sample notes."))
        val result = WatchText.message(value, labels)
        assertFalse(result.own)
        assertTrue(result.injected)
        assertEquals("patrol", result.source?.kind)
        assertEquals("Check the sample notes.", result.text)
    }

    @Test fun fromAnotherConversationIsExternalButUnrecognizedPromptProseIsNotGuessed() {
        val external = WatchText.message(item("A sample update").put("from", "another-session"), labels)
        assertFalse(external.own)
        assertEquals("conversation", external.source?.kind)
        assertEquals("another-session", external.source?.conversationId)
        val plain = WatchText.message(item("Scheduled task example, written by a person."), labels)
        assertTrue(plain.own)
        assertNull(plain.source)
    }

    @Test fun namedContextBecomesOneMarkWhileInternalSourceAndInterjectDisappear() {
        val raw = sourceBlock("""{"author":"user"}""") +
            "<persona-context kind=\"context\" title=\"Sample &amp; notes\">hidden `model text`</persona-context>\n" +
            "<persona-context kind=\"context\" title=\"插话\">model instructions</persona-context>\nRead **this**" // i18n-ignore: canonical prompt title
        assertEquals("〔Sample & notes〕\nRead this", WatchText.shown(raw, labels))
        assertEquals("[2 contexts]\nRead", WatchText.shown(
            "<persona-context title=\"One\">a</persona-context><persona-context title=\"Two\">b</persona-context>Read", labels))
        assertEquals("[Context]\nRead", WatchText.shown("<persona-context>hidden</persona-context>Read", labels))
    }

    @Test fun imagesVideosFilesAndViewErrorsBecomeLocalizedMarksWithoutPaths() {
        val raw = "<persona-view-errors>internal `diagnostic`</persona-view-errors>" +
            "<attached_images>/private/sample.png</attached_images>[Image #1][Image #2]\n" +
            "<persona-file kind=\"video\" path=\"/private/clip.mp4\"/>" +
            "<persona-file path=\"/private/document.pdf\"/>\nReview these."
        val shown = WatchText.shown(raw, labels)
        assertFalse(shown.contains("private"))
        assertFalse(shown.contains("diagnostic"))
        assertEquals(1, Regex(Regex.escape(labels.photo)).findAll(shown).count())
        assertTrue(shown.contains(labels.video))
        assertTrue(shown.contains(labels.file))
        assertTrue(shown.endsWith("Review these."))
    }

    @Test fun pastedTextAndCodeExamplesAreKeptAsLiteralContent() {
        val example = "<persona-context kind=\"message-source\">{\"kind\":\"patrol\"}</persona-context>\n**literal**"
        assertEquals(example, WatchText.shown("<pasted_content>$example</pasted_content>", labels))
        assertEquals(example, WatchText.shown("```xml\n$example\n```", labels))
        assertEquals("<persona-context>literal</persona-context>", WatchText.shown("`<persona-context>literal</persona-context>`", labels))
    }

    @Test fun cardsUseFallbackOrTitleAndNeverShowIncompleteOrMalformedJson() {
        assertEquals("Before\nReady for review.\nAfter", WatchText.shown(
            "Before\n```persona-view\n{\"fallback\":\"Ready for review.\",\"private\":\"hidden\"}\n```\nAfter", labels))
        assertEquals("〔Sample card〕", WatchText.shown("~~~persona-card\n{\"title\":\"Sample card\"}\n~~~", labels))
        assertEquals("[Card]", WatchText.shown("```persona-view\n{\"fallback\":", labels))
        assertEquals("[Card]", WatchText.shown("```persona-view\nnot json\n```", labels))
        assertEquals("[Card]", WatchText.shown("```persona-card\n{\"title\":42}\n```", labels))
    }

    @Test fun cardExampleInsideAnOuterFenceIsNotInterpreted() {
        val example = "```persona-view\n{\"fallback\":\"An example\"}\n```"
        assertEquals(example, WatchText.shown("````markdown\n$example\n````", labels))
        assertEquals(example, WatchText.shown("<pasted_content>$example</pasted_content>", labels))
    }

    @Test fun onlyCanonicalTrailingActionMetadataIsRemoved() {
        val marker = "\n<persona-action view=\"sample\" action=\"confirm\">{\"values\":{\"choice\":\"one\"}}</persona-action>"
        val valid = WatchText.message(item("Confirm" + marker), labels)
        assertEquals("Confirm", valid.text)
        assertTrue(valid.fromCard)
        assertTrue(valid.own)
        val invalid = marker.replace("\"one\"", "42")
        assertTrue(WatchText.shown("Example" + invalid, labels).contains("persona-action"))
        assertFalse(WatchText.message(item("Example" + invalid), labels).fromCard)
        assertTrue(WatchText.shown("```xml\nExample" + marker, labels).contains("persona-action"))
    }

    @Test fun plainMarkdownIsReadableAndDefaultLabelsNeverInventEnglishUi() {
        assertEquals("Heading\n• Bold and italic\nLink", WatchText.shown("## Heading\n- **Bold** and *italic*\n[Link](https://example.com)", labels))
        assertEquals("2 * 3 and a*b*c", WatchText.shown("2 * 3 and a*b*c", labels))
        assertEquals("", WatchText.shown("```persona-view\ninvalid\n```"))
        assertEquals("Ready", WatchText.shown(sourceBlock("""{"author":"user"}""") + "Ready"))
    }
}
