package persona.core.hub

import java.io.Closeable
import java.io.File
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.RecordedRequest
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.*

class HubFileManagerTest {
    private val mid = "studio/one"
    private val cwd = "/home/demo/work tree"

    /** The real pairing, request manager selection and HTTP codec run against a loopback-only node stand-in. */
    private class Fixture(val id: String, features: List<String> = emptyList()) : Closeable {
        private val replies = LinkedBlockingQueue<MockResponse>()
        private val requests = LinkedBlockingQueue<RecordedRequest>()
        private val hub = LoopbackHub(hubId = id, features = features, api = { request ->
            if (request.requestUrl!!.encodedPath.startsWith("/api/v1/machines/")) {
                requests.add(request)
                replies.poll() ?: MockResponse().setResponseCode(500).setBody("Unexpected file request")
            } else MockResponse().setResponseCode(404)
        })
        val connection = OkHttpHubConnection(HubConnectionConfig(id, null, "fixture-$id", listOf(hub.endpoint("lan")), "test"),
            HubNetwork("loopback"))
        val files: SessionFiles = HubSessionFiles(connection)
        val panes: TerminalPanes = HttpTerminalPanes(connection)

        suspend fun ready() = withTimeout(8000) { connection.state.first { it == ConnectionState.Online } }
        fun respond(body: String, status: Int = 200) { replies.add(MockResponse().setResponseCode(status).setBody(body)) }
        fun respond(bytes: ByteArray) { replies.add(MockResponse().setBody(okio.Buffer().write(bytes))) }
        fun next(method: String, path: String): RecordedRequest {
            val r = requireNotNull(requests.poll(2, TimeUnit.SECONDS))
            assertEquals(method, r.method)
            assertEquals("/api/v1/machines/studio%2Fone/$path", r.requestUrl!!.encodedPath)
            assertEquals("Bearer fixture-$id", r.getHeader("Authorization"))
            return r
        }
        fun assertNoRequests() = assertNull(requests.poll())
        override fun close() { connection.close(); hub.close() }
    }

    private fun RecordedRequest.hasJson(expected: String) =
        assertEquals(Json.parseToJsonElement(expected), Json.parseToJsonElement(body.readUtf8()))

    private suspend fun refused(status: Int, code: String?, action: suspend () -> Unit): HubFailure {
        val error = runCatching { action() }.exceptionOrNull()
        if (error !is HubFailure) throw AssertionError("Expected HTTP $status ($code)", error)
        assertEquals(status, error.status)
        assertEquals(code, error.code)
        return error
    }

    @Test fun listsAndFindsRelativeToTheDisplayedFolderAndKeepsLegacyInfoUseful() = runBlocking {
        Fixture("files").use { hub ->
            hub.ready()
            val path = "folder/link #?&"
            hub.respond("""{"path":"/home/demo","entries":[{"name":"good","dir":true,"symlink":true,"link_target":"../good"},{"name":"gone","symlink":true,"link_target":"x","link_broken":true},{"name":"old"}]}""")
            val entries = hub.files.directory(mid, cwd, ".").entries
            assertTrue(entries[0].dir && entries[0].symlink)
            assertEquals("../good", entries[0].linkTarget)
            assertTrue(entries[1].linkBroken)
            assertFalse(entries[2].linkBroken)
            hub.next("GET", "fs")

            hub.respond("""{"cwd":"ignored","files":["子目录/a b.kt","other"],"truncated":true,"source":"git"}""")
            assertEquals(SessionFileSearch(listOf("子目录/a b.kt", "other"), true), hub.files.findFiles(mid, path))
            assertEquals(path, hub.next("GET", "fs/find").requestUrl!!.queryParameter("cwd"))

            hub.respond("""{"stats":[{"path":"other","size":90},{"path":"folder/link #?&","kind":"dir","size":0,"mode":0,"items":0,"items_more":false,"owner":"me","group":"team","mtime":"modified","ctime":"changed","birthtime":"created","symlink":true,"link_target":"../docs","link_real":"/home/demo/docs","link_broken":false,"mime":"text/plain"}]}""")
            val info = hub.files.fileInfo(mid, cwd, path)
            assertEquals(SessionPathStat(path, "dir", mode = 0, owner = "me", group = "team",
                mtime = "modified", ctime = "changed", birthtime = "created", items = 0, itemsMore = false,
                symlink = true, linkTarget = "../docs", linkReal = "/home/demo/docs", linkBroken = false, mime = "text/plain"), info)
            val query = hub.next("GET", "stat").requestUrl!!
            assertEquals(path, query.queryParameter("path"))
            assertEquals(cwd, query.queryParameter("cwd"))
            assertEquals("1", query.queryParameter("detail"))

            hub.respond("""{"stats":[{"path":"exact","kind":"file","size":7}]}""")
            assertEquals(SessionPathStat("exact", "file", 7), hub.files.fileInfo(mid, cwd, "exact"))
            hub.next("GET", "stat")
            hub.respond("""{"stats":[{"path":"other","kind":"file","size":7}]}""")
            assertEquals(HubError.Protocol, (runCatching { hub.files.fileInfo(mid, cwd, "exact") }.exceptionOrNull() as HubFailure).kind)
            hub.next("GET", "stat")
            hub.respond("""{"path":"notes","name":"notes","text":"page","next":2147483655}""")
            val page = hub.files.filePreview(mid, cwd, "notes")
            assertFalse(page.truncated)
            assertEquals(2_147_483_655L, page.next)
            hub.next("GET", "file")
            hub.respond("""{"path":"notes","name":"notes","text":"whole"}""")
            assertEquals(0L, hub.files.filePreview(mid, cwd, "notes").next)
            hub.next("GET", "file")
            assertTrue(SessionFileFeature.entries.all { hub.files.supports(mid, it) })
        }
    }

