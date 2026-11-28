package dev.persona.watch.data

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class WatchParserTest {
    @Test fun quietMutedAndDelegatingCardsDoNotReachWrist() {
        val sessions = WatchParser.sessions(JSONObject("""{"sessions":[
            {"machine_id":"m","session_id":"quiet","quiet":true},
            {"machine_id":"m","session_id":"normal","capabilities":{"approve":true}}
        ]}"""))
        val cards = WatchParser.interactions(JSONObject("""{"interactions":[
            {"id":"quiet","machine_id":"m","session_id":"quiet","kind":"permission"},
            {"id":"muted","machine_id":"m","session_id":"normal","kind":"permission","muted":true},
            {"id":"delegating","machine_id":"m","session_id":"normal","kind":"question","delegating":true},
            {"id":"form","kind":"elicitation"},
            {"id":"visible","machine_id":"m","session_id":"normal","kind":"permission"}
        ]}"""), sessions)
        assertEquals(listOf("visible"), cards.map { it.id })
        assertTrue(cards.single().canAnswer)
    }

    @Test fun unknownSourcesAndMissingSessionsCannotBeApproved() {
        val sessions = WatchParser.sessions(JSONObject("""{"sessions":[
            {"machine_id":"m","session_id":"read","source_kind":"codex_history"}
        ]}"""))
        assertFalse(sessions.single().canSend)
        val cards = WatchParser.interactions(JSONObject("""{"interactions":[
            {"id":"a","machine_id":"m","session_id":"read","kind":"permission"},
            {"id":"b","machine_id":"m","session_id":"missing","kind":"plan"},
            {"id":"c","machine_id":"m","session_id":"","kind":"permission"},
            {"id":"d","machine_id":"m","session_id":"","kind":"future"}
        ]}"""), sessions)
        assertEquals(listOf(false, false, true, false), cards.map { it.canAnswer })
    }

    @Test fun unknownSessionFieldsAndMalformedRowsDoNotDropWholeList() {
        val sessions = WatchParser.sessions(JSONObject("""{"sessions":[null,{},
            {"machine_id":"m","session_id":"s","title":"Example","state":"working","future":{"a":1}}
        ]}"""))
        assertEquals("Example", sessions.single().title)
        assertThrows(HubException::class.java) { WatchParser.sessions(JSONObject("{}")) }
    }

    @Test fun assistantWorkAndIntermediateNarrationStartCollapsed() {
        val body = JSONObject("""{"items":[
            {"id":"0","kind":"text","role":"assistant","text":"old"},
            {"id":"tool","kind":"tool_use","role":"assistant","tool_name":"Read","text":"Read a file"},
            {"id":"1","kind":"text","role":"user","text":"**One**"},
            {"id":"2","kind":"text","role":"assistant","text":"Two"},
            {"id":"3","kind":"text","role":"user","text":"Three","from":"other-session"},
            {"id":"4","kind":"text","role":"assistant","text":"Four"},
            {"id":"5","kind":"text","role":"assistant","text":"Five"},
            {"id":"6","kind":"text","role":"assistant","text":"Six"}
        ]}""")
        val messages = WatchParser.messages(body)
        assertEquals(listOf("old", "One", "Two", "Three", "Six"), messages.map { it.text })
        assertEquals(listOf("Four", "Five"), messages.last().work.map { it.text })
        assertEquals("Read\nRead a file", messages.first().work.single().text)
        assertTrue(messages[1].own)
        assertFalse(messages[3].own)
        assertFalse(messages.any { it.id == "tool" })
    }

    @Test fun currentAssistantSegmentReplacesPreviousAndKeepsWorkInspectable() {
        val messages = WatchParser.messages(JSONObject("""{"items":[
            {"id":"u","kind":"text","role":"user","text":"Please inspect"},
            {"id":"a","kind":"text","role":"assistant","text":"Starting inspection","turn_id":"t"},
            {"id":"fold:f","seq":2,"kind":"fold","role":"assistant","turn_id":"t","fold":{"items":3,"tools":1,"through_seq":4,"tool_names":["Read"]}},
            {"id":"b","kind":"text","role":"assistant","text":"Checking results","turn_id":"t"}
        ]}"""))
        assertEquals(listOf("Please inspect", "Checking results"), messages.map { it.text })
        assertEquals("Starting inspection", messages.last().work.first().text)
        assertEquals(2L, messages.last().work.last().fromSeq)
        assertEquals(4L, messages.last().work.last().throughSeq)
        assertEquals(3, messages.last().work.last().count)
    }

    @Test fun explicitDifferentTurnsDoNotLosePreviousFinalAnswer() {
        val messages = WatchParser.messages(JSONObject("""{"items":[
            {"id":"a","kind":"text","role":"assistant","text":"First answer","turn_id":"one"},
            {"id":"b","kind":"text","role":"assistant","text":"Second answer","turn_id":"two"}
        ]}"""))
        assertEquals(listOf("First answer", "Second answer"), messages.map { it.text })
    }

    @Test fun sensitiveSecretToolNeedsPhoneEvenWithoutFields() {
        val cards = WatchParser.interactions(JSONObject("""{"interactions":[
            {"id":"run","kind":"permission","tool_name":"secret_run"},
            {"id":"handoff","kind":"permission","tool_name":"secret_handoff"},
            {"id":"fields","kind":"permission","fields":[{"name":"password"}]}
        ]}"""), emptyList())
        assertTrue(cards.all { it.requiresInput })
        cards.forEach { card -> assertThrows(HubException::class.java) { WatchResolution.permission(card, true) } }
    }

    @Test fun inboxOnlyShowsActionAndRoutesByManager() {
        val items = WatchParser.inbox(JSONObject("""{"items":[
            {"id":"a","title":"Needs reply","source":"github","status":"action","manager":"manager-a","connector":{"can_reply":true,"reply_to":"issue"}},
            {"id":"b","status":"fyi"},
            {"id":"c","status":"action","done_at":"2026-11-10T10:00:00Z"}
        ]}"""))
        assertEquals("a", items.single().id)
        assertEquals("manager-a", items.single().manager)
        assertTrue(items.single().canReply)
    }
}
