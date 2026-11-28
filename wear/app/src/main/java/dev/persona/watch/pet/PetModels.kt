package dev.persona.watch.pet

import dev.persona.watch.data.HubException
import dev.persona.watch.data.WatchSnapshot
import org.json.JSONObject
import java.time.LocalTime

enum class PetState(val wire: String) { NEEDS_YOU("needs_you"), WORKING("working"), DONE("done"), IDLE("idle"), SLEEP("sleep"), OFFLINE("offline") }

data class PetStatus(val state: PetState, val count: Int = 0) {
    companion object {
        fun from(snapshot: WatchSnapshot?, unreachable: Boolean, hour: Int = LocalTime.now().hour, partlyUnreachable: Boolean = false): PetStatus {
            if (unreachable || snapshot == null) return PetStatus(PetState.OFFLINE)
            val sessions = snapshot.sessions.filter { !it.quiet && !it.archived }
            val waiting = snapshot.interactions.filter { !it.muted && !it.delegating }.map { it.sessionKey }.toSet() +
                sessions.filter { it.state == "needs_you" }.map { it.key }
            if (waiting.isNotEmpty()) return PetStatus(PetState.NEEDS_YOU, waiting.size)
            val working = sessions.count { it.state == "working" }
            if (working > 0) return PetStatus(PetState.WORKING, working)
            if (partlyUnreachable) return PetStatus(PetState.OFFLINE)
            val done = sessions.count { it.state == "unread" }
            if (done > 0) return PetStatus(PetState.DONE, done)
            return PetStatus(if (hour >= 23 || hour < 7) PetState.SLEEP else PetState.IDLE)
        }
    }
}

data class PetStoredStatus(val status: PetStatus, val updatedAt: Long) {
    fun visible(now: Long = System.currentTimeMillis()): PetStatus =
        if (freshUntil(now) == null) PetStatus(PetState.OFFLINE) else status

    /** Exclusive expiry for both the visible page and the system's complication timeline. */
    fun freshUntil(now: Long = System.currentTimeMillis()): Long? {
        if (now < 0 || updatedAt <= 0 || updatedAt > Long.MAX_VALUE - STALE_MS) return null
        if (updatedAt > now && updatedAt - now > FUTURE_SKEW_MS) return null
        return (updatedAt + STALE_MS).takeIf { now < it }
    }

    /** Refreshing the provider does not discard the already-valid part of this observation. */
    internal fun freshInterval(now: Long): PetFreshInterval? {
        if (status.state == PetState.OFFLINE) return null
        val until = freshUntil(now) ?: return null
        // Preserve visible()'s small clock-skew tolerance without a gap at the current instant.
        return PetFreshInterval(minOf(updatedAt, now), until)
    }

    companion object {
        const val STALE_MS = 60 * 60 * 1000L
        private const val FUTURE_SKEW_MS = 60_000L
    }
}

internal data class PetFreshInterval(val startMs: Long, val endExclusiveMs: Long)

data class PetAsset(val file: String, val hash: String)
data class PetClip(val asset: PetAsset, val fps: Double, val frames: Int, val still: Int)
data class PetManifest(val clips: Map<String, PetClip>, val faces: Map<String, PetAsset>, val json: String) {
    val assets: List<PetAsset> get() = clips.values.map { it.asset } + faces.values
    companion object {
        const val PATH = "/api/v1/personas/nwverse/pet/pet.json"
        fun parse(json: String): PetManifest = try {
            if (json.toByteArray().size > 64 * 1024) invalid()
            val body = JSONObject(json)
            if (body.getInt("format") != 1) invalid()
            val clipBody = body.getJSONObject("clips")
            val faceBody = body.getJSONObject("faces")
            val names = PetState.entries.map { it.wire }
            fun asset(value: JSONObject): PetAsset {
                val file = value.getString("file")
                val hash = value.getString("sha256").lowercase()
                if (!file.matches(Regex("pet/[a-z_-]+\\.png")) || !hash.matches(Regex("[a-f0-9]{64}"))) invalid()
                return PetAsset(file, hash)
            }
            val clips = (names + "tap").associateWith { name ->
                val value = clipBody.getJSONObject(name)
                val fps = value.getDouble("fps"); val frames = value.getInt("frames"); val still = value.getInt("still")
                if (!fps.isFinite() || fps <= 0 || fps > 30 || frames !in 1..120 || still !in 0 until frames) invalid()
                PetClip(asset(value), fps, frames, still)
            }
            PetManifest(clips, names.associateWith { asset(faceBody.getJSONObject(it)) }, json)
        } catch (failure: HubException) { throw failure }
        catch (_: Exception) { invalid() }
        private fun invalid(): Nothing = throw HubException("invalid_pet_assets")
    }
}
