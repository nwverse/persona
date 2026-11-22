package persona.core.data

import com.google.crypto.tink.Aead
import com.google.crypto.tink.BinaryKeysetWriter
import com.google.crypto.tink.KeysetHandle
import com.google.crypto.tink.aead.AeadKeyTemplates
import java.io.ByteArrayOutputStream
import java.security.GeneralSecurityException
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import persona.core.contracts.ProtocolCompatibility

@Suppress("DEPRECATION")
class CredentialKeysetTest {
    private val uris = ProtocolCompatibility.pairingMasterKeyUris

    @Before fun register() = registerPairingAead()

    @Test fun bothWrappingKeysRetainOldAndCurrentPairingsAcrossRepeatedStarts() {
        val masters = uris.associateWith { newHandle().getPrimitive(Aead::class.java) }
        for (uri in uris) {
            val original = newHandle()
            val wrapped = wrap(original, masters.getValue(uri))
            val originalBytes = wrapped.copyOf()
            val data = original.getPrimitive(Aead::class.java)
            val documents = listOf("active.enc", "groups.enc", "hub/generation/credentials.enc", "hub/generation/endpoints.enc")
                .associateWith { path -> data.encrypt("retained pairing".toByteArray(), path.toByteArray()) }
            repeat(3) {
                val restored = CredentialKeyset.read(wrapped) { masters.getValue(it) }.getPrimitive(Aead::class.java)
                for ((path, ciphertext) in documents) {
                    assertArrayEquals("retained pairing".toByteArray(), restored.decrypt(ciphertext, path.toByteArray()))
                    val updated = restored.encrypt("new data".toByteArray(), path.toByteArray())
                    assertArrayEquals("new data".toByteArray(), data.decrypt(updated, path.toByteArray()))
                }
                assertArrayEquals(originalBytes, wrapped)
            }
        }
    }

    @Test fun missingCurrentWrappingKeyStillReadsOriginalKeyset() {
        val oldMaster = newHandle().getPrimitive(Aead::class.java)
        val original = newHandle()
        val wrapped = wrap(original, oldMaster)
        val restored = CredentialKeyset.read(wrapped) { uri ->
            if (uri == uris.last()) oldMaster else throw GeneralSecurityException("Key unavailable")
        }.getPrimitive(Aead::class.java)
        val encrypted = original.getPrimitive(Aead::class.java).encrypt(byteArrayOf(1), byteArrayOf(2))
        assertArrayEquals(byteArrayOf(1), restored.decrypt(encrypted, byteArrayOf(2)))
    }

    @Test fun unavailableOrWrongKeysPreserveWrappedBytesAndReportBothFailures() {
        val wrapped = wrap(newHandle(), newHandle().getPrimitive(Aead::class.java))
        for (missing in listOf(true, false)) {
            val original = wrapped.copyOf()
            val lookedUp = mutableListOf<String>()
            try {
                CredentialKeyset.read(wrapped) { uri ->
                    lookedUp += uri
                    if (missing) throw GeneralSecurityException("Key unavailable")
                    newHandle().getPrimitive(Aead::class.java)
                }
                fail("An unreadable keyset must not become an empty pairing")
            } catch (failure: GeneralSecurityException) {
                assertEquals(2, failure.suppressed.size)
                assertEquals(uris, lookedUp)
                assertArrayEquals(original, wrapped)
            }
        }
    }

    @Test fun malformedWrappedKeysetIsRetained() {
        val wrapped = byteArrayOf(1, 2, 3)
        try {
            CredentialKeyset.read(wrapped) { newHandle().getPrimitive(Aead::class.java) }
            fail("Malformed keysets must fail closed")
        } catch (failure: GeneralSecurityException) {
            assertEquals(2, failure.suppressed.size)
            assertArrayEquals(byteArrayOf(1, 2, 3), wrapped)
        }
    }

    private fun newHandle() = KeysetHandle.generateNew(AeadKeyTemplates.AES256_GCM)

    private fun wrap(handle: KeysetHandle, master: Aead): ByteArray = ByteArrayOutputStream().use {
        handle.write(BinaryKeysetWriter.withOutputStream(it), master)
        it.toByteArray()
    }
}
