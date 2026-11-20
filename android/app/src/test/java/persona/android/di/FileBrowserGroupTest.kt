package persona.android.di

import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import persona.android.ui.folderTerminalRoute
import persona.core.contracts.GroupKeys
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import persona.core.navigation.TerminalRoute

class FileBrowserGroupTest {
    private class Computer : SessionFiles {
        val writes = mutableListOf<List<Any?>>()
        override fun supports(machineId: String, feature: SessionFileFeature) = machineId == "desk"
        override suspend fun directory(machineId: String, cwd: String, path: String) = error("unused")
        override suspend fun file(machineId: String, cwd: String, path: String) = error("unused")
        override suspend fun status(machineId: String, cwd: String) = error("unused")
        override suspend fun diff(machineId: String, cwd: String, path: String, staged: Boolean) = error("unused")
        override suspend fun log(machineId: String, cwd: String, skip: Int, limit: Int) = error("unused")
        override suspend fun show(machineId: String, cwd: String, hash: String) = error("unused")
        override suspend fun findFiles(machineId: String, folder: String) = SessionFileSearch(listOf("$machineId/$folder"))
        override suspend fun fileInfo(machineId: String, cwd: String, path: String) = SessionPathStat(path, "file", mode = 420)
        override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean, revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
            writes += listOf(machineId, cwd, path, text, create, revision, sha256, force)
            return SessionWrittenFile(path, text.length.toLong(), "new")
        }
        override suspend fun chmod(machineId: String, cwd: String, path: String, mode: Int): Int {
            writes += listOf(machineId, cwd, path, mode)
            return mode
        }
    }

    @Test fun sameNamedComputersUseTheirOwnReadAndWriteContractsAndAnUnpairedSuffixIsOffline() = runBlocking {
        val first = Computer()
        val second = Computer()
        val names = GroupNames("g0a1b2c3d")
        val secondary = names.machineId("desk")
        var paired = true
        val other = second.inGroup(names)
        val api = routedFiles { mid ->
            when (GroupKeys.namespaceOf(mid)) { null -> first; names.namespace -> other.takeIf { paired }; else -> null }
        }
        val path = "/home/fixture/read me.md"
        assertTrue(api.supports(secondary, SessionFileFeature.Write))
        assertEquals(listOf("desk/notes"), api.findFiles(secondary, "notes").files)
        assertEquals(420, api.fileInfo(secondary, "cwd", path).mode)
        assertEquals(path, api.writeText(secondary, "cwd", path, "exact text", revision = "revision", sha256 = "hash").path)
        assertEquals(493, api.chmod("desk", "cwd", path, 493))
        assertEquals(listOf(listOf("desk", "cwd", path, "exact text", false, "revision", "hash", false)), second.writes)
        assertEquals(listOf(listOf("desk", "cwd", path, 493)), first.writes)
        paired = false
        assertFalse(api.supports(secondary, SessionFileFeature.Write))
        try { api.chmod(secondary, "cwd", path, 0); fail("expected offline") }
        catch (failure: HubFailure) { assertEquals(HubError.NoNetwork, failure.kind) }
        assertEquals(1, first.writes.size)
        assertEquals(1, second.writes.size)
        val key = SessionKey(secondary, "pane:p1")
        assertFalse(unpairedTerminal.subscribe(key, null, 0, null))
        assertFalse(unpairedTerminal.input(key, "discarded"))
    }

    @Test fun folderShellUsesTheReturnedPaneOnTheOriginatingPairedMachine() = runBlocking {
        val requests = mutableListOf<List<String?>>()
        val panes = object : TerminalPanes {
            override val available = flowOf(true)
            override suspend fun panes(machineId: String) = error("unused")
            override suspend fun newTab(machineId: String, workspaceId: String, kind: String?, cwd: String?) = error("unused")
            override suspend fun openInFolder(machineId: String, cwd: String, workspaceId: String?): NewPaneTab {
                requests += listOf(machineId, cwd, workspaceId)
                return NewPaneTab("local-pane", "pane:returned-address")
            }
        }
        val machine = "desk~g0a1b2c3d"
        val path = "/home/fixture/new project"
        assertEquals(TerminalRoute(machine, "pane:returned-address", path), folderTerminalRoute(panes, machine, path, "s2"))
        folderTerminalRoute(panes, machine, path, " ")
        assertEquals(listOf(listOf(machine, path, "s2"), listOf(machine, path, null)), requests)
    }
}
