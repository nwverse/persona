package persona.feature.files

import persona.core.contracts.api.SessionFileEntry
import persona.core.designsystem.JAction
import java.util.Locale

internal data class FileBrowserTarget(val path: String, val entry: SessionFileEntry)

internal data class FileBrowserActions(
    val newItems: List<JAction>,
    val folderItems: List<JAction>,
    val rowItems: (FileBrowserTarget) -> List<JAction>,
    val selectedItems: (List<FileBrowserTarget>) -> List<JAction>,
    val permissions: (FileBrowserTarget) -> JAction?,
)

internal data class FileNameHit(val path: String, val directory: Boolean) {
    val name get() = path.substringAfterLast('/')
    val folder get() = path.substringBeforeLast('/', "")
}

/** fs.find returns relative file paths; their common parents are searchable directories in their own right. */
internal fun fileNameEntries(files: List<String>): List<FileNameHit> {
    val dirs = mutableSetOf<String>()
    val entries = files.filter { path ->
        path.isNotEmpty() && path.split('/').none { it.isEmpty() || it == "." || it == ".." }
    }.distinct()
    for (file in entries) {
        var dir = file.substringBeforeLast('/', "")
        while (dir.isNotEmpty() && dirs.add(dir)) dir = dir.substringBeforeLast('/', "")
    }
    return dirs.map { FileNameHit(it, true) } + entries.map { FileNameHit(it, false) }
}

private val wordSeparator = Regex("\\s+")

/** Same ranking as the iPhone and web: each word must occur in the name, with exact and prefix matches first. */
internal fun findFileNames(entries: List<FileNameHit>, query: String, limit: Int = 200): List<FileNameHit> {
    val words = query.lowercase(Locale.ROOT).split(wordSeparator).filter(String::isNotEmpty)
    if (words.isEmpty()) return emptyList()
    return entries.mapNotNull { hit ->
        val name = hit.name.lowercase(Locale.ROOT)
        var score = 0
        for (word in words) {
            val i = name.indexOf(word)
            score += when {
                i < 0 -> return@mapNotNull null
                name == word -> 4
                i == 0 -> 3
                !Character.isLetterOrDigit(name.codePointBefore(i)) -> 2
                else -> 1
            }
        }
        hit to score
    }.sortedWith(compareByDescending<Pair<FileNameHit, Int>> { it.second }.thenByDescending { it.first.directory }
        .thenBy { it.first.path.length }.thenBy { it.first.path }).take(limit).map { it.first }
}

internal fun fileModeText(mode: Int): String = buildString {
    for (index in 0..2) {
        val bits = (mode shr (6 - 3 * index)) and 7
        append(if (bits and 4 != 0) 'r' else '-')
        append(if (bits and 2 != 0) 'w' else '-')
        val executable = bits and 1 != 0
        append(when {
            mode and (1 shl (11 - index)) == 0 -> if (executable) 'x' else '-'
            index == 2 -> if (executable) 't' else 'T'
            else -> if (executable) 's' else 'S'
        })
    }
    append(" · ").append((mode and 4095).toString(8))
}
