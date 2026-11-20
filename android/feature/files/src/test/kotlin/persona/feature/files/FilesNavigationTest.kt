package persona.feature.files

import org.junit.Assert.*
import org.junit.Test

class FilesNavigationTest {
    @Test fun pathBarStartsAtHomeOrRootAndKeepsSpacesInAComponent() {
        val user = "/Users/demo"
        assertEquals(listOf("~" to user, "project" to "$user/project", " notes " to "$user/project/ notes "),
            FilesPaths.pathBar("$user/project/ notes ", user))
        assertEquals(listOf("/" to "/", "Users" to "/Users", "demo2" to "/Users/demo2"), FilesPaths.pathBar("/Users/demo2", user))
        assertEquals(listOf("/" to "/"), FilesPaths.pathBar("/", null))
        assertEquals(listOf("~" to "~", "work" to "~/work"), FilesPaths.pathBar("~/work", null))
        assertEquals("D:/" to "D:/", FilesPaths.pathBar("D:\\Projects", "C:\\Users\\demo").first())
        assertEquals("C:\\", FilesPaths.parent("C:\\Users"))
        assertNull(FilesPaths.parent("C:\\"))
        assertEquals("C:\\Users\\demo\\Studio", joinRemotePath("C:\\Users", "demo/Studio"))
        assertEquals("C:\\Users\\demo", FilesPaths.resolve("C:\\Users\\draft\\..\\demo", "C:\\"))
    }

    @Test fun writesUsePathBoundariesAndUnknownHomeDefersToTheNode() {
        val user = "/Users/demo"
        listOf(user, "~/", "$user/Library-old", "$user/.persona-files", "$user/project").forEach {
            assertTrue(it, FilesPaths.writable(it, user))
        }
        listOf("/", "/Users/demo2", "~/.persona", "$user/Library", "$user/.persona/config", "$user/Library/Logs",
            "$user/.jarvis", "$user/library/Logs", "$user/project/../../outside", "~/../elsewhere").forEach {
            assertFalse(it, FilesPaths.writable(it, user))
        }
        assertTrue(FilesPaths.writable("/legacy", null))
        assertFalse(FilesPaths.writable("C:\\USERS\\Demo\\Library\\Logs", "c:\\Users\\Demo", "windows"))
    }

    @Test fun findRanksEveryWordInTheNameAndReturnsEachAncestorOnce() {
        val index = fileNameEntries(listOf("a/plan", "b/plan/report.md", "a/pre-plan", "plans.txt", "unplanned.md",
            "b/plan/report final.md", "b/plan/nested/summary.txt", "b/plan/report.md", "../outside"))
        assertEquals(listOf("b/plan", "a/plan", "plans.txt", "a/pre-plan", "unplanned.md"), findFileNames(index, "PLAN").map { it.path })
        assertEquals(listOf("b/plan/report final.md"), findFileNames(index, " FINAL  rep ").map { it.path })
        assertEquals(1, findFileNames(index, "rep", limit = 1).size)
        assertTrue(findFileNames(index, "  ").isEmpty())
        assertFalse(index.any { it.path == "../outside" })
    }

    @Test fun permissionModeRendersTheSameSpecialBitsAsTheOtherClients() {
        assertEquals("rwxr-xr-x · 755", fileModeText(493))
        assertEquals("rwsr-xr-T · 5754", fileModeText("5754".toInt(8)))
        assertEquals("rw-r-S--- · 2640", fileModeText("2640".toInt(8)))
    }
}
