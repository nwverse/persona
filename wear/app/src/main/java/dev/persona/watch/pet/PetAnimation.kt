package dev.persona.watch.pet

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.PorterDuffXfermode
import dev.persona.watch.data.HubException
import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.nio.ByteBuffer
import java.util.zip.CRC32

/** Bounded APNG reader for the upstream pet assets; Android's static PNG decoder supplies each frame. */
object PetAnimation {
    private val signature = byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10)
    data class Frame(val width: Int, val height: Int, val x: Int, val y: Int, val dispose: Int, val blend: Int, val png: ByteArray)
    data class Source(val width: Int, val height: Int, val frames: List<Frame>)

    fun parse(bytes: ByteArray, expectedFrames: Int): Source {
        if (bytes.size !in 20..4 * 1024 * 1024 || !bytes.copyOfRange(0, 8).contentEquals(signature) || expectedFrames !in 1..120) invalid()
        var offset = 8
        var header: ByteArray? = null
        var width = 0; var height = 0; var sequence = 0; var declared = 0; var ended = false
        var defaultImageSeen = false
        var control: IntArray? = null
        val payloads = mutableListOf<ByteArray>()
        val shared = mutableListOf<Pair<String, ByteArray>>()
        val frames = mutableListOf<Frame>()
        fun number(data: ByteArray, at: Int) = ByteBuffer.wrap(data, at, 4).int
        fun finish() {
            val c = control ?: return
            if (payloads.isEmpty()) invalid()
            val png = ByteArrayOutputStream()
            png.write(signature)
            val frameHeader = header!!.clone()
            ByteBuffer.wrap(frameHeader).putInt(c[0]).putInt(c[1])
            chunk(png, "IHDR", frameHeader)
            shared.forEach { chunk(png, it.first, it.second) }
            payloads.forEach { chunk(png, "IDAT", it) }
            chunk(png, "IEND", byteArrayOf())
            frames += Frame(c[0], c[1], c[2], c[3], c[4], c[5], png.toByteArray())
            if (frames.size > expectedFrames) invalid()
            payloads.clear()
        }
        while (offset <= bytes.size - 12) {
            val length = number(bytes, offset)
            if (length < 0 || length > bytes.size - offset - 12) invalid()
            val type = String(bytes, offset + 4, 4, Charsets.US_ASCII)
            val data = bytes.copyOfRange(offset + 8, offset + 8 + length)
            val crc = CRC32().apply { update(bytes, offset + 4, length + 4) }.value.toInt()
            if (crc != number(bytes, offset + 8 + length)) invalid()
            when (type) {
                "IHDR" -> {
                    if (header != null || length != 13 || offset != 8) invalid()
                    width = number(data, 0); height = number(data, 4)
                    if (width !in 1..512 || height !in 1..512) invalid()
                    // The upstream art uses noninterlaced 8-bit RGB; also accept RGBA and indexed PNG.
                    if (data[8].toInt() != 8 || data[9].toInt() !in setOf(2, 3, 6) ||
                        data[10].toInt() != 0 || data[11].toInt() != 0 || data[12].toInt() != 0) invalid()
                    header = data
                }
                "acTL" -> {
                    if (header == null || control != null || length != 8 || declared != 0) invalid()
                    declared = number(data, 0)
                    if (declared != expectedFrames) invalid()
                }
                "fcTL" -> {
                    if (header == null || declared == 0 || length != 26 || number(data, 0) != sequence++) invalid()
                    finish()
                    val w = number(data, 4); val h = number(data, 8); val x = number(data, 12); val y = number(data, 16)
                    val dispose = data[24].toInt(); val blend = data[25].toInt()
                    if (w !in 1..width || h !in 1..height || x !in 0..(width - w) || y !in 0..(height - h) || dispose !in 0..2 || blend !in 0..1) invalid()
                    control = intArrayOf(w, h, x, y, dispose, blend)
                }
                "IDAT" -> {
                    if (control == null || frames.isNotEmpty() || length == 0) invalid()
                    defaultImageSeen = true
                    payloads += data
                }
                "fdAT" -> {
                    if (control == null || !defaultImageSeen || frames.isEmpty() || length < 5 || number(data, 0) != sequence++) invalid()
                    payloads += data.copyOfRange(4, data.size)
                }
                "PLTE", "tRNS" -> { if (control != null) invalid(); shared += type to data }
                "IEND" -> {
                    if (length != 0 || !defaultImageSeen || offset + 12 != bytes.size) invalid()
                    finish(); ended = true; break
                }
                else -> if (type.length != 4 || type[0] in 'A'..'Z') invalid()
            }
            offset += length + 12
        }
        if (!ended || frames.size != expectedFrames) invalid()
        return Source(width, height, frames)
    }

    /** Decode once off the main thread. Retain only one small clip, never a full pet package in memory. */
    fun decode(bytes: ByteArray, expectedFrames: Int): List<Bitmap> {
        val source = parse(bytes, expectedFrames)
        val working = Bitmap.createBitmap(source.width, source.height, Bitmap.Config.ARGB_8888)
        try {
            val canvas = Canvas(working)
            val clear = Paint().apply { xfermode = PorterDuffXfermode(PorterDuff.Mode.CLEAR) }
            val paint = Paint(Paint.FILTER_BITMAP_FLAG)
            val side = PetTokens.decodedSidePixels
            val factor = minOf(1f, side.toFloat() / maxOf(source.width, source.height))
            return collectPetFrames(source.frames.size, Bitmap::recycle) { output -> source.frames.forEach { frame ->
                val previous = if (frame.dispose == 2) working.copy(Bitmap.Config.ARGB_8888, false) else null
                var part: Bitmap? = null
                try {
                    part = BitmapFactory.decodeByteArray(frame.png, 0, frame.png.size) ?: invalid()
                    if (part.width != frame.width || part.height != frame.height) invalid()
                    val left = frame.x.toFloat(); val top = frame.y.toFloat()
                    if (frame.blend == 0) canvas.drawRect(left, top, left + frame.width, top + frame.height, clear)
                    canvas.drawBitmap(part, left, top, paint)
                    val snapshot = Bitmap.createScaledBitmap(working, (source.width * factor).toInt().coerceAtLeast(1),
                        (source.height * factor).toInt().coerceAtLeast(1), true)
                    output += if (snapshot === working) working.copy(Bitmap.Config.ARGB_8888, false) else snapshot
                    when (frame.dispose) {
                        1 -> canvas.drawRect(left, top, left + frame.width, top + frame.height, clear)
                        2 -> { canvas.drawColor(Color.TRANSPARENT, PorterDuff.Mode.CLEAR); canvas.drawBitmap(previous!!, 0f, 0f, paint) }
                    }
                } finally { part?.recycle(); previous?.recycle() }
            } }
        } finally { working.recycle() }
    }

    private fun chunk(target: ByteArrayOutputStream, type: String, data: ByteArray) {
        val output = DataOutputStream(target); val name = type.toByteArray(Charsets.US_ASCII)
        output.writeInt(data.size); output.write(name); output.write(data)
        output.writeInt(CRC32().apply { update(name); update(data) }.value.toInt())
    }
    private fun invalid(): Nothing = throw HubException("invalid_pet_assets")
}