    @Test fun writesUseTheOpenedVersionAndNeverRetryAConflictOnTheirOwn() = runBlocking {
        Fixture("writer").use { hub ->
            hub.ready()
            val path = "notes/a b.txt"
            val openedHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // SHA-256 of "abc"
            hub.respond("""{"path":"/home/demo/new"}""")
            assertEquals("/home/demo/new", hub.files.mkdir(mid, cwd, "~/documents", "new"))
            hub.next("POST", "files/mkdir").hasJson("""{"dir":"~/documents","cwd":"$cwd","name":"new"}""")
            hub.respond("""{"path":"/home/demo/new.txt","size":0,"revision":"first"}""")
            assertEquals("first", hub.files.writeText(mid, cwd, "new.txt", "", create = true).revision)
            hub.next("POST", "files/write").hasJson("""{"path":"new.txt","cwd":"$cwd","text":"","create":true}""")
            hub.respond("""{"error":"Name already exists","code":"exists","name":"a b.txt"}""", 409)
            refused(409, "exists") { hub.files.writeText(mid, cwd, path, "", create = true) }
            hub.next("POST", "files/write").hasJson("""{"path":"$path","cwd":"$cwd","text":"","create":true}""")

            hub.respond("""{"error":"Changed since open","code":"file_changed","revision":"r2","mtime":"now"}""", 409)
            val stale = refused(409, "file_changed") { hub.files.writeText(mid, cwd, path, "my draft", revision = "r1", sha256 = openedHash) }
            assertEquals("Changed since open", stale.message)
            assertEquals(Json.parseToJsonElement("""{"error":"Changed since open","code":"file_changed","revision":"r2","mtime":"now"}"""),
                Json.parseToJsonElement(requireNotNull(stale.body)))
            hub.next("POST", "files/write").hasJson("""{"path":"$path","cwd":"$cwd","text":"my draft","revision":"r1","sha256":"$openedHash"}""")
            assertTrue(runCatching { hub.files.writeText(mid, cwd, path, "unguarded") }.exceptionOrNull() is IllegalArgumentException)
            hub.assertNoRequests()

            hub.respond("""{"path":"/home/demo/a b.txt","size":8,"revision":"r3","mtime":"now"}""")
            assertEquals(SessionWrittenFile("/home/demo/a b.txt", 8, "r3", "now"), hub.files.writeText(mid, cwd, path, "my draft", force = true))
            hub.next("POST", "files/write").hasJson("""{"path":"$path","cwd":"$cwd","text":"my draft","force":true}""")
            hub.respond("""{"path":"/home/demo/a b.txt","size":4,"revision":"r4"}""")
            assertEquals("r4", hub.files.writeText(mid, cwd, path, "edit", revision = "r3").revision)
            hub.next("POST", "files/write").hasJson("""{"path":"$path","cwd":"$cwd","text":"edit","revision":"r3"}""")
            hub.respond("""{"path":"/home/demo/a b.txt","size":4,"revision":"r5"}""")
            assertEquals("r5", hub.files.writeText(mid, cwd, path, "next", sha256 = openedHash).revision)
            hub.next("POST", "files/write").hasJson("""{"path":"$path","cwd":"$cwd","text":"next","sha256":"$openedHash"}""")
            hub.respond("""{"error":"Old node","code":"node_outdated"}""", 409)
            refused(409, "node_outdated") { hub.files.mkdir(mid, cwd, ".", "new") }
            hub.next("POST", "files/mkdir")
            hub.assertNoRequests()
        }
    }

