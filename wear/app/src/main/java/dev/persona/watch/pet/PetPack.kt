package dev.persona.watch.pet

import dev.persona.watch.data.AssetTransport
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubException
import java.io.File
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.nio.file.AtomicMoveNotSupportedException
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap

/** Only a complete, verified package is published. Old Hub versions never get invented artwork. */
class PetPack(cache: File, config: HubConfig) {
    private val config = config.validated()
    val directory = File(cache, "pet/" + scope(this.config))
    fun file(asset: PetAsset): File = File(directory, asset.hash + ".png")

    fun cached(verify: Boolean = true): PetManifest? = runCatching {
        val saved = File(directory, "pet.json")
        if (!saved.isFile || saved.length() > 64 * 1024) return null
        val manifest = PetManifest.parse(saved.readText())
        if (manifest.assets.any { !valid(it, verify) }) return null
        manifest
    }.getOrNull()

    fun fetch(): PetManifest = synchronized(fetchLocks.computeIfAbsent(directory.canonicalPath) { Any() }) {
        val transport = AssetTransport()
        val next = PetManifest.parse(String(transport.get(config, PetManifest.PATH, 64 * 1024), Charsets.UTF_8))
        if (!directory.isDirectory && !directory.mkdirs()) throw HubException("pet_storage_failed")
        var total = 0L
        for (asset in next.assets) {
            if (!valid(asset, true)) {
                val bytes = transport.get(config, "/api/v1/personas/nwverse/${asset.file}")
                if (hash(bytes) != asset.hash) throw HubException("invalid_pet_assets")
                atomicWrite(file(asset), bytes)
            }
            total += file(asset).length()
            if (total > 20 * 1024 * 1024) throw HubException("response_too_large")
        }
        atomicWrite(File(directory, "pet.json"), next.json.toByteArray())
        val keep = next.assets.map { it.hash + ".png" }.toSet() + "pet.json"
        directory.listFiles()?.filter { it.isFile && it.name !in keep }?.forEach { it.delete() }
        next
    }

    private fun valid(asset: PetAsset, verify: Boolean): Boolean {
        val file = file(asset)
        return file.isFile && file.length() in 1..4L * 1024 * 1024 && (!verify || hash(file.readBytes()) == asset.hash)
    }

    companion object {
        private val fetchLocks = ConcurrentHashMap<String, Any>()
        fun scope(config: HubConfig): String = hash((config.baseUrl.trimEnd('/') + "\n" + config.fingerprint.orEmpty()).toByteArray()).take(32)
        fun hash(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        private fun atomicWrite(file: File, bytes: ByteArray) {
            val temporary = File(file.parentFile, file.name + ".part")
            try {
                temporary.outputStream().use { it.write(bytes); it.fd.sync() }
                try { Files.move(temporary.toPath(), file.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE) }
                catch (_: AtomicMoveNotSupportedException) { Files.move(temporary.toPath(), file.toPath(), StandardCopyOption.REPLACE_EXISTING) }
            } finally { temporary.delete() }
        }
    }
}
