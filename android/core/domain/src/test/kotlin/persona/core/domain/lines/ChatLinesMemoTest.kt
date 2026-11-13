package persona.core.domain.lines

import kotlin.random.Random
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.CompactBoundary
import persona.core.contracts.ImagePayload
import persona.core.contracts.Item

/** [ChatLinesMemo] draws what [chatLines] draws, call after call, while keeping the turns before a change. */
class ChatLinesMemoTest {
    private fun item(seq: Int, kind: String, text: String? = null, role: String = "assistant", id: String = "i$seq",
        tool: String? = null) = LineItem(Item(id, seq, role = role, kind = kind, text = text, toolUseId = tool,
            toolName = tool?.let { "Bash" }))
    private fun turn(seq: Int) = listOf(item(seq, "text", "go $seq", "user"), item(seq + 1, "thinking", "hm"),
        item(seq + 2, "tool_use", tool = "t$seq"), item(seq + 3, "tool_result", "ok", "user", tool = "t$seq"),
        item(seq + 4, "text", "done $seq"))

    @Test fun aChangeInTheLastTurnKeepsTheTurnsBeforeIt() {
        val options = LineOptions(live = true)
        val memo = ChatLinesMemo()
        val chat = (0 until 5).flatMap { turn(it * 10) }
        assertEquals(chatLines(chat, options), memo.lines(chat, options))
        assertEquals(0, memo.reused)
        val streamed = chat + item(45, "text", "more")
        assertEquals(chatLines(streamed, options), memo.lines(streamed, options))
        assertEquals(20, memo.reused)
        val edited = streamed.dropLast(1) + item(45, "text", "more and more")
        assertEquals(chatLines(edited, options), memo.lines(edited, options))
        assertEquals(20, memo.reused)
        // A result for the second turn's call, late: drawn with that call, so that turn on is drawn again.
        val late = edited + item(46, "tool_result", "late", "user", tool = "t10")
        assertEquals(chatLines(late, options), memo.lines(late, options))
        assertEquals(5, memo.reused)
        val next = late + turn(50)
        assertEquals(chatLines(next, options), memo.lines(next, options))
        assertEquals(late.size, memo.reused)
    }

    @Test fun randomStreamsDrawWhatChatLinesDraws() {
        var reused = 0L
        for (seed in 1..40) reused += Stream(Random(seed)).run(seed, steps = 250)
        assertTrue("turns were kept", reused > 0)
    }

