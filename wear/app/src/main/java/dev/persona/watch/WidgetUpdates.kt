package dev.persona.watch

import android.content.ComponentName
import android.content.Context
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceUpdateRequester
import dev.persona.watch.extensions.ExtensionComplicationService
import dev.persona.watch.pet.PetComplicationService

object WidgetUpdates {
    fun request(context: Context) {
        listOf(PetComplicationService::class.java, ExtensionComplicationService::class.java).forEach { service ->
            runCatching { ComplicationDataSourceUpdateRequester.create(context, ComponentName(context, service)).requestUpdateAll() }
        }
    }
}
