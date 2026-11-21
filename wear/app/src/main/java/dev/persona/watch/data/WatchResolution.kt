package dev.persona.watch.data

import org.json.JSONArray
import org.json.JSONObject

/** The watch never supplies arbitrary terminal input, secret fields or an Always grant. */
object WatchResolution {
    /** Used by both the displayed controls and the final tap, including an already-open confirmation. */
    fun canResolve(interaction: WatchInteraction, failedGroups: Set<String>): Boolean =
        interaction.groupId !in failedGroups && interaction.canAnswer && !interaction.muted && !interaction.delegating &&
            interaction.kind in setOf("question", "plan", "permission", "screen")

    fun question(interaction: WatchInteraction, picks: Map<String, Set<String>>): JSONObject {
        requireKind(interaction, "question")
        val answers = JSONObject()
        if (interaction.questions.isEmpty()) throw HubException("unanswered_question")
        interaction.questions.forEach { question ->
            val selected = picks[question.question].orEmpty()
            val labels = question.options.map { it.label }
            if (selected.isEmpty() || !labels.containsAll(selected) || !question.multiSelect && selected.size != 1)
                throw HubException("unanswered_question")
            answers.put(question.question, labels.filter { it in selected }.joinToString(", "))
        }
        return JSONObject().put("answers", answers)
    }

    fun plan(interaction: WatchInteraction, approve: Boolean, feedback: String = ""): JSONObject {
        requireKind(interaction, "plan")
        return JSONObject().put("approve", approve).apply {
            if (approve) put("mode", "acceptEdits") else if (feedback.isNotBlank()) put("feedback", feedback.take(2000))
        }
    }

    fun permission(interaction: WatchInteraction, allow: Boolean): JSONObject {
        requireKind(interaction, "permission")
        if (allow && interaction.requiresInput) throw HubException("requires_phone")
        return JSONObject().put("allow", allow)
    }

    fun screen(interaction: WatchInteraction, keys: Set<String>): JSONObject {
        requireKind(interaction, "screen")
        if (keys.isEmpty() || !interaction.options.map { it.key }.containsAll(keys)) throw HubException("invalid_answer")
        if (!interaction.multiSelect && keys.size != 1) throw HubException("invalid_answer")
        if (interaction.rawOptions) return JSONObject().put("keys", JSONArray().put(keys.single()))
        return if (interaction.multiSelect) JSONObject().put("options", JSONArray(interaction.options.map { it.key }.filter { it in keys }))
        else JSONObject().put("option", keys.single())
    }

    fun dismiss(interaction: WatchInteraction): JSONObject {
        requireAnswerable(interaction)
        return JSONObject().put("dismiss", true)
    }

    fun validate(interaction: WatchInteraction, resolution: JSONObject) {
        requireAnswerable(interaction)
        val keys = resolution.keys().asSequence().toSet()
        if (keys == setOf("dismiss") && resolution.opt("dismiss") == true) return
        when (interaction.kind) {
            "question" -> {
                if (keys != setOf("answers")) throw HubException("invalid_answer")
                val answers = resolution.optJSONObject("answers") ?: throw HubException("invalid_answer")
                if (interaction.questions.isEmpty() || answers.keys().asSequence().toSet() != interaction.questions.map { it.question }.toSet())
                    throw HubException("unanswered_question")
                interaction.questions.forEach { q ->
                    val answer = answers.text(q.question)
                    if (answer.isBlank()) throw HubException("unanswered_question")
                    if (!q.multiSelect && q.options.none { it.label == answer }) throw HubException("invalid_answer")
                    if (q.multiSelect && !validOrderedChoices(q.options.map { it.label }, answer)) throw HubException("invalid_answer")
                }
            }
            "plan" -> {
                if (keys.any { it !in setOf("approve", "mode", "feedback") } || resolution.opt("approve") !is Boolean)
                    throw HubException("invalid_answer")
                if (resolution.optBoolean("approve") && resolution.text("mode") != "acceptEdits") throw HubException("invalid_answer")
                if (!resolution.optBoolean("approve") && resolution.has("mode")) throw HubException("invalid_answer")
            }
            "permission" -> {
                if (keys != setOf("allow") || resolution.opt("allow") !is Boolean) throw HubException("invalid_answer")
                if (resolution.optBoolean("allow") && interaction.requiresInput) throw HubException("requires_phone")
            }
            "screen" -> when {
                keys == setOf("option") && !interaction.multiSelect -> {
                    if (interaction.options.none { it.key == resolution.text("option") }) throw HubException("invalid_answer")
                }
                keys == setOf("options") && interaction.multiSelect -> {
                    val selected = resolution.optJSONArray("options") ?: throw HubException("invalid_answer")
                    if (selected.length() == 0) throw HubException("invalid_answer")
                    for (i in 0 until selected.length()) if (interaction.options.none { it.key == selected.optString(i) })
                        throw HubException("invalid_answer")
                }
                keys == setOf("keys") -> {
                    val selected = resolution.optJSONArray("keys") ?: throw HubException("invalid_answer")
                    if (selected.length() != 1) throw HubException("invalid_answer")
                    val key = selected.optString(0)
                    if (key !in setOf("esc", "enter") && !(interaction.rawOptions && key.matches(Regex("[1-9]")) && interaction.options.any { it.key == key }))
                        throw HubException("invalid_answer")
                }
                else -> throw HubException("invalid_answer")
            }
            else -> throw HubException("unsupported_interaction")
        }
    }

    private fun validOrderedChoices(labels: List<String>, answer: String): Boolean {
        // Carry offsets, not combinations: option labels may themselves include commas.
        var offsets = setOf(0)
        for (label in labels) {
            val next = offsets.toMutableSet()
            for (offset in offsets) {
                val chunk = (if (offset == 0) "" else ", ") + label
                if (answer.startsWith(chunk, offset)) next += offset + chunk.length
            }
            offsets = next
        }
        return answer.length in offsets
    }

    private fun requireKind(interaction: WatchInteraction, kind: String) {
        requireAnswerable(interaction)
        if (interaction.kind != kind) throw HubException("invalid_answer")
    }
    private fun requireAnswerable(interaction: WatchInteraction) {
        if (interaction.kind !in setOf("question", "plan", "permission", "screen")) throw HubException("unsupported_interaction")
        if (!interaction.canAnswer || interaction.muted || interaction.delegating) throw HubException("read_only")
    }
}
