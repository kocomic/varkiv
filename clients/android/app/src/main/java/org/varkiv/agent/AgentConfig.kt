package org.varkiv.agent

import android.annotation.SuppressLint
import android.content.Context
import android.net.Uri
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class AgentConfig(
    val serverUrl: String,
    val deviceId: String,
    val deviceProfileId: String,
    val accessToken: String,
    val allowHttp: Boolean,
    val saveTree: Uri?,
    val driverSaveTrees: Map<String, Uri>,
    val romTrees: Map<String, Uri>,
    val runtimeFiles: Map<String, Uri>,
)

data class SaveDriverOption(val id: String, val name: String)
data class RuntimeFileOption(val kind: String, val runtimeId: String, val contractVersion: Int, val name: String) {
    val key: String get() = runtimeGrantKey(kind, runtimeId)
}

data class PlatformOption(val id: String, val name: String, val nameZH: String = "") {
    fun label(language: String): String = when {
        language.lowercase().startsWith("zh") && nameZH.isNotBlank() -> nameZH
        else -> name
    }
}

data class ROMHashRecord(
    val size: Long,
    val modified: Long,
    val checksum: String,
    val kind: String = "file",
    val signal: String = "",
    val verifiedAt: Long = 0,
) {
    fun matches(candidateKind: String, candidateSize: Long, candidateModified: Long, candidateSignal: String, now: Long = System.currentTimeMillis() / 1000): Boolean {
        val age = now - verifiedAt
        return kind == candidateKind && size == candidateSize && modified == candidateModified && signal.isNotBlank() && signal == candidateSignal &&
            verifiedAt > 0 && age in 0 until 24 * 60 * 60 && checksum.matches(Regex("^[0-9a-f]{64}$"))
    }
}

data class BackgroundSyncStatus(
    val enabled: Boolean = false,
    val state: String = "disabled",
    val updatedAt: Long = 0,
    val uploaded: Int = 0,
    val downloaded: Int = 0,
    val conflicts: Int = 0,
    val failureCode: String = "",
)

internal fun backgroundFailureCode(error: Throwable): String = when (error) {
    is SecurityException -> "permission_denied"
    is java.net.SocketTimeoutException -> "network_timeout"
    is java.net.ConnectException, is java.net.UnknownHostException, is java.net.NoRouteToHostException -> "network_unavailable"
    is java.io.IOException -> "network_or_storage_error"
    is IllegalArgumentException, is IllegalStateException -> "configuration_or_protocol_error"
    else -> "sync_failed"
}

class AgentConfigStore(private val context: Context) {
    private val prefs = context.getSharedPreferences("agent_config", Context.MODE_PRIVATE)
    private val alias = "varkiv-agent-token-v1"

    fun load(): AgentConfig? {
        val token = decrypt(prefs.getString("token", null) ?: return null)
        val server = prefs.getString("server", null) ?: return null
        val device = prefs.getString("device", null) ?: return null
        return AgentConfig(
            server, device, prefs.getString("profile", "") ?: "", token,
            prefs.getBoolean("allow_http", false), prefs.getString("save_tree", null)?.let(Uri::parse),
            loadDriverSaveTrees(), loadROMTrees(), loadRuntimeFiles()
        )
    }

    private fun validRuntimeID(value: String): Boolean = value.matches(Regex("^[a-z0-9][a-z0-9._-]{0,79}$"))

    private fun loadDriverSaveTrees(): Map<String, Uri> {
        val result = linkedMapOf<String, Uri>()
        try {
            val stored = JSONObject(prefs.getString("driver_save_trees_v1", "{}") ?: "{}")
            stored.keys().asSequence().sorted().forEach { driver ->
                val normalized = driver.trim().lowercase()
                if (validRuntimeID(normalized)) result[normalized] = Uri.parse(stored.getString(driver))
            }
        } catch (_: Exception) { }
        return result
    }

