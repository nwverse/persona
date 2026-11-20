package persona.core.contracts.api

import kotlinx.serialization.Serializable

/** Hello feature names for the Files pane, evaluated on the machine's own pairing. */
enum class SessionFileFeature(val wire: String) {
    Info("file_info"), Mkdir("file_mkdir"), Write("file_write"), Upload("upload_dir"), Download("file_download"),
    Edit("file_edit"), Copy("file_copy"), Zip("file_zip"), Chmod("file_chmod"), PaneCwd("pane_cwd"),
}

/** Upload, copy and move use the same explicit conflict choices on the wire. */
typealias SessionFileConflict = SessionMoveConflictPolicy

@Serializable data class SessionFileSearch(val files: List<String> = emptyList(), val truncated: Boolean = false)
@Serializable data class SessionWrittenFile(val path: String, val size: Long, val revision: String, val mtime: String? = null)
@Serializable data class SessionUploadedFile(val path: String, val name: String, val size: Long)
@Serializable data class SessionZippedFile(val path: String, val name: String, val size: Long, val files: Int)
@Serializable data class SessionFileChange(val from: String, val to: String)
@Serializable data class SessionCopiedFiles(val copied: List<SessionFileChange> = emptyList())
@Serializable data class SessionFileMode(val path: String, val mode: Int)
