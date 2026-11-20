package persona.core.hub

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.asRequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import persona.core.contracts.api.SessionGitPull
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import kotlin.coroutines.coroutineContext
import kotlinx.serialization.SerializationException
import okhttp3.HttpUrl
import okhttp3.Response
import java.io.File

/** Uses the active pairing's authenticated route, including its certificate policy. */
class HubSessionFiles internal constructor(
    private val request: suspend (String) -> Response,
    private val decoding: CoroutineDispatcher = Dispatchers.Default,
    /** An unbuffered request, for bodies read as a stream (file/download). */
    private val stream: suspend (String) -> Response = request,
    /**
     * A request that waits longer than the client's 15 s read timeout (seconds given): file/convert and file/thumb
     * wait on the machine's LibreOffice, which takes longer than that on a cold start.
     */
    private val slow: suspend (String, Long) -> Response = { path, _ -> request(path) },
    /** A POST with a JSON body (git/pull); the hub waits up to 90 s on the machine for it. */
    private val post: suspend (String, String) -> Response = { _, _ -> throw UnsupportedOperationException("post") },
    /** The hub's features from its last hello. */
    private val features: () -> Set<String> = { emptySet() },
    private val editPost: suspend (String, RequestBody, Long) -> Response = { _, _, _ -> throw UnsupportedOperationException("file edit") },
) : SessionFiles {
    constructor(connection: OkHttpHubConnection, decoding: CoroutineDispatcher = Dispatchers.Default) :
        this({ path -> connection.request(path, "GET", null) }, decoding, { path -> connection.request(path, "GET", null, buffered = false) },
            { path, seconds -> connection.request(path, "GET", null, readTimeoutSeconds = seconds) },
            { path, body -> connection.request(path, "POST", body.toRequestBody(JSON), readTimeoutSeconds = 100) },
            { connection.serverFeatures.value },
            { path, body, seconds -> connection.request(path, "POST", body, readTimeoutSeconds = seconds) })

    companion object {
        /** Past the hub's 2 minutes for file/convert (the node gives LibreOffice 90 s), as agent creation waits 130 s. */
        const val CONVERT_TIMEOUT_SECONDS = 130L
        /** Past the hub's 30 s for file/thumb (25 s at the node). */
        const val THUMB_TIMEOUT_SECONDS = 35L
        /** Past the hub's 2 minutes for a file change, as iOS waits 130 s for a trash or a restore. */
        const val EDIT_TIMEOUT_SECONDS = 130L
        /** Copy and ZIP can process up to 2 GB. The hub waits ten minutes for either. */
        const val COPY_ZIP_TIMEOUT_SECONDS = 610L
    }

    override suspend fun directory(machineId: String, cwd: String, path: String) =
        get<SessionDirectory>(machineId, "fs", cwd, "path" to path)
    override suspend fun findFiles(machineId: String, folder: String) =
        get<SessionFileSearch>(machineId, "fs/find", folder)

    override suspend fun fileInfo(machineId: String, cwd: String, path: String): SessionPathStat =
        get<SessionPathStats>(machineId, "stat", cwd, "path" to path, "detail" to "1").stats.singleOrNull { it.path == path }
            ?: throw HubFailure(HubError.Protocol, retryable = false)
    override suspend fun file(machineId: String, cwd: String, path: String) =
        get<SessionFileContent>(machineId, "file", cwd, "path" to path)
    override suspend fun filePreview(machineId: String, cwd: String, path: String) =
        get<SessionFileContent>(machineId, "file", cwd, "path" to path, "stream" to "1")
    override suspend fun status(machineId: String, cwd: String) =
        get<SessionGitStatus>(machineId, "git/status", cwd)
    override suspend fun diff(machineId: String, cwd: String, path: String, staged: Boolean) =
        get<SessionGitDiff>(machineId, "git/diff", cwd, "path" to path, "staged" to if (staged) "1" else "0")
    override suspend fun log(machineId: String, cwd: String, skip: Int, limit: Int): SessionGitLog {
        require(skip >= 0 && limit in 1..100)
        return get(machineId, "git/log", cwd, "skip" to skip.toString(), "limit" to limit.toString())
    }
    override suspend fun show(machineId: String, cwd: String, hash: String) =
        get<SessionGitShow>(machineId, "git/show", cwd, "hash" to hash)
    override fun comparesVersions(machineId: String) = "git_blob" in features()
    override suspend fun blob(machineId: String, cwd: String, path: String, rev: String) =
        get<SessionGitBlob>(machineId, "git/blob", cwd, "path" to path, "rev" to rev)

    override suspend fun pull(machineId: String, cwd: String): SessionGitPull {
        require(machineId.isNotBlank() && cwd.isNotBlank())
        val path = HttpUrl.Builder().scheme("https").host("example.com")
            .addPathSegments("api/v1/machines").addPathSegment(machineId).addPathSegments("git/pull").build().encodedPath
        val body = buildJsonObject { put("cwd", cwd) }.toString()
        return decode(post(path, body))
    }

    override fun supports(machineId: String, feature: SessionFileFeature) = features().let { it.isEmpty() || feature.wire in it }
    override fun editsFiles(machineId: String) = supports(machineId, SessionFileFeature.Edit)

    override suspend fun mkdir(machineId: String, cwd: String, dir: String, name: String) =
        edit<SessionFilePath>(machineId, "mkdir", buildJsonObject { put("dir", dir); put("cwd", cwd); put("name", name) }).path

    override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean,
                                   revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
        require(create || force || !revision.isNullOrBlank() || !sha256.isNullOrBlank()) { "A save requires the opened file version" }
        return edit(machineId, "write", buildJsonObject {
            put("path", path); put("cwd", cwd); put("text", text)
            if (create) put("create", true)
            revision?.let { put("revision", it) }
            sha256?.let { put("sha256", it) }
            if (force) put("force", true)
        })
    }

    override suspend fun uploadFile(machineId: String, cwd: String, dir: String, name: String, file: File,
                                    conflict: SessionFileConflict?): SessionUploadedFile {
        require(machineId.isNotBlank() && dir.isNotBlank() && name.isNotBlank())
        val path = apiUrl(machineId, "files", cwd, "name" to name, "dest" to "dir", "dir" to dir, "conflict" to conflict?.wire)
        return decode(editPost(path, file.asRequestBody("application/octet-stream".toMediaType()), EDIT_TIMEOUT_SECONDS))
    }

    override suspend fun copyFiles(machineId: String, cwd: String, paths: List<String>, dir: String, conflict: SessionFileConflict?) =
        edit<SessionCopiedFiles>(machineId, "copy", transferBody(cwd, paths, dir, conflict), COPY_ZIP_TIMEOUT_SECONDS).copied

    override suspend fun move(machineId: String, cwd: String, paths: List<String>, directory: String, conflict: SessionMoveConflictPolicy?) =
        edit<SessionMovedFiles>(machineId, "move", transferBody(cwd, paths, directory, conflict)).moved

    private fun transferBody(cwd: String, paths: List<String>, dir: String, conflict: SessionFileConflict?) = buildJsonObject {
        putJsonArray("paths") { paths.forEach { add(it) } }
        put("cwd", cwd); put("dir", dir)
        conflict?.let { put("conflict", it.wire) }
    }

    override suspend fun zipFiles(machineId: String, cwd: String, paths: List<String>, name: String, dir: String?) =
        edit<SessionZippedFile>(machineId, "zip", buildJsonObject {
            putJsonArray("paths") { paths.forEach { add(it) } }
            put("cwd", cwd); put("name", name)
            dir?.let { put("dir", it) }
        }, COPY_ZIP_TIMEOUT_SECONDS)

    override suspend fun chmod(machineId: String, cwd: String, path: String, mode: Int): Int {
        require(mode in 0..511)
        return edit<SessionFileMode>(machineId, "chmod", buildJsonObject { put("path", path); put("cwd", cwd); put("mode", mode) }).mode
    }

    override suspend fun rename(machineId: String, cwd: String, path: String, name: String) =
        edit<SessionFilePath>(machineId, "rename", buildJsonObject { put("path", path); put("cwd", cwd); put("name", name) }).path

    override suspend fun move(machineId: String, cwd: String, path: String, directory: String, conflict: SessionMoveConflictPolicy?) =
        move(machineId, cwd, listOf(path), directory, conflict).singleOrNull() ?: throw HubFailure(HubError.Protocol, retryable = false)

    override suspend fun trash(machineId: String, cwd: String, paths: List<String>) =
        edit<SessionTrashedFiles>(machineId, "trash", buildJsonObject { putJsonArray("paths") { paths.forEach { add(it) } }; put("cwd", cwd) }).trashed

    override suspend fun restore(machineId: String, trashIds: List<String>) =
        edit<SessionRestoredFiles>(machineId, "restore", buildJsonObject { putJsonArray("trash_ids") { trashIds.forEach { add(it) } } })
            .restored.map { it.path }

    private suspend inline fun <reified T> edit(machineId: String, operation: String, body: JsonObject, timeout: Long = EDIT_TIMEOUT_SECONDS): T {
        require(machineId.isNotBlank())
        val path = HttpUrl.Builder().scheme("https").host("example.com")
            .addPathSegments("api/v1/machines").addPathSegment(machineId).addPathSegments("files/$operation").build().encodedPath
        return decode(editPost(path, body.toString().toRequestBody(JSON), timeout))
    }

    override suspend fun stat(machineId: String, cwd: String, paths: List<String>) =
        get<SessionPathStats>(machineId, "stat", cwd, "paths" to paths.joinToString("\n"))

    override suspend fun convertToPdf(machineId: String, cwd: String, path: String) =
        get<SessionConvertedFile>(machineId, "file/convert", cwd, "path" to path, "to" to "pdf", timeout = CONVERT_TIMEOUT_SECONDS)

    override suspend fun thumbnail(machineId: String, cwd: String, path: String, size: Int): ByteArray? {
        require(machineId.isNotBlank() && cwd.isNotBlank())
        val url = apiUrl(machineId, "file/thumb", cwd, "path" to path, "size" to size.coerceIn(32, 1024).toString())
        // The connection throws for any status but 2xx: 404 (`no_thumbnail`) is the machine saying it cannot draw
        // this file, the row keeps its icon; anything else stays an error the caller may try again.
        val response = try { slow(url, THUMB_TIMEOUT_SECONDS) } catch (failure: HubFailure) {
            if (failure.status == 404 || failure.code == "no_thumbnail") return null
            throw failure
        }
        return response.use {
            if (!it.isSuccessful) {
                val failure = HubFailure.from(it)
                if (failure.status == 404 || failure.code == "no_thumbnail") return null
                throw failure
            }
            val type = it.header("Content-Type").orEmpty()
            if (!type.startsWith("image/")) throw HubFailure(HubError.Protocol, retryable = false)
            withContext(Dispatchers.IO) { requireNotNull(it.body).bytes() }
        }
    }

    override suspend fun download(machineId: String, cwd: String, path: String, into: File): Long {
        require(machineId.isNotBlank() && cwd.isNotBlank())
        val url = apiUrl(machineId, "file/download", cwd, "path" to path)
        return stream(url).use { response ->
            if (!response.isSuccessful) throw HubFailure.from(response)
            withContext(Dispatchers.IO) {
                into.parentFile?.mkdirs()
                // A part file renamed at the end: a download cut short never passes for the whole file.
                // Its own part name: two pages fetching one file don't write into each other.
                val part = File(into.path + ".part-" + System.nanoTime())
                try {
                    var n = 0L
                    part.outputStream().use { out ->
                        requireNotNull(response.body).byteStream().use { input ->
                            val buffer = ByteArray(64 * 1024)
                            while (true) {
                                // Leaving the page stops the download between chunks, not at the end of the file.
                                coroutineContext.ensureActive()
                                val read = input.read(buffer)
                                if (read < 0) break
                                out.write(buffer, 0, read)
                                n += read
                            }
                        }
                    }
                    if (!part.renameTo(into)) throw java.io.IOException("rename")
                    n
                } finally {
                    part.delete()
                }
            }
        }
    }

    private fun apiUrl(machineId: String, operation: String, cwd: String, vararg query: Pair<String, String?>): String {
        // The placeholder origin is discarded; only encoded path and query reach the connection.
        val url = HttpUrl.Builder().scheme("https").host("example.com")
            .addPathSegments("api/v1/machines").addPathSegment(machineId).addPathSegments(operation)
            .addQueryParameter("cwd", cwd).apply { query.forEach { (key, value) -> if (value != null) addQueryParameter(key, value) } }.build()
        return url.encodedPath + "?" + url.encodedQuery
    }

    private suspend inline fun <reified T> get(
        machineId: String, operation: String, cwd: String, vararg query: Pair<String, String>, timeout: Long? = null,
    ): T {
        require(machineId.isNotBlank() && cwd.isNotBlank())
        val url = apiUrl(machineId, operation, cwd, *query)
        return decode(if (timeout != null) slow(url, timeout) else request(url))
    }

    private suspend inline fun <reified T> decode(response: Response): T = response.use {
        if (!it.isSuccessful) throw HubFailure.from(it)
        withContext(decoding) {
            try { JsonCodec.decodeFromString<T>(requireNotNull(response.body).string()) }
            catch (error: SerializationException) { throw HubFailure(HubError.Protocol, retryable = false, cause = error) }
        }
    }
}

private val JSON = "application/json".toMediaType()