    private fun loadROMTrees(): Map<String, Uri> {
        val result = linkedMapOf<String, Uri>()
        try {
            val stored = JSONObject(prefs.getString("rom_trees_v2", "{}") ?: "{}")
            stored.keys().asSequence().sorted().forEach { platform ->
                val normalized = platform.trim().lowercase()
                if (normalized.matches(Regex("^[a-z0-9][a-z0-9._-]{0,63}$"))) result[normalized] = Uri.parse(stored.getString(platform))
            }
        } catch (_: Exception) { }
        if (result.isEmpty()) {
            val legacyPlatform = prefs.getString("rom_platform", "")?.trim()?.lowercase().orEmpty()
            val legacyTree = prefs.getString("rom_tree", null)
            if (legacyTree != null && legacyPlatform.matches(Regex("^[a-z0-9][a-z0-9._-]{0,63}$"))) result[legacyPlatform] = Uri.parse(legacyTree)
        }
        return result
    }

    private fun loadRuntimeFiles(): Map<String, Uri> {
        val result = linkedMapOf<String, Uri>()
        try {
            val stored = JSONObject(prefs.getString("runtime_files_v1", "{}") ?: "{}")
            stored.keys().asSequence().sorted().forEach { key ->
                if (validRuntimeGrantKey(key)) result[key] = Uri.parse(stored.getString(key))
            }
        } catch (_: Exception) { }
        return result
    }

    @SuppressLint("ApplySharedPref") // Identity and encrypted token must be durable before pairing is reported as complete.
    fun saveIdentity(server: String, device: String, profile: String, token: String, allowHttp: Boolean) {
        val editor = prefs.edit()
        // A pairing identity owns stream revisions, pending idempotency keys,
        // matched launches, and ROM hashes. Folder grants may be reused only
        // after the user explicitly confirms re-pairing in the UI.
        prefs.all.keys.filter { it.startsWith("stream:") || it.startsWith("romhash:") }.forEach(editor::remove)
        val saved = editor.remove("pending_fingerprint").remove("pending_key").remove("launches")
            .putString("server", server).putString("device", device).putString("profile", profile)
            .putString("token", encrypt(token)).putBoolean("allow_http", allowHttp).commit()
        check(saved) { "Unable to persist the paired device identity" }
    }

    fun saveTree(kind: String, uri: Uri, platform: String = "") {
        require(kind == "save" || kind == "driver-save" || kind == "rom")
        if (kind == "save") {
            check(prefs.edit().putString("save_tree", uri.toString()).commit()) { "Unable to persist the save folder grant" }
            return
        }
        if (kind == "driver-save") {
            val driver = platform.trim().lowercase()
            require(validRuntimeID(driver)) { "A valid emulator driver ID is required" }
            val trees = loadDriverSaveTrees().toMutableMap().apply { put(driver, uri) }
            val payload = JSONObject()
            trees.toSortedMap().forEach { (id, tree) -> payload.put(id, tree.toString()) }
            check(prefs.edit().putString("driver_save_trees_v1", payload.toString()).commit()) { "Unable to persist the emulator save folder grant" }
            return
        }
        val normalized = platform.trim().lowercase()
        require(normalized.matches(Regex("^[a-z0-9][a-z0-9._-]{0,63}$"))) { "A valid ROM platform ID is required" }
        val trees = loadROMTrees().toMutableMap().apply { put(normalized, uri) }
        val payload = JSONObject()
        trees.toSortedMap().forEach { (id, tree) -> payload.put(id, tree.toString()) }
        check(prefs.edit().putString("rom_trees_v2", payload.toString()).remove("rom_tree").remove("rom_platform").commit()) { "Unable to persist the ROM folder grant" }
    }

    fun saveRuntimeFile(kind: String, runtimeId: String, uri: Uri) {
        val key = runtimeGrantKey(kind, runtimeId)
        require(validRuntimeGrantKey(key)) { "A valid runtime identity is required" }
        val files = loadRuntimeFiles().toMutableMap().apply { put(key, uri) }
        val payload = JSONObject()
        files.toSortedMap().forEach { (id, value) -> payload.put(id, value.toString()) }
        check(prefs.edit().putString("runtime_files_v1", payload.toString()).commit()) { "Unable to persist the runtime file grant" }
    }

    fun saveRuntimeOptions(remote: JSONObject) {
        val payload = org.json.JSONArray()
        parseAndroidRuntimeRequirements(remote).forEach { item ->
            payload.put(JSONObject().put("kind", item.kind).put("runtime_id", item.runtimeId)
                .put("contract_version", item.contractVersion).put("name", item.name))
        }
        check(prefs.edit().putString("runtime_file_options_v1", payload.toString()).commit()) { "Unable to persist runtime verification choices" }
    }