    @Test fun uploadsTheFileBytesOnlyToTheChosenFolderAndKeepsTheServerName() = runBlocking {
        val bytes = ByteArray(260_001) { (it % 251).toByte() }
        val local = File.createTempFile("persona-files", ".bin")
        try {
            local.writeBytes(bytes)
            Fixture("upload").use { hub ->
                hub.ready()
                val dir = "~/folder #&"
                val name = "图 &?.bin"
                hub.respond("""{"error":"Name already exists","code":"exists"}""", 409)
                refused(409, "exists") { hub.files.uploadFile(mid, cwd, dir, name, local) }
                val first = hub.next("POST", "files")
                assertEquals("application/octet-stream", first.getHeader("Content-Type"))
                assertEquals(bytes.size.toString(), first.getHeader("Content-Length"))
                assertEquals(bytes.size.toLong(), first.bodySize)
                assertArrayEquals(bytes, first.body.readByteArray())
                assertEquals("dir", first.requestUrl!!.queryParameter("dest"))
                assertEquals(dir, first.requestUrl!!.queryParameter("dir"))
                assertEquals(cwd, first.requestUrl!!.queryParameter("cwd"))
                assertEquals(name, first.requestUrl!!.queryParameter("name"))
                assertNull(first.requestUrl!!.queryParameter("conflict"))

                hub.respond("""{"path":"/home/demo/report 2.bin","name":"report 2.bin","size":260001}""")
                assertEquals(SessionUploadedFile("/home/demo/report 2.bin", "report 2.bin", 260001),
                    hub.files.uploadFile(mid, cwd, dir, name, local, SessionFileConflict.KeepBoth))
                assertEquals("keep", hub.next("POST", "files").requestUrl!!.queryParameter("conflict"))
                hub.respond("""{"error":"This folder is read-only","code":"read_only"}""", 409)
                refused(409, "read_only") { hub.files.uploadFile(mid, cwd, dir, name, local, SessionFileConflict.Replace) }
                assertEquals("replace", hub.next("POST", "files").requestUrl!!.queryParameter("conflict"))
                hub.assertNoRequests()
            }
        } finally { local.delete() }
    }

    @Test fun duplicatesMovesAndArchivesKeepTheSelectedPathsAndConflictChoices() = runBlocking {
        Fixture("transfer").use { hub ->
            hub.ready()
            val paths = listOf("draft &.txt", "folder")
            hub.respond("""{"copied":[{"from":"draft &.txt","to":"draft & 2.txt"},{"from":"folder","to":"folder 2"}]}""")
            assertEquals(listOf(SessionFileChange(paths[0], "draft & 2.txt"), SessionFileChange(paths[1], "folder 2")),
                hub.files.copyFiles(mid, cwd, paths, cwd, SessionFileConflict.KeepBoth))
            hub.next("POST", "files/copy").hasJson("""{"paths":["draft &.txt","folder"],"cwd":"$cwd","dir":"$cwd","conflict":"keep"}""")
            hub.respond("""{"error":"Name taken","code":"exists"}""", 409)
            refused(409, "exists") { hub.files.move(mid, cwd, paths, "~/out") }
            hub.next("POST", "files/move").hasJson("""{"paths":["draft &.txt","folder"],"cwd":"$cwd","dir":"~/out"}""")
            hub.respond("""{"moved":[{"from":"draft &.txt","to":"~/out/draft &.txt"},{"from":"folder","to":"~/out/folder"}]}""")
            assertEquals(listOf(SessionMovedFile(paths[0], "~/out/draft &.txt"), SessionMovedFile(paths[1], "~/out/folder")),
                hub.files.move(mid, cwd, paths, "~/out", SessionMoveConflictPolicy.Replace))
            hub.next("POST", "files/move").hasJson("""{"paths":["draft &.txt","folder"],"cwd":"$cwd","dir":"~/out","conflict":"replace"}""")

            for (dir in listOf(null, "~/saved")) {
                hub.respond("""{"path":"/result/Archive.zip","name":"Archive.zip","size":224,"files":5}""")
                assertEquals(SessionZippedFile("/result/Archive.zip", "Archive.zip", 224, 5), hub.files.zipFiles(mid, cwd, paths, "Archive", dir))
                val request = hub.next("POST", "files/zip")
                if (dir == null) request.hasJson("""{"paths":["draft &.txt","folder"],"cwd":"$cwd","name":"Archive"}""")
                else request.hasJson("""{"paths":["draft &.txt","folder"],"cwd":"$cwd","name":"Archive","dir":"~/saved"}""")
            }
            hub.respond("""{"path":"/actual","mode":420}""")
            assertEquals(420, hub.files.chmod(mid, cwd, "draft &.txt", 420))
            hub.next("POST", "files/chmod").hasJson("""{"path":"draft &.txt","cwd":"$cwd","mode":420}""")
            assertTrue(runCatching { hub.files.chmod(mid, cwd, "draft &.txt", 512) }.exceptionOrNull() is IllegalArgumentException)
            hub.respond("""{"error":"Unsupported on Windows","code":"unsupported"}""", 409)
            refused(409, "unsupported") { hub.files.chmod(mid, cwd, "draft", 0) }
            hub.next("POST", "files/chmod")
            hub.assertNoRequests()
        }
    }

