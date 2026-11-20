package persona.feature.files

import androidx.annotation.StringRes
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import persona.core.contracts.api.SessionFileContent
import persona.core.contracts.api.SessionFiles
import persona.core.contracts.api.hubCodedFailure
import java.security.MessageDigest

internal const val FILE_EDIT_BYTES = 1 shl 20
internal const val FILE_WRITE_BYTES = 4 * FILE_EDIT_BYTES // The node allows an edited draft to grow after opening.

/** The node omits the empty UTF-8 string from JSON. Its MIME still identifies a new empty text file. */
internal fun editableText(file: SessionFileContent): String? = file.text ?: "".takeIf {
    file.size == 0L && !file.isDir && !file.stream && file.data == null &&
        (file.mime.orEmpty().startsWith("text/") || file.mime.orEmpty().contains("json"))
}

@StringRes
internal fun fileEditProblem(file: SessionFileContent): Int? = when {
    file.isDir || editableText(file) == null -> R.string.files_editor_not_text
    file.size > FILE_EDIT_BYTES -> R.string.files_editor_too_large
    file.truncated || file.next > 0 -> R.string.files_editor_partial
    else -> null
}

/** Preserve the opening bytes, including a UTF-8 BOM and the file's choice of line breaks. */
internal class OpenedFileText(private val source: String) {
    private val bom = source.startsWith('\uFEFF')
    private val withoutMark = source.removePrefix("\uFEFF")
    private val crlf = withoutMark.contains("\r\n") &&
        withoutMark.indices.none { withoutMark[it] == '\n' && (it == 0 || withoutMark[it - 1] != '\r') }
    val body = if (crlf) withoutMark.replace("\r\n", "\n") else withoutMark
    val sha256: String by lazy { MessageDigest.getInstance("SHA-256").digest(source.toByteArray()).joinToString("") { "%02x".format(it) } }

    fun encoded(edited: String) = (if (bom) "\uFEFF" else "") + if (crlf) edited.replace("\n", "\r\n") else edited
}

internal class FileEditorDocument(val file: SessionFileContent, private val api: SessionFiles,
    private val machine: String, private val cwd: String) {
    val opened = OpenedFileText(requireNotNull(editableText(file)))

    suspend fun save(edited: String, force: Boolean = false) {
        val (text, digest) = withContext(Dispatchers.Default) { opened.encoded(edited) to opened.sha256 }
        api.writeText(machine, cwd, file.path, text, revision = file.revision, sha256 = digest, force = force)
    }

    /** Atomically create the first free sibling. A concurrent creator can take a candidate between attempts. */
    suspend fun saveCopy(edited: String): String? {
        val text = withContext(Dispatchers.Default) { opened.encoded(edited) }
        val split = file.name.lastIndexOf('.').takeIf { it > 0 } ?: file.name.length
        val separator = file.path.lastIndexOfAny(charArrayOf('/', '\\'))
        val parent = if (separator >= 0) file.path.take(separator + 1) else
            cwd.trimEnd('/', '\\') + if ('\\' in cwd && '/' !in cwd) "\\" else "/"
        for (number in 2..99) {
            val name = file.name.take(split) + " " + number + file.name.drop(split)
            try {
                api.writeText(machine, cwd, parent + name, text, create = true)
                return name
            } catch (failure: Exception) {
                if (!fileEditorConflict(failure, "exists")) throw failure
            }
        }
        return null
    }
}

internal fun fileEditorConflict(error: Throwable, code: String) =
    hubCodedFailure(error)?.let { it.status == 409 && it.code == code } == true
