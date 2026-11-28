package dev.persona.watch.pet

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import dev.persona.watch.R
import java.io.File

object PetPresentation {
    fun title(context: Context, status: PetStatus): String = when (status.state) {
        PetState.NEEDS_YOU -> context.getString(R.string.pet_needs_you, status.count)
        PetState.WORKING -> context.getString(R.string.pet_working, status.count)
        PetState.DONE -> context.getString(R.string.pet_done, status.count)
        PetState.IDLE -> context.getString(R.string.pet_idle)
        PetState.SLEEP -> context.getString(R.string.pet_sleep)
        PetState.OFFLINE -> context.getString(R.string.pet_offline)
    }

    fun shortTitle(context: Context, state: PetState): String = context.getString(when (state) {
        PetState.NEEDS_YOU -> R.string.pet_short_waiting; PetState.WORKING -> R.string.pet_short_working
        PetState.DONE -> R.string.pet_short_done; PetState.IDLE -> R.string.pet_short_idle
        PetState.SLEEP -> R.string.pet_short_sleep; PetState.OFFLINE -> R.string.pet_short_offline
    })

    fun face(file: File): Bitmap? {
        val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(file.path, options)
        if (options.outWidth !in 1..512 || options.outHeight !in 1..512) return null
        return BitmapFactory.decodeFile(file.path)
    }
}