    @Test fun downloadsTheTemporaryZipThroughTheSamePairingWithoutCreatingAFileInTheBrowsedFolder() = runBlocking {
        Fixture("zip-download").use { hub ->
            hub.ready()
            val content = java.io.ByteArrayOutputStream()
            java.util.zip.ZipOutputStream(content).use { zip ->
                zip.putNextEntry(java.util.zip.ZipEntry("folder/a.txt"))
                zip.write("synthetic text".toByteArray())
                zip.closeEntry()
            }
            val bytes = content.toByteArray()
            val length = bytes.size
            hub.respond("""{"path":"/tmp/persona-zips/one.zip","name":"one.zip","size":$length,"files":2}""")
            val archive = hub.files.zipFiles(mid, cwd, listOf("folder"), "one.zip")
            hub.next("POST", "files/zip").hasJson("""{"paths":["folder"],"cwd":"$cwd","name":"one.zip"}""")
            hub.respond(bytes)
            val local = File.createTempFile("persona-download", ".zip")
            try {
                assertEquals(archive.size, hub.files.download(mid, cwd, archive.path, local))
                assertArrayEquals(bytes, local.readBytes())
            } finally { local.delete() }
            assertEquals(archive.path, hub.next("GET", "file/download").requestUrl!!.queryParameter("path"))
            hub.assertNoRequests()
        }
    }

    @Test fun folderShellAndFileOperationsKeepThePairedMachineAndItsManager() = runBlocking {
        Fixture("home", listOf("file_write")).use { home -> Fixture("other", listOf("pane_cwd", "file_copy")).use { other ->
            home.ready(); other.ready()
            assertTrue(home.files.supports(mid, SessionFileFeature.Write))
            assertFalse(other.files.supports(mid, SessionFileFeature.Write))
            assertTrue(other.files.supports(mid, SessionFileFeature.PaneCwd))
            assertFalse(home.files.supports(mid, SessionFileFeature.PaneCwd))
            withContext(RequestManager("manager-b")) {
                other.respond("""{"pane":"w:p1","terminal":"pane:w:p1"}""")
                assertEquals("pane:w:p1", other.panes.openInFolder(mid, "~/old folder").terminal)
                val terminal = other.next("POST", "panes")
                terminal.hasJson("""{"cwd":"~/old folder"}""")
                assertEquals("manager-b", terminal.requestUrl!!.queryParameter(ManagerQuery))
                other.respond("""{"pane":"w:p3","terminal":"pane:w:p3"}""")
                assertEquals("w:p3", other.panes.openInFolder(mid, "~/work", "agent-workspace").pane)
                other.next("POST", "panes").hasJson("""{"cwd":"~/work","workspace_id":"agent-workspace"}""")
                other.respond("""{"error":"Full device required","code":"forbidden"}""", 403)
                refused(403, "forbidden") { other.files.copyFiles(mid, cwd, listOf("a"), "~/out") }
                assertEquals("manager-b", other.next("POST", "files/copy").requestUrl!!.queryParameter(ManagerQuery))
            }
            home.assertNoRequests()
            home.respond("""{"path":"/home/demo/new"}""")
            assertEquals("/home/demo/new", home.files.mkdir(mid, cwd, ".", "new"))
            assertNull(home.next("POST", "files/mkdir").requestUrl!!.queryParameter(ManagerQuery))
            other.connection.revoke()
            assertEquals(HubError.PairingExpired, (runCatching { other.files.findFiles(mid, cwd) }.exceptionOrNull() as HubFailure).kind)
            other.assertNoRequests()
            home.assertNoRequests()
        } }
    }
}
