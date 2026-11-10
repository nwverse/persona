package persona.core.designsystem

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.PorterDuffXfermode
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.util.zip.CRC32

/** A dot state's frames, each a whole picture, and how long each shows. */
class PersonaFrames(val bitmaps: List<Bitmap>, val delaysMs: IntArray)

/**
 * Decodes an APNG (or a still PNG: one frame) into whole frames (docs/apple/persona.md "Pack format"). Android decodes only a
 * PNG's first frame, so each frame's data is wrapped as a PNG of its own and composed onto the canvas by its
 * dispose and blend ops, as the APNG spec says. [fps] times the frames whose delay is 0. Null when it is not a PNG.
 */
fun decodeApng(bytes: ByteArray, fps: Double?): PersonaFrames? {
    if (bytes.size < 8 || !bytes.copyOfRange(0, 8).contentEquals(SIGNATURE)) return null
    val fallback = (1000 / (fps ?: 8.0).coerceAtLeast(1.0)).toInt()
    var ihdr: ByteArray? = null
    var animated = false
    val shared = mutableListOf<Pair<String, ByteArray>>()
    val frames = mutableListOf<Frame>()
    var frame: Frame? = null
    val buffer = ByteBuffer.wrap(bytes, 8, bytes.size - 8)
    while (buffer.remaining() >= 12) {
        val length = buffer.int
        val type = ByteArray(4).also { buffer.get(it) }.decodeToString()
        if (length < 0 || length > buffer.remaining() - 4) return null
        val data = ByteArray(length).also { buffer.get(it) }
        buffer.int // CRC
        when (type) {
            "IHDR" -> ihdr = data
            "acTL" -> animated = true
            "fcTL" -> frame = Frame(ByteBuffer.wrap(data)).also { frames += it }
            "IDAT" -> if (animated) frame?.data?.add(data) else return BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
                ?.let { PersonaFrames(listOf(it), intArrayOf(fallback)) }
            "fdAT" -> frame?.data?.add(data.copyOfRange(4, data.size))
            "IEND" -> break
            else -> if (frames.isEmpty() && type[0].isLowerCase() || type == "PLTE") shared += type to data
        }
    }
    val header = ihdr ?: return null
    val width = ByteBuffer.wrap(header).int
    val height = ByteBuffer.wrap(header, 4, 4).int
    if (width <= 0 || height <= 0 || frames.isEmpty()) return null
    val canvasBitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
    val canvas = Canvas(canvasBitmap)
    val clear = Paint().apply { xfermode = PorterDuffXfermode(PorterDuff.Mode.CLEAR) }
    val source = Paint().apply { xfermode = PorterDuffXfermode(PorterDuff.Mode.SRC) }
    val out = mutableListOf<Bitmap>()
    val delays = IntArray(frames.size)
    frames.forEachIndexed { i, f ->
        val picture = f.png(header, shared) ?: return null
        val region = android.graphics.Rect(f.x, f.y, f.x + f.width, f.y + f.height)
        val before = if (f.dispose == DISPOSE_PREVIOUS && i > 0) Bitmap.createBitmap(canvasBitmap, f.x, f.y, f.width, f.height) else null
        canvas.drawBitmap(picture, f.x.toFloat(), f.y.toFloat(), if (f.blend == BLEND_SOURCE) source else null)
        picture.recycle()
        out += canvasBitmap.copy(Bitmap.Config.ARGB_8888, false)
        delays[i] = if (f.delayNum == 0) fallback else f.delayNum * 1000 / (if (f.delayDen == 0) 100 else f.delayDen)
        when {
            before != null -> { canvas.drawBitmap(before, f.x.toFloat(), f.y.toFloat(), source); before.recycle() }
            f.dispose != DISPOSE_NONE -> canvas.drawRect(region, clear)
        }
    }
    canvasBitmap.recycle()
    return PersonaFrames(out, delays)
}

private class Frame(c: ByteBuffer) {
    init { c.int } // sequence number
    val width = c.int
    val height = c.int
    val x = c.int
    val y = c.int
    val delayNum = c.short.toInt() and 0xFFFF
    val delayDen = c.short.toInt() and 0xFFFF
    val dispose = c.get().toInt()
    val blend = c.get().toInt()
    val data = mutableListOf<ByteArray>()

    /** This frame alone as a PNG: the header at the frame's size, the chunks every frame shares, its data. */
    fun png(header: ByteArray, shared: List<Pair<String, ByteArray>>): Bitmap? {
        val out = ByteArrayOutputStream()
        out.write(SIGNATURE)
        out.chunk("IHDR", header.copyOf().also { ByteBuffer.wrap(it).putInt(width).putInt(height) })
        shared.forEach { (type, data) -> out.chunk(type, data) }
        data.forEach { out.chunk("IDAT", it) }
        out.chunk("IEND", ByteArray(0))
        val bytes = out.toByteArray()
        return BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
    }
}

private fun ByteArrayOutputStream.chunk(type: String, data: ByteArray) {
    val typeBytes = type.encodeToByteArray()
    write(ByteBuffer.allocate(4).putInt(data.size).array())
    write(typeBytes)
    write(data)
    val crc = CRC32().apply { update(typeBytes); update(data) }
    write(ByteBuffer.allocate(4).putInt(crc.value.toInt()).array())
}

private val SIGNATURE = byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10)
private const val DISPOSE_NONE = 0
private const val DISPOSE_PREVIOUS = 2
private const val BLEND_SOURCE = 0
