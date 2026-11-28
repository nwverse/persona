package dev.persona.watch.pet

import dev.persona.watch.data.HubException
import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.io.File
import java.nio.ByteBuffer
import java.util.zip.CRC32

class PetAnimationTest {
    @Test fun everyOriginalClipParsesToTheManifestFrameCount() {
        val directory = PetModelsTest.upstreamDirectory()
        val manifest = PetManifest.parse(File(directory, "pet.json").readText())
        manifest.clips.values.forEach { clip ->
            val source = PetAnimation.parse(File(directory.parentFile, clip.asset.file).readBytes(), clip.frames)
            assertEquals(288, source.width)
            assertEquals(288, source.height)
            assertEquals(clip.frames, source.frames.size)
            assertTrue(source.frames.all { it.png.size > 32 })
        }
    }

    @Test fun crcTruncationTrailingDataAndWrongManifestCountAreRejected() {
        val valid = sample()
        assertEquals(1, PetAnimation.parse(valid, 1).frames.size)
        val corrupt = valid.clone().also { it[20] = (it[20].toInt() xor 1).toByte() }
        for (invalid in listOf(corrupt, valid.copyOf(valid.size - 1), valid + byteArrayOf(1)))
            assertThrows(HubException::class.java) { PetAnimation.parse(invalid, 1) }
        assertThrows(HubException::class.java) { PetAnimation.parse(valid, 2) }
    }

    @Test fun frameBoundsSequenceAndDecodedSizeAreBounded() {
        for (invalid in listOf(sample(width = 513), sample(frameWidth = 2), sample(sequence = 1)))
            assertThrows(HubException::class.java) { PetAnimation.parse(invalid, 1) }
        assertThrows(HubException::class.java) { PetAnimation.parse(ByteArray(4 * 1024 * 1024 + 1), 1) }
    }

    private fun sample(width: Int = 1, frameWidth: Int = 1, sequence: Int = 0): ByteArray {
        val target = ByteArrayOutputStream()
        target.write(byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10))
        fun chunk(type: String, bytes: ByteArray) {
            val name = type.toByteArray(Charsets.US_ASCII)
            val data = DataOutputStream(target)
            data.writeInt(bytes.size); data.write(name); data.write(bytes)
            data.writeInt(CRC32().apply { update(name); update(bytes) }.value.toInt())
        }
        chunk("IHDR", ByteBuffer.allocate(13).putInt(width).putInt(1).put(byteArrayOf(8, 6, 0, 0, 0)).array())
        chunk("acTL", ByteBuffer.allocate(8).putInt(1).putInt(0).array())
        chunk("fcTL", ByteBuffer.allocate(26).putInt(sequence).putInt(frameWidth).putInt(1).putInt(0).putInt(0)
            .putShort(1).putShort(10).put(0).put(0).array())
        // Parsing validates the APNG envelope; the platform decoder validates compressed pixels on device.
        chunk("IDAT", byteArrayOf(120, -100, 1))
        chunk("IEND", byteArrayOf())
        return target.toByteArray()
    }
}