    fun runtimeFileOptions(): List<RuntimeFileOption> {
        val result = mutableListOf<RuntimeFileOption>()
        try {
            val stored = org.json.JSONArray(prefs.getString("runtime_file_options_v1", "[]") ?: "[]")
            for (index in 0 until stored.length()) {
                val item = stored.optJSONObject(index) ?: continue
                val kind = item.optString("kind")
                val runtimeId = item.optString("runtime_id")
                val contractVersion = item.optInt("contract_version")
                val name = item.optString("name").trim().take(160)
                if (validRuntimeGrantKey(runtimeGrantKey(kind, runtimeId)) && contractVersion > 0 && name.isNotBlank()) {
                    result += RuntimeFileOption(kind, runtimeId, contractVersion, name)
                }
            }
        } catch (_: Exception) { }
        return result.distinctBy { it.key }.sortedWith(compareBy({ it.kind }, { it.name.lowercase() }, { it.runtimeId }))
    }

    fun saveDriverOptions(bindings: org.json.JSONArray) {
        val options = linkedMapOf<String, String>()
        for (index in 0 until bindings.length()) {
            val descriptor = bindings.optJSONObject(index) ?: continue
            val paths = descriptor.optJSONObject("binding")?.optJSONArray("local_paths") ?: continue
            if ((0 until paths.length()).none { paths.optString(it).contains("{{driver.user_dir}}") }) continue
            val driver = descriptor.optJSONObject("driver") ?: continue
            val id = driver.optString("id").trim().lowercase()
            if (validRuntimeID(id)) options[id] = driver.optString("name", id).take(160)
        }
        val payload = JSONObject()
        options.toSortedMap().forEach { (id, name) -> payload.put(id, name) }
        check(prefs.edit().putString("save_driver_options_v1", payload.toString()).commit()) { "Unable to persist emulator save folder choices" }
    }

    fun driverSaveOptions(): List<SaveDriverOption> {
        val options = linkedMapOf("builtin-driver-ppsspp" to "PPSSPP")
        try {
            val stored = JSONObject(prefs.getString("save_driver_options_v1", "{}") ?: "{}")
            stored.keys().asSequence().sorted().forEach { id ->
                val normalized = id.trim().lowercase()
                if (validRuntimeID(normalized)) options[normalized] = stored.optString(id, normalized).take(160)
            }
        } catch (_: Exception) { }
        return options.map { SaveDriverOption(it.key, it.value) }
    }

    fun savePlatformOptions(platforms: org.json.JSONArray) {
        val payload = org.json.JSONArray()
        val seen = mutableSetOf<String>()
        for (index in 0 until platforms.length()) {
            val item = platforms.optJSONObject(index) ?: continue
            val id = item.optString("id").trim().lowercase()
            if (!validRuntimeID(id) || !seen.add(id)) continue
            val name = item.optString("name").trim().take(160)
            if (name.isBlank()) continue
            val nameZH = item.optString("name_zh").trim().take(160)
            payload.put(JSONObject().put("id", id).put("name", name).put("name_zh", nameZH))
        }
        check(prefs.edit().putString("platform_options_v1", payload.toString()).commit()) { "Unable to persist platform choices" }
    }

    fun platformOptions(): List<PlatformOption> {
        val options = linkedMapOf<String, PlatformOption>()
        try {
            val stored = org.json.JSONArray(prefs.getString("platform_options_v1", "[]") ?: "[]")
            for (index in 0 until stored.length()) {
                val item = stored.optJSONObject(index) ?: continue
                val id = item.optString("id").trim().lowercase()
                val name = item.optString("name").trim().take(160)
                if (!validRuntimeID(id) || name.isBlank()) continue
                options[id] = PlatformOption(id, name, item.optString("name_zh").trim().take(160))
            }
        } catch (_: Exception) { }
        return options.values.sortedBy { it.name.lowercase().ifBlank { it.id } }
    }

