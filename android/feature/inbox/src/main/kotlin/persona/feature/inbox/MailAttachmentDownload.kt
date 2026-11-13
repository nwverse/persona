package persona.feature.inbox

import kotlinx.coroutines.withContext
import persona.core.contracts.api.MailAttachment
import persona.core.contracts.api.ManagerPins
import persona.core.contracts.api.MailReference
import persona.core.contracts.api.MailTransport
import persona.core.contracts.api.pinned
import java.io.File

/** Keeps the cached file alive until the viewer returns; its descriptor has a separate lease. */
interface MailAttachmentFile : AutoCloseable {
    val file: File
}

/** Mail transport decorated by the pairing's budgeted media store. */
interface MailAttachmentTransport : MailTransport {
    suspend fun downloadAttachmentFile(ref: MailReference, uid: Long, attachment: MailAttachment,
        progress: (Long, Long?) -> Unit = { _, _ -> }): MailAttachmentFile
}

/** This transport with a page's [pins] on its requests (MailTransport.pinned), still a [MailAttachmentTransport] when it was one. */
internal fun MailTransport.pinnedForPage(pins: ManagerPins?): MailTransport {
    val pinned = pinned(pins)
    val files = this as? MailAttachmentTransport
    return if (pins == null || files == null) pinned else object : MailAttachmentTransport, MailTransport by pinned {
        override suspend fun downloadAttachmentFile(ref: MailReference, uid: Long, attachment: MailAttachment,
            progress: (Long, Long?) -> Unit) = withContext(pins) { files.downloadAttachmentFile(ref, uid, attachment, progress) }
    }
}
