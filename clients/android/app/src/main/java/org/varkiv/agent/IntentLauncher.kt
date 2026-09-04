package org.varkiv.agent

import android.content.ClipData
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import org.json.JSONObject

object IntentLauncher {
    private val component = Regex("^[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z][A-Za-z0-9_]*)+$")

    internal data class PreparedLaunch(
        val intent: Intent,
        val packageName: String,
        val romUri: Uri,
        val uriGrantFlags: Int,
    )

    fun launch(context: Context, item: JSONObject) {
        val prepared = prepare(context, item)
        if (prepared.uriGrantFlags != 0) {
            context.grantUriPermission(prepared.packageName, prepared.romUri, prepared.uriGrantFlags)
        }
        try {
            context.startActivity(prepared.intent)
        } catch (error: Exception) {
            if (prepared.uriGrantFlags != 0) {
                context.revokeUriPermission(prepared.packageName, prepared.romUri, prepared.uriGrantFlags)
            }
            throw error
        }
    }

    internal fun prepare(context: Context, item: JSONObject): PreparedLaunch {
        val spec = item.getJSONObject("intent")
        val primaryPackage = spec.getString("package").trim()
        val packageNames = mutableListOf(primaryPackage)
        spec.optJSONArray("package_candidates")?.let { candidates ->
            for (index in 0 until candidates.length()) {
                val candidate = candidates.getString(index).trim()
                if (candidate.isNotBlank() && candidate !in packageNames) packageNames += candidate
            }
        }
        require(packageNames.size <= 9 && packageNames.all(component::matches)) { "Invalid Android package" }
        val packageName = packageNames.firstOrNull { candidate ->
            try {
                context.packageManager.getApplicationInfo(candidate, 0)
                true
            } catch (_: PackageManager.NameNotFoundException) {
                false
            }
        } ?: primaryPackage
        var activity = spec.optString("activity").trim()
        if (activity.startsWith('.')) activity = packageName + activity
        require(activity.isNotBlank() && component.matches(activity)) { "An explicit Android activity is required" }
        val romUri = Uri.parse(item.getString("rom_uri"))
        require(romUri.scheme == "content") { "Only a persisted SAF content URI can be launched" }
        val action = spec.optString("action", Intent.ACTION_VIEW)
        require(component.matches(action)) { "Invalid Intent action" }
        val intent = Intent(action).setComponent(ComponentName(packageName, activity))
        intent.clipData = ClipData.newRawUri("ROM", romUri)
        val values = mutableMapOf("rom.uri" to romUri.toString(), "edition.id" to item.getString("edition_id"),
            "platform.id" to item.optString("platform_id"), "core.library" to item.optString("core_library"))
        fun render(template: String): String {
            if (template.contains("{{android.package_data}}") && !values.containsKey("android.package_data")) {
                val packageData = context.packageManager.getApplicationInfo(packageName, 0).dataDir
                require(packageData.isNotBlank()) { "The target emulator data directory is unavailable" }
                values["android.package_data"] = packageData
            }
            var output = template
            values.forEach { (key, value) -> output = output.replace("{{$key}}", value) }
            require(!output.contains("{{") && !output.contains("}}") && !output.contains('\u0000') && !output.contains('\n') && output.length <= 4096) { "Intent template is unresolved or unsafe" }
            return output
        }
        val renderedData = spec.optString("data").takeIf { it.isNotBlank() }?.let { Uri.parse(render(it)) }
        val renderedMIME = spec.optString("mime_type").takeIf { it.isNotBlank() }?.let(::render)
        when {
            renderedData != null && renderedMIME != null -> intent.setDataAndType(renderedData, renderedMIME)
            renderedData != null -> intent.data = renderedData
            renderedMIME != null -> intent.type = renderedMIME
        }
        val categories = spec.optJSONArray("categories")
        if (categories != null) for (index in 0 until categories.length()) intent.addCategory(render(categories.getString(index)))
        val strings = spec.optJSONObject("string_extras") ?: JSONObject()
        strings.keys().forEach { key -> intent.putExtra(key, render(strings.getString(key))) }
        val booleans = spec.optJSONObject("boolean_extras") ?: JSONObject()
        booleans.keys().forEach { key -> intent.putExtra(key, booleans.getBoolean(key)) }
        var uriGrantFlags = 0
        val flags = spec.optJSONArray("flags")
        if (flags != null) for (index in 0 until flags.length()) when (flags.getString(index)) {
            "grant-read-uri" -> {
                uriGrantFlags = uriGrantFlags or Intent.FLAG_GRANT_READ_URI_PERMISSION
                intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            "grant-write-uri" -> {
                uriGrantFlags = uriGrantFlags or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                intent.addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            }
            "new-task" -> intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            "clear-top" -> intent.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP)
            else -> error("Unsupported Intent flag")
        }
        return PreparedLaunch(intent, packageName, romUri, uriGrantFlags)
    }
}