    fun streamState(streamId: String): JSONObject = JSONObject(prefs.getString("stream:$streamId", "{}") ?: "{}")
    fun saveStreamState(streamId: String, revisionId: String, contentHash: String) {
        prefs.edit().putString("stream:$streamId", JSONObject().put("revision_id", revisionId).put("content_hash", contentHash).toString()).apply()
    }
    fun pendingKey(fingerprint: String): String {
        val current = prefs.getString("pending_fingerprint", "")
        if (current == fingerprint) return prefs.getString("pending_key", "") ?: ""
        val key = java.util.UUID.randomUUID().toString()
        prefs.edit().putString("pending_fingerprint", fingerprint).putString("pending_key", key).apply()
        return key
    }
    fun clearPending() { prefs.edit().remove("pending_fingerprint").remove("pending_key").apply() }
    fun saveLaunches(launches: org.json.JSONArray) { prefs.edit().putString("launches", launches.toString()).apply() }
    fun launches(): org.json.JSONArray = org.json.JSONArray(prefs.getString("launches", "[]") ?: "[]")

    fun backgroundSyncStatus(): BackgroundSyncStatus {
        return try {
            val item = JSONObject(prefs.getString("background_sync_v1", "{}") ?: "{}")
            val allowedStates = setOf("disabled", "scheduled", "running", "complete", "failed", "deferred")
            val allowedFailures = setOf("", "permission_denied", "network_timeout", "network_unavailable", "network_or_storage_error", "configuration_or_protocol_error", "sync_failed")
            BackgroundSyncStatus(
                enabled = item.optBoolean("enabled", false),
                state = item.optString("state", "disabled").takeIf(allowedStates::contains) ?: "failed",
                updatedAt = item.optLong("updated_at").coerceAtLeast(0),
                uploaded = item.optInt("uploaded").coerceIn(0, 100_000),
                downloaded = item.optInt("downloaded").coerceIn(0, 100_000),
                conflicts = item.optInt("conflicts").coerceIn(0, 100_000),
                failureCode = item.optString("failure_code").takeIf(allowedFailures::contains) ?: "sync_failed",
            )
        } catch (_: Exception) { BackgroundSyncStatus() }
    }

    @SuppressLint("ApplySharedPref") // Scheduling state must be durable before the UI reports that automatic sync changed.
    fun saveBackgroundSyncStatus(status: BackgroundSyncStatus) {
        val item = JSONObject().put("enabled", status.enabled).put("state", status.state)
            .put("updated_at", status.updatedAt.coerceAtLeast(0))
            .put("uploaded", status.uploaded.coerceIn(0, 100_000))
            .put("downloaded", status.downloaded.coerceIn(0, 100_000))
            .put("conflicts", status.conflicts.coerceIn(0, 100_000))
            .put("failure_code", status.failureCode)
        check(prefs.edit().putString("background_sync_v1", item.toString()).commit()) { "Unable to persist automatic sync state" }
    }

    fun cachedROMHash(cacheKey: String, kind: String, size: Long, modified: Long, signal: String): ROMHashRecord? {
        val value = prefs.getString("romhash:$cacheKey", null) ?: return null
        return try {
            val item = JSONObject(value)
            val record = ROMHashRecord(item.getLong("size"), item.getLong("modified"), item.getString("sha256"),
                item.optString("kind"), item.optString("signal"), item.optLong("verified_at"))
            record.takeIf { it.matches(kind, size, modified, signal) }
        } catch (_: Exception) { null }
    }

    fun replaceROMHashCache(records: Map<String, ROMHashRecord>) {
        val editor = prefs.edit()
        prefs.all.keys.filter { it.startsWith("romhash:") }.forEach(editor::remove)
        records.forEach { (key, record) ->
            editor.putString("romhash:$key", JSONObject().put("kind", record.kind).put("size", record.size).put("modified", record.modified)
                .put("signal", record.signal).put("sha256", record.checksum).put("verified_at", record.verifiedAt).toString())
        }
        check(editor.commit()) { "Unable to persist the private ROM hash cache" }
    }

    private fun secretKey(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
            generateKey()
        }
    }

    private fun encrypt(value: String): String {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, secretKey())
        val payload = JSONObject().put("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .put("data", Base64.encodeToString(cipher.doFinal(value.toByteArray()), Base64.NO_WRAP))
        return payload.toString()
    }

    private fun decrypt(value: String): String {
        val payload = JSONObject(value)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, Base64.decode(payload.getString("iv"), Base64.NO_WRAP)))
        return String(cipher.doFinal(Base64.decode(payload.getString("data"), Base64.NO_WRAP)))
    }
}
