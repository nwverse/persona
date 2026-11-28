package dev.persona.watch

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import dev.persona.watch.data.HubConfig
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** A watch-scoped credential, encrypted with a non-exportable Android Keystore key. */
class CredentialStore private constructor(private val file: File, private val alias: String) {
    constructor(context: Context, namespace: String = "connection") : this(credentialFile(context.noBackupFilesDir, namespace), ALIAS)

    @Synchronized fun exists(): Boolean = file.exists()

    @Synchronized fun load(): HubConfig? {
        if (!file.exists()) return null
        return try {
            val envelope = JSONObject(file.readText())
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, decode(envelope.getString("iv"))))
            val body = JSONObject(String(cipher.doFinal(decode(envelope.getString("ciphertext"))), Charsets.UTF_8))
            HubConfig(body.getString("baseUrl"), body.getString("token"), body.optString("fingerprint").ifBlank { null },
                body.optString("deviceId").ifBlank { null })
        } catch (_: Exception) {
            // A lost keystore key requires pairing again; never return partial credentials.
            null
        }
    }

    @Synchronized fun save(config: HubConfig) {
        val body = JSONObject().put("baseUrl", config.baseUrl).put("token", config.token)
        config.fingerprint?.let { body.put("fingerprint", it) }
        config.deviceId?.let { body.put("deviceId", it) }
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val encrypted = cipher.doFinal(body.toString().toByteArray(Charsets.UTF_8))
        val envelope = JSONObject().put("iv", encode(cipher.iv)).put("ciphertext", encode(encrypted))
        val temporary = File(file.parentFile, file.nameWithoutExtension + ".new")
        temporary.outputStream().use { it.write(envelope.toString().toByteArray(Charsets.UTF_8)); it.fd.sync() }
        check(temporary.renameTo(file)) { "Could not save connection" }
    }

    @Synchronized fun clear() {
        if (file.exists()) check(file.delete()) { "Could not remove connection" }
    }

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        }.generateKey()
    }

    private fun encode(value: ByteArray) = Base64.encodeToString(value, Base64.NO_WRAP)
    private fun decode(value: String) = Base64.decode(value, Base64.NO_WRAP)
    companion object {
        private const val ALIAS = "persona.watch.connection.v1"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private fun credentialFile(directory: File, namespace: String): File {
            require(namespace == "connection" || namespace.matches(Regex("group_[a-f0-9-]{36}")))
            return File(directory, "$namespace.enc")
        }
        /** Debug instrumentation may exercise real encryption without touching the application's key. */
        internal fun isolatedForCheck(context: Context, directory: File, alias: String): CredentialStore {
            check(BuildConfig.DEBUG)
            return CredentialStore(credentialCheckFile(context.noBackupFilesDir, directory, alias), alias)
        }
    }
}