    /** Appends, streamed edits, deletes, late and split items, and option changes, checked after every one. */
    private inner class Stream(val r: Random) {
        val items = mutableListOf<LineItem>()
        val calls = mutableListOf<String>()
        var seq = 0
        var n = 0
        var options = LineOptions()
        var last: ChatSnapshot? = null

        fun run(seed: Int, steps: Int): Long {
            val memo = ChatLinesMemo()
            var reused = 0L
            repeat(steps) { step ->
                change()
                val input = if (r.nextInt(10) == 0) items.shuffled(r) else items.toList()
                val expected = chatLines(input, options)
                assertEquals("seed $seed step $step", expected, memo.lines(input, options))
                reused += memo.reused
                last = expected
            }
            return reused
        }

        fun change() {
            when (r.nextInt(100)) {
                in 0..64 -> items += next(++seq, "i${n++}")
                in 65..74 -> if (items.isNotEmpty()) {
                    val at = if (r.nextBoolean()) items.lastIndex else r.nextInt(items.size)
                    val old = items[at]
                    items[at] = old.copy(item = old.item.copy(text = old.text.orEmpty() + " more"))
                }
                in 75..79 -> if (items.isNotEmpty()) items.removeAt(r.nextInt(items.size))
                in 80..86 -> if (items.isNotEmpty()) {
                    // Late (an older seq) or split (a block of the same seq): ordered by seq, then the id's part.
                    val at = if (r.nextBoolean()) items.lastIndex else r.nextInt(items.size)
                    items += next(items[at].seq, "i${n++}#${1 + r.nextInt(3)}")
                }
                else -> options = when (r.nextInt(5)) {
                    0 -> options.copy(live = !options.live)
                    1 -> {
                        val ids = last?.lines.orEmpty().filterIsInstance<ChatLine.Work>().map { it.id } + LiveWorkId
                        val id = ids[r.nextInt(ids.size)]
                        options.copy(openWork = if (id in options.openWork) options.openWork - id else options.openWork + id)
                    }
                    2 -> options.copy(liveTurnId = last?.turns?.takeIf { it.isNotEmpty() && r.nextBoolean() }
                        ?.let { it[r.nextInt(it.size)].id })
                    3 -> options.copy(frozen = items.takeIf { it.isNotEmpty() && r.nextInt(3) == 0 }?.let {
                        val at = it[r.nextInt(it.size)]
                        Frozen(at.seq, at.part, last?.liveTurnId)
                    })
                    else -> options.copy(pendingInteractionIds = if (options.pendingInteractionIds == null) listOf("q") else null)
                }
            }
        }

        fun pick(vararg values: String) = values[r.nextInt(values.size)]

        /** A recent call mostly, an older one (a later turn's result) or one never made now and then. */
        fun call(): String? = when {
            calls.isEmpty() || r.nextInt(12) == 0 -> "missing"
            r.nextInt(8) == 0 -> calls[r.nextInt(calls.size)]
            else -> calls[maxOf(0, calls.size - 1 - r.nextInt(3))]
        }

        fun next(seq: Int, id: String): LineItem {
            val base = Item(id, seq, ts = "2026-01-01T00:%02d:%02dZ".format(seq / 60 % 60, seq % 60))
            fun of(item: Item) = LineItem(item)
            return when (r.nextInt(40)) {
                in 0..5 -> of(base.copy(role = "user", kind = "text", text = pick("fix it", "continue", "继续", "and the tests"),
                    queued = if (r.nextInt(5) == 0) true else null))
                in 6..10 -> of(base.copy(role = "assistant", kind = "text",
                    text = pick("Done.", "API Error: 529 Overloaded", "API Error: 500 boom", "", "Looking.")))
                in 11..13 -> of(base.copy(role = "assistant", kind = "thinking", text = pick("hm", "")))
                in 14..19 -> {
                    calls += id
                    of(base.copy(role = "assistant", kind = "tool_use", toolUseId = id,
                        toolName = pick("Bash", "Read", "TodoWrite", "TaskCreate", "TaskUpdate", "TaskList"),
                        toolInput = buildJsonObject { put("description", JsonPrimitive("step $id")) }))
                }
                in 20..25 -> of(base.copy(role = "user", kind = "tool_result", toolUseId = call(),
                    isError = if (r.nextInt(8) == 0) true else null,
                    text = pick("ok", "Command running in background with ID: b${r.nextInt(4)}", "agentId: a${r.nextInt(4)}",
                        "Task #${r.nextInt(4)} created successfully", "{\"task\":{\"id\":\"1\",\"status\":\"completed\"}}",
                        "#1 [pending] write it", "{\"success\":false}")))
                26 -> of(base.copy(role = "user", kind = "image", toolUseId = if (r.nextBoolean()) call() else null,
                    image = ImagePayload()))
                in 27..33 -> when (r.nextInt(10)) {
                    0 -> of(base.copy(role = "system", kind = "notice", text = "woke", wake = true))
                    1 -> of(base.copy(role = "system", kind = "notice", text = "已中断"))
                    2, 3 -> LineItem(base.copy(role = "system", kind = "notice", text = "task"), task = TaskNote("shell",
                        "build", pick("completed", "failed", "event", "running"), call().takeIf { r.nextBoolean() },
                        "b${r.nextInt(4)}"))
                    4 -> of(base.copy(role = "system", kind = "notice", text = "Compacted (ctrl+o)",
                        compact = CompactBoundary(pick("manual", "auto"), 1000L).takeIf { r.nextInt(3) == 0 }))
                    5 -> of(base.copy(role = "system", kind = "notice",
                        text = if (r.nextInt(3) == 0) CompactedNotice else ContinuedNotice).takeIf { r.nextInt(3) == 0 }
                        ?: base.copy(role = "system", kind = "notice", text = "Compacted while idle"))
                    6, 7 -> of(base.copy(role = "system", kind = "notice", text = CompactCommandNotice))
                    else -> of(base.copy(role = "system", kind = "notice", text = "Model changed"))
                }
                in 34..35 -> of(base.copy(role = "assistant", kind = "recap", text = "recap"))
                36 -> LineItem(base.copy(role = "assistant", kind = "fold"), fold = Fold(3, 2, 1, seq, id))
                else -> of(base.copy(role = "system", kind = pick("unknown", "turn_duration")))
            }
        }
    }
}
