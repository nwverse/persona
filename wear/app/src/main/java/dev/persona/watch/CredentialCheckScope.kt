package dev.persona.watch

import java.io.File

/** Only the disposable direct child matching this isolated alias may hold test credentials. */
internal fun credentialCheckFile(root: File, directory: File, alias: String): File {
    require(alias.matches(Regex("persona\\.watch\\.fixture\\.loss\\.[a-f0-9-]{36}")))
    val safe = directory.canonicalFile
    require(safe.isDirectory && safe.parentFile == root.canonicalFile &&
        safe.name == "credential-loss-" + alias.removePrefix("persona.watch.fixture.loss."))
    return File(safe, "connection.enc")
}
