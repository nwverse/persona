package persona.feature.files

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.Density
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.ChatRoute
import persona.core.hub.HubFailure
import persona.core.hub.HubError
import java.io.File

private const val HOME = "/home/demo"
private const val WORK = "$HOME/Studio"

private class BrowserFixture(val detail: Boolean = true) : SessionFiles {
    val find = mutableListOf<Pair<String, String>>()
    val info = mutableListOf<String>()
    var infoFailure: Exception? = null
    val opened = mutableListOf<String>()
    val copies = mutableListOf<Pair<List<String>, String>>()
    private val timestamp = "2026-11-04T09:15:00Z"
    override fun supports(machineId: String, feature: SessionFileFeature) = detail
    override fun editsFiles(machineId: String) = true
    override suspend fun directory(machineId: String, cwd: String, path: String) = SessionDirectory(path, when (path) {
        WORK -> listOf(SessionFileEntry(".review.txt", size = 15), SessionFileEntry("Clips", dir = true), SessionFileEntry("Current", dir = true, symlink = true, linkTarget = "Clips"),
            SessionFileEntry("Draft.txt", size = 1536, mtime = timestamp), SessionFileEntry("Missing", symlink = true, linkTarget = "Past", linkBroken = true))
        "$WORK/Clips", "$WORK/Current" -> listOf(SessionFileEntry("Notes.txt", size = 42))
        HOME -> listOf(SessionFileEntry("Studio", dir = true), SessionFileEntry("Library", dir = true))
        "/home" -> listOf(SessionFileEntry("demo", dir = true), SessionFileEntry("Shared.txt", size = 512))
        else -> emptyList()
    })
    override suspend fun findFiles(machineId: String, folder: String): SessionFileSearch {
        find += machineId to folder
        return SessionFileSearch(listOf("Clips/Notes.txt", "Clips/Ideas/Work.txt", "Draft.txt"), truncated = true)
    }
    override suspend fun fileInfo(machineId: String, cwd: String, path: String): SessionPathStat {
        info += path
        infoFailure?.let { throw it }
        return if (path.endsWith("/Current")) SessionPathStat(path, "dir", symlink = true, linkTarget = "Clips",
            linkReal = "$WORK/Clips", items = 2, mode = 493, owner = "demo", group = "studio", mtime = timestamp)
        else SessionPathStat(path, "file", size = 1536, mode = 420, owner = "demo", group = "studio", birthtime = timestamp,
            ctime = timestamp, mtime = timestamp)
    }
    override suspend fun file(machineId: String, cwd: String, path: String): SessionFileContent {
        opened += path
        return SessionFileContent(path, FilesPaths.name(path), size = 15, text = "Synthetic notes")
    }
    override suspend fun copyFiles(machineId: String, cwd: String, paths: List<String>, dir: String, conflict: SessionFileConflict?): List<SessionFileChange> {
        copies += paths to dir
        return paths.map { SessionFileChange(it, joinRemotePath(dir, FilesPaths.name(it))) }
    }
    override suspend fun status(machineId: String, cwd: String) = SessionGitStatus(false)
    override suspend fun diff(machineId: String, cwd: String, path: String, staged: Boolean) = SessionGitDiff(path, "")
    override suspend fun log(machineId: String, cwd: String, skip: Int, limit: Int) = SessionGitLog()
    override suspend fun show(machineId: String, cwd: String, hash: String) = SessionGitShow(SessionGitCommit(hash))
}

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class FilesBrowserRouteTest {
    @get:Rule val rule = createComposeRule()

    private var dark by mutableStateOf(false)
    private var style by mutableStateOf(AppStyle.Glass)
    private var large by mutableStateOf(false)
    private var home by mutableStateOf<String?>(HOME)
    private fun screen(machine: BrowserFixture, os: String = "linux", initialPath: String? = null) {
        rule.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, if (large) 2f else 1f)) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.fillMaxSize().testTag("browser-shot").background(PersonaTheme.palette.background)) {
                        SessionFilesScreen(ChatRoute("paired-demo", "s1"), WORK, machine, {}, {},
                            initialPath = initialPath, machineHome = home, machineOs = os)
                    }
                }
            }
        }
        if (initialPath == null) rule.onNodeWithText("Clips").assertExists()
    }
    private fun menu(name: String) = rule.onNodeWithContentDescription("Actions for $name").performClick()
    private fun tapAction(label: String) = rule.onAllNodes(hasText(label) and hasClickAction()).onLast().performClick()

    private fun capture(name: String, modal: Boolean = false) {
        val directory = System.getenv("PERSONA_FILES_BROWSER_SHOTS")?.let(::File) ?: return
        directory.mkdirs()
        val activity = rule.onNodeWithTag("browser-shot").captureToImage().asAndroidBitmap()
        val bitmap = if (!modal) activity else rule.runOnIdle {
            // Compose's dialog capture can crop the Activity. Draw the actual sheet window over its own Activity.
            val decor = checkNotNull(org.robolectric.shadows.ShadowDialog.getLatestDialog().window).decorView
            val result = activity.copy(Bitmap.Config.ARGB_8888, true)
            decor.draw(android.graphics.Canvas(result))
            result
        }
        val file = File(directory, "$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}-${if (large) "large" else "standard"}.png")
        file.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun captureThemes(name: String, modal: Boolean = false) {
        for (isDark in listOf(false, true)) for (isLarge in listOf(false, true)) {
            rule.runOnIdle { dark = isDark; large = isLarge }
            capture(name, modal)
        }
        rule.runOnIdle { dark = false; large = false }
    }

    @Test fun theSessionRouteSearchesItsFolderOnceAndOpensTheMatchedFile() {
        val machine = BrowserFixture()
        screen(machine)
        rule.onNodeWithContentDescription("Find in this folder").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("note")
        rule.waitUntil { rule.onAllNodesWithText("Notes.txt").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Clips").assertExists()
        captureThemes("find")
        rule.onNode(hasSetTextAction()).performTextReplacement("notes.txt")
        rule.waitUntil { rule.onAllNodesWithText("Notes.txt").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Notes.txt").performClick()
        rule.waitUntil { machine.opened.isNotEmpty() }
        assertEquals(listOf("paired-demo" to WORK), machine.find)
        assertEquals(listOf("$WORK/Clips/Notes.txt"), machine.opened)
    }

    @Test fun findOpensAnAncestorFolderAndNavigationRetainsHistoryAcrossRefreshes() {
        val machine = BrowserFixture()
        screen(machine)
        rule.onNodeWithContentDescription("Find in this folder").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("ideas")
        rule.waitUntil { rule.onAllNodesWithText("Ideas").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Ideas").performClick()
        rule.onNodeWithText("$WORK/Clips/Ideas").assertExists()
        rule.onNodeWithContentDescription("Previous folder").performClick()
        rule.onNodeWithText("Studio · Session").assertExists()
        rule.onNodeWithContentDescription("Next folder").performClick()
        rule.onNodeWithContentDescription("Home folder").performClick()
        rule.onNodeWithContentDescription("Next folder").assertIsNotEnabled()
        rule.onNodeWithContentDescription("Up one level").performClick()
        rule.onNodeWithText("Read-only location").assertExists()
        menu("demo")
        rule.onNodeWithText("Rename").assertDoesNotExist()
        rule.onNodeWithText("Move to Trash").assertDoesNotExist()
        rule.onNodeWithText("Info").assertExists()
    }

    @Test fun linkKeepsItsOwnFolderPathAndInfoExplicitlyOpensTheRealLocation() {
        val machine = BrowserFixture()
        screen(machine)
        rule.onNodeWithText("Broken link → Past").assertExists()
        rule.onNodeWithText("Current").performClick()
        rule.onNodeWithText("$WORK/Current").assertExists()
        rule.onNodeWithContentDescription("Previous folder").performClick()
        menu("Current")
        tapAction("Info")
        rule.onNodeWithText("Link to folder").assertExists()
        rule.onNodeWithText("2 items").assertExists()
        rule.onNodeWithText("rwxr-xr-x · 755").assertExists()
        for (isDark in listOf(false, true)) {
            rule.runOnIdle { dark = isDark }
            capture("info", modal = true)
        }
        tapAction("Go to real location")
        rule.onNodeWithText("$WORK/Clips").assertExists()
        assertEquals(listOf("$WORK/Current"), machine.info)
    }

    @Test fun aLegacyHubShowsBasicInfoFromTheListing() {
        val machine = BrowserFixture(detail = false)
        screen(machine)
        menu("Draft.txt")
        tapAction("Info")
        rule.onNodeWithText("1.5 KB (1,536 bytes)").assertExists()
        rule.onNodeWithText("Update the hub to see more file information.").assertExists()
        rule.onNodeWithText("Copy path").assertExists()
        assertTrue(machine.info.isEmpty())
    }

    @Test fun aLegacyUnadvertisedStatRouteKeepsBasicInfoAndNamesTheUpgrade() {
        val machine = BrowserFixture()
        machine.infoFailure = HubFailure(HubError.Server, "HTTP 404", 404)
        screen(machine)
        menu("Draft.txt")
        tapAction("Info")
        rule.onNodeWithText("Update the hub to see more file information.").assertExists()
        rule.onNodeWithText("1.5 KB (1,536 bytes)").assertExists()
        rule.onNodeWithText("Retry").assertDoesNotExist()
    }

    @Test @Config(fontScale = 2f)
    fun systemLargeTextInfoCanScrollToThePermissionsInBothThemes() {
        val resources = ApplicationProvider.getApplicationContext<android.content.Context>().resources
        assertEquals(2f, resources.configuration.fontScale, 0.01f)
        large = true
        screen(BrowserFixture())
        for (isDark in listOf(false, true)) {
            rule.runOnIdle { dark = isDark }
            menu("Current")
            tapAction("Info")
            rule.onNodeWithText("Link to folder").assertIsDisplayed()
            capture("info", modal = true)
            rule.onNodeWithText("rwxr-xr-x · 755").performScrollTo().assertIsDisplayed()
            capture("info-permissions", modal = true)
            rule.onNodeWithText("Go to real location").performScrollTo().performClick()
            rule.onNodeWithContentDescription("Previous folder").performClick()
        }
    }

    @Test @Config(qualifiers = "zh-w400dp-h800dp-xhdpi")
    fun simplifiedChineseFindUsesTheSessionFolder() = localizedFind("在此文件夹中查找", "文件或文件夹名称")

    @Test @Config(qualifiers = "ja-w400dp-h800dp-xhdpi")
    fun japaneseFindUsesTheSessionFolder() = localizedFind("このフォルダ内を検索", "ファイル名またはフォルダ名")

    private fun localizedFind(action: String, placeholder: String) {
        val machine = BrowserFixture()
        screen(machine)
        rule.onNodeWithContentDescription(action).performClick()
        rule.onNodeWithText(placeholder).assertExists()
        rule.onNode(hasSetTextAction()).performTextInput("Notes")
        rule.waitUntil { rule.onAllNodesWithText("Notes.txt").fetchSemanticsNodes().isNotEmpty() }
        assertEquals(listOf("paired-demo" to WORK), machine.find)
    }

    @Test fun aDirectPreviewRevealsAndMarksEvenAHiddenFileInItsFolder() {
        screen(BrowserFixture(), initialPath = "$WORK/.review.txt")
        rule.onNodeWithContentDescription("Preview options").performClick()
        tapAction("Show in Files")
        rule.onNode(hasText(".review.txt") and isSelected()).assertIsDisplayed()
        rule.onNodeWithText("Studio · Session").assertExists()
    }

    @Test fun lateMachineMetadataKeepsTheUsersFolderAndUpdatesItsReadOnlyStatus() {
        home = null
        screen(BrowserFixture())
        rule.onNodeWithContentDescription("Up one level").performClick()
        rule.onNodeWithContentDescription("Up one level").performClick()
        rule.onNodeWithText("/home").assertExists()
        rule.onNodeWithText("Read-only location").assertDoesNotExist()
        rule.runOnIdle { home = HOME }
        rule.onNodeWithText("/home").assertExists()
        rule.onNodeWithText("Read-only location").assertExists()
        rule.onNodeWithContentDescription("New").assertDoesNotExist()
    }

    @Test fun windowsOmitsUnixPermissionsEvenIfModeCameBack() {
        screen(BrowserFixture(), os = "windows")
        menu("Draft.txt")
        tapAction("Info")
        rule.onNodeWithText("Metadata changed").assertExists()
        rule.onNodeWithText("Permissions").assertDoesNotExist()
        rule.onNodeWithText("Owner").assertDoesNotExist()
    }

    @Test fun copyCanReadAnOutsideSourceAndOnlyTargetsAWritableDestination() {
        val machine = BrowserFixture()
        screen(machine)
        repeat(2) { rule.onNodeWithContentDescription("Up one level").performClick() }
        menu("Shared.txt")
        rule.onNodeWithText("Move…").assertDoesNotExist()
        tapAction("Copy to…")
        rule.onNodeWithText("Copy here").assertIsNotEnabled()
        val picker = isRoot() and hasAnyDescendant(hasText("Copy here"))
        rule.onNode(hasText("demo") and hasAnyAncestor(picker)).performClick()
        rule.onNodeWithText("Copy here").assertIsEnabled().performClick()
        rule.waitUntil { machine.copies.isNotEmpty() }
        assertEquals(listOf(listOf("/home/Shared.txt") to HOME), machine.copies)
    }

    @Test fun selectingTogglesFilesWithoutOpeningTheirPreview() {
        val machine = BrowserFixture()
        screen(machine)
        menu("Draft.txt")
        tapAction("Select")
        rule.onNodeWithText("1 selected").assertExists()
        rule.onNodeWithText("Clips").performClick()
        rule.onNodeWithText("2 selected").assertExists()
        for (action in listOf("Download", "Copy to…", "Move…", "Compress", "Move to Trash"))
            rule.onNodeWithContentDescription(action).assertIsDisplayed()
        captureThemes("selection")
        tapAction("Done")
        rule.onNodeWithText("2 selected").assertDoesNotExist()
        assertTrue(machine.opened.isEmpty())
    }

    @Test fun browserAtRegularAndLargeTypeInEveryStyle() {
        screen(BrowserFixture())
        for (appearance in AppStyle.entries) for (isDark in listOf(false, true)) for (isLarge in listOf(false, true)) {
            rule.runOnIdle { style = appearance; dark = isDark; large = isLarge }
            rule.onNodeWithContentDescription("Find in this folder").assertIsDisplayed()
            rule.onNodeWithText("Studio · Session").assertIsDisplayed()
            capture("browser")
        }
    }
}
