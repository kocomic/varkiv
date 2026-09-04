package org.varkiv.agent

import android.content.ContentResolver
import android.net.Uri
import org.json.JSONArray
import org.json.JSONObject
import java.io.InputStream
import java.security.MessageDigest

internal const val ANDROID_MAX_RUNTIME_FILE_BYTES = 512L * 1024 * 1024
private val runtimeIDPattern = Regex("^[a-z0-9][a-z0-9._-]{0,79}$")

internal data class AndroidRuntimeRequirement(
    val kind: String,
    val runtimeId: String,
    val contractVersion: Int,
    val name: String,
)

internal data class AndroidRuntimeAttestation(
    val kind: String,
    val runtimeId: String,
    val contractVersion: Int,
    val sha256: String,
    val size: Long,
) {
    fun toJSONObject(): JSONObject = JSONObject().put("kind", kind).put("runtime_id", runtimeId)
        .put("contract_version", contractVersion).put("sha256", sha256).put("size", size)
}

internal fun runtimeGrantKey(kind: String, runtimeId: String): String = "${kind.trim().lowercase()}|${runtimeId.trim().lowercase()}"

internal fun validRuntimeGrantKey(value: String): Boolean {
    val parts = value.split('|')
    return parts.size == 2 && parts[0] in setOf("driver", "core") && runtimeIDPattern.matches(parts[1])
}

internal fun parseAndroidRuntimeRequirements(remote: JSONObject): List<RuntimeFileOption> {
    val driverNames = runtimeNames(remote.optJSONArray("drivers"), "id", "name")
    val coreNames = runtimeNames(remote.optJSONArray("retroarch_cores"), "id", "name")
    val requirements = remote.optJSONArray("runtime_attestation_requirements") ?: JSONArray()
    val raw = mutableListOf<AndroidRuntimeRequirement>()
    for (index in 0 until requirements.length()) {
        val item = requirements.optJSONObject(index) ?: continue
        raw += AndroidRuntimeRequirement(item.optString("kind"), item.optString("runtime_id"), item.optInt("contract_version"), "")
    }
    return normalizeAndroidRuntimeRequirements(raw, driverNames, coreNames)
}

internal fun normalizeAndroidRuntimeRequirements(
    requirements: List<AndroidRuntimeRequirement>,
    driverNames: Map<String, String>,
    coreNames: Map<String, String>,
): List<RuntimeFileOption> {
    val seen = mutableSetOf<String>()
    val result = mutableListOf<RuntimeFileOption>()
    requirements.forEach { item ->
        val kind = item.kind.trim().lowercase()
        val runtimeId = item.runtimeId.trim().lowercase()
        val key = runtimeGrantKey(kind, runtimeId)
        if (!validRuntimeGrantKey(key) || item.contractVersion < 1 || !seen.add(key)) return@forEach
        val runtimeName = (if (kind == "driver") driverNames else coreNames)[runtimeId]?.trim().orEmpty().ifBlank { runtimeId }
        val label = if (kind == "driver") runtimeName else "$runtimeName · core"
        result += RuntimeFileOption(kind, runtimeId, item.contractVersion, label.take(160))
    }
    return result.sortedWith(compareBy({ it.kind }, { it.name.lowercase() }, { it.runtimeId }))
}

private fun runtimeNames(items: JSONArray?, idField: String, nameField: String): Map<String, String> {
    val result = linkedMapOf<String, String>()
    if (items == null) return result
    for (index in 0 until items.length()) {
        val item = items.optJSONObject(index) ?: continue
        val id = item.optString(idField).trim().lowercase()
        val name = item.optString(nameField).trim()
        if (runtimeIDPattern.matches(id) && name.isNotBlank()) result[id] = name.take(160)
    }
    return result
}

internal fun <T> buildAndroidRuntimeAttestations(
    requirements: List<RuntimeFileOption>,
    grants: Map<String, T>,
    probe: (T) -> Pair<String, Long>,
): List<AndroidRuntimeAttestation> {
    val result = mutableListOf<AndroidRuntimeAttestation>()
    requirements.distinctBy { it.key }.forEach { requirement ->
        val uri = grants[requirement.key] ?: return@forEach
        try {
            val (digest, size) = probe(uri)
            require(digest.matches(Regex("^[0-9a-f]{64}$")) && size > 0) { "Runtime file identity is invalid" }
            result += AndroidRuntimeAttestation(requirement.kind, requirement.runtimeId, requirement.contractVersion, digest, size)
        } catch (_: Exception) {
            // A revoked, unreadable, oversized, or changing grant is omitted
            // from the complete heartbeat snapshot. This immediately removes
            // its server authorization without blocking unrelated save streams.
        }
    }
    return result.sortedWith(compareBy({ it.kind }, { it.runtimeId }))
}

internal fun runtimeAttestationsJSON(items: List<AndroidRuntimeAttestation>): JSONArray = JSONArray().also { output ->
    items.forEach { output.put(it.toJSONObject()) }
}

internal fun probeAndroidRuntimeFile(resolver: ContentResolver, uri: Uri): Pair<String, Long> {
    val before = resolver.openAssetFileDescriptor(uri, "r") ?: error("Runtime file permission is unavailable")
    val beforeLength = before.length
    before.close()
    require(beforeLength <= ANDROID_MAX_RUNTIME_FILE_BYTES || beforeLength < 0) { "Runtime file exceeds the 512 MiB limit" }
    val identity = resolver.openInputStream(uri)?.let(::hashBoundedRuntimeFile) ?: error("Runtime file is unavailable")
    val after = resolver.openAssetFileDescriptor(uri, "r") ?: error("Runtime file permission is unavailable")
    val afterLength = after.length
    after.close()
    require(beforeLength < 0 || beforeLength == identity.second) { "Runtime file changed while it was being verified" }
    require(afterLength < 0 || afterLength == identity.second) { "Runtime file changed while it was being verified" }
    require(beforeLength < 0 || afterLength < 0 || beforeLength == afterLength) { "Runtime file changed while it was being verified" }
    return identity
}

internal fun hashBoundedRuntimeFile(input: InputStream): Pair<String, Long> {
    val digest = MessageDigest.getInstance("SHA-256")
    var size = 0L
    val buffer = ByteArray(64 * 1024)
    input.use {
        while (true) {
            if (Thread.currentThread().isInterrupted) throw java.io.InterruptedIOException("Runtime verification was cancelled")
            val count = it.read(buffer)
            if (count < 0) break
            size = checkedRuntimeFileSize(size, count)
            digest.update(buffer, 0, count)
        }
    }
    require(size > 0) { "Runtime file is empty" }
    return digest.digest().joinToString("") { "%02x".format(it) } to size
}

internal fun checkedRuntimeFileSize(current: Long, nextBytes: Int): Long {
    require(current in 0..ANDROID_MAX_RUNTIME_FILE_BYTES && nextBytes >= 0) { "Runtime file size is invalid" }
    val updated = current + nextBytes
    require(updated <= ANDROID_MAX_RUNTIME_FILE_BYTES) { "Runtime file exceeds the 512 MiB limit" }
    return updated
}
