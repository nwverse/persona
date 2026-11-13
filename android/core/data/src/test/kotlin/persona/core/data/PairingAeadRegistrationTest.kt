package persona.core.data

import com.google.crypto.tink.Aead
import com.google.crypto.tink.BinaryKeysetReader
import com.google.crypto.tink.BinaryKeysetWriter
import com.google.crypto.tink.CleartextKeysetHandle
import com.google.crypto.tink.KeysetHandle
import com.google.crypto.tink.aead.AeadKeyTemplates
import java.io.ByteArrayOutputStream
import java.lang.reflect.InvocationTargetException
import java.net.URLClassLoader
import java.security.GeneralSecurityException
import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.ProtocolCompatibility

/**
 * Start-up registers only the AEAD key managers the pairing keyset needs ([registerPairingAead]). Tink's registry is
 * global to the class loader and another test in this JVM may register all of `AeadConfig` first, so the check runs
 * on a fresh copy of Tink.
 */
class PairingAeadRegistrationTest {
    @Test fun aStoredAesGcmKeysetAndANewOneWorkWithOnlyTheStartUpRegistration() {
        val code = listOf(PairingAeadProbe::class.java, Class.forName("persona.core.data.EncryptedCredentialsKt"),
            ProtocolCompatibility::class.java, KeysetHandle::class.java, Unit::class.java)
            .map { it.protectionDomain.codeSource.location }.distinct()
        // The boot class loader as parent: nothing of this one's Tink (or its registry) is visible to the copy.
        val result = URLClassLoader(code.toTypedArray(), null).use { loader ->
            val probe = loader.loadClass(PairingAeadProbe::class.java.name)
            try { probe.getMethod("run").invoke(probe.getField("INSTANCE").get(null)) }
            catch (e: InvocationTargetException) { throw e.cause ?: e }
        }
        assertEquals(listOf(PairingAeadProbe.DOCUMENT, "new keyset round trip", "ChaCha20-Poly1305 not registered"), result)
    }
}

/** Runs inside the fresh class loader: only Tink, the app's code and the Kotlin standard library are visible. */
@Suppress("DEPRECATION")
object PairingAeadProbe {
    const val DOCUMENT = """{"version":1,"hubId":"fixture-hub","generation":"fixture-generation","discarding":false}"""

    /** An AES256_GCM keyset generated as the app generates one, and [DOCUMENT] encrypted with it as `active.enc`. */
    private const val KEYSET = "CJeRybIMEmQKWAowdHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUuY3J5cHRvLnRpbmsuQWVzR2NtS2V5EiIaIOC67LYx" +
        "yOIZHqd/wmQ0vsTTGVxPd5NXUKqMMrTkYPZLGAEQARiXkcmyDCAB"
    private const val CIPHERTEXT = "AcZSSJcHRdjhr3gU/nmdOA4QKe9kyN2Dyz/nqbKSTXnGj/0CZmL5xhG0LiEess7wgafWApZxP13uKDNuCQlsUyRs" +
        "2PdVYFmogTLSi8JeegxmYlTE5a2ElQ/uOCW1KQvlEz0EucjTJcuY3xqvw7wyjRgkh7ru57RQ5w=="

    fun run(): List<String> {
        registerPairingAead()
        val stored = CleartextKeysetHandle.read(BinaryKeysetReader.withBytes(Base64.getDecoder().decode(KEYSET)))
        val document = stored.getPrimitive(Aead::class.java)
            .decrypt(Base64.getDecoder().decode(CIPHERTEXT), "active.enc".toByteArray()).decodeToString()

        // A first start generates the keyset and keeps it wrapped by the Keystore key; a later start reads it back.
        val master = KeysetHandle.generateNew(AeadKeyTemplates.AES256_GCM).getPrimitive(Aead::class.java)
        val created = KeysetHandle.generateNew(AeadKeyTemplates.AES256_GCM)
        val wrapped = ByteArrayOutputStream().also { created.write(BinaryKeysetWriter.withOutputStream(it), master) }
        val ciphertext = created.getPrimitive(Aead::class.java).encrypt(byteArrayOf(1, 2), byteArrayOf(3))
        val restored = CredentialKeyset.read(wrapped.toByteArray()) { master }.getPrimitive(Aead::class.java)
        check(restored.decrypt(ciphertext, byteArrayOf(3)).contentEquals(byteArrayOf(1, 2)))

        val other = try { KeysetHandle.generateNew(AeadKeyTemplates.CHACHA20_POLY1305); "ChaCha20-Poly1305 registered" }
            catch (_: GeneralSecurityException) { "ChaCha20-Poly1305 not registered" }
        return listOf(document, "new keyset round trip", other)
    }
}
