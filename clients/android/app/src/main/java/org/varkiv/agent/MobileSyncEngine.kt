package org.varkiv.agent

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.BufferedInputStream
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.locks.ReentrantLock

data class MobileSyncResult(val sessionId: String, val uploaded: Int, val downloaded: Int, val conflicts: Int) {
    override fun toString(): String = "↑$uploaded ↓$downloaded ⚠$conflicts"
}

private data class SaveFile(val logicalPath: String, val document: SafDocument, val checksum: String, val size: Long)
private data class SaveSet(val streamId: String, val editionId: String, val treeUri: android.net.Uri, val targetRelative: String, val target: SafDocument?, val files: List<SaveFile>, val contentHash: String)
private data class InventoryPayload(val body: JSONArray, val uris: Map<String, android.net.Uri>, val stems: Map<String, String>)
internal data class SaveContentIdentity(val logicalPath: String, val checksum: String, val size: Long)

internal data class AndroidSavePathValues(
    val editionId: String,
    val saveNamespace: String,
    val serial: String = "",
    val productCode: String = "",
    val titleId: String = "",
    val titleIdHigh: String = "",
    val titleIdLow: String = "",
    val platformId: String,
    val romStem: String = "",
    val driverId: String,
)

internal fun renderAndroidSavePath(template: String, values: AndroidSavePathValues, saveDir: String, usesDriverRoot: Boolean): String {
    val replacements = linkedMapOf(
        "edition.id" to values.editionId,
        "edition.save_namespace" to values.saveNamespace,
        "edition.serial" to values.serial,
        "edition.product_code" to values.productCode,
        "edition.title_id" to values.titleId,
        "edition.title_id_high" to values.titleIdHigh,
        "edition.title_id_low" to values.titleIdLow,
        "platform.id" to values.platformId,
        "rom.stem" to values.romStem,
        "driver.id" to values.driverId,
    )
    replacements.forEach { (name, replacement) ->
        if (template.contains("{{$name}}")) require(replacement.isNotBlank()) { "Save binding requires unavailable $name" }
    }
    if (template.contains("{{edition.title_id_high}}") || template.contains("{{edition.title_id_low}}")) {
        require(values.titleIdHigh.matches(Regex("[0-9A-Fa-f]{8}")) && values.titleIdLow.matches(Regex("[0-9A-Fa-f]{8}"))) {
            "Save binding requires a 16-hex edition.title_id"
        }
    }
    var value = template
    replacements.forEach { (name, replacement) -> value = value.replace("{{$name}}", replacement) }
    value = value.replace("{{device.save_dir}}", "")
    if (usesDriverRoot) value = value.replace("{{driver.user_dir}}", "")
    require(!value.contains("{{") && !value.contains("}}")) { "Save path contains an unresolved variable" }
    value = value.replace('\\', '/').trim('/')
    val normalizedSaveDir = saveDir.trim('/')
    if (normalizedSaveDir.isNotBlank() && value.startsWith("$normalizedSaveDir/")) value = value.removePrefix("$normalizedSaveDir/")
    require(value.isNotBlank() && value.split('/').all { it.isNotBlank() && it != "." && it != ".." }) { "Save binding is not SAF-portable" }
    return value
}

internal fun requireCompatibleSharedSaveSet(
    existingTree: String,
    existingTarget: String,
    existingContentHash: String,
    candidateTree: String,
    candidateTarget: String,
    candidateContentHash: String,
) {
    require(existingTree == candidateTree && existingTarget == candidateTarget && existingContentHash == candidateContentHash) {
        "Shared save stream bindings resolve to different local targets or snapshots"
    }
}

internal fun stableSingleFileLogicalPath(localName: String): String {
    val dot = localName.lastIndexOf('.')
    val extension = if (dot >= 0) localName.substring(dot + 1).lowercase() else ""
    return if (extension.length in 1..16 && extension.all { it in 'a'..'z' || it in '0'..'9' }) "primary.$extension" else "primary"
}

internal fun isSingleFileSaveBinding(discoveryMode: String, driverLayout: String): Boolean {
    val mode = discoveryMode.trim().lowercase()
    return mode == "file" || (mode.isBlank() && driverLayout.trim().lowercase() == "single-file")
}

internal fun saveSetContentHash(files: List<SaveContentIdentity>): String {
    if (files.isEmpty()) return ""
    val digest = MessageDigest.getInstance("SHA-256")
    files.sortedBy { it.logicalPath }.forEach { file ->
        listOf(file.logicalPath, file.checksum, file.size.toString()).forEach { value ->
            digest.update(value.toByteArray())
            digest.update(0.toByte())
        }
    }
    return digest.digest().joinToString("") { "%02x".format(it) }
}

internal fun hasValidUnicodeScalars(value: String): Boolean {
    var index = 0
    while (index < value.length) {
        val current = value[index]
        when {
            Character.isHighSurrogate(current) -> {
                if (index + 1 >= value.length || !Character.isLowSurrogate(value[index + 1])) return false
                index += 2
            }
            Character.isLowSurrogate(current) -> return false
            else -> index++
        }
    }
    return true
}

internal fun portableSaveLogicalPath(value: String): String {
    require(value.isNotEmpty() && value == value.trim() && hasValidUnicodeScalars(value) && value.toByteArray(Charsets.UTF_8).size <= 1024) {
        "Server returned an unsafe logical path"
    }
    val normalized = value.replace('\\', '/')
    require(!normalized.startsWith('/') && !normalized.endsWith('/') && !normalized.contains("//")) {
        "Server returned an unsafe logical path"
    }
    normalized.split('/').forEach { part ->
        val base = part.substringBeforeLast('.', part).uppercase()
        val reserved = base in setOf("CON", "PRN", "AUX", "NUL") ||
            (base.length == 4 && (base.startsWith("COM") || base.startsWith("LPT")) && base[3] in '1'..'9')
        require(
            part.isNotEmpty() && part != "." && part != ".." && part.toByteArray(Charsets.UTF_8).size <= 255 &&
                !part.endsWith('.') && !part.endsWith(' ') && part.none { it in "<>:\"|?*" || it.code < 0x20 || it.code == 0x7f } && !reserved,
        ) { "Server returned an unsafe logical path" }
    }
    return normalized
}

internal const val MOBILE_MAX_SAVE_FILES = 4096
internal const val MOBILE_MAX_SAVE_BYTES = 240L * 1024 * 1024

internal fun remainingMobileSaveDownloadBudget(total: Long, declared: Long, fileCount: Int): Long {
    require(fileCount in 0..MOBILE_MAX_SAVE_FILES && total in 0..MOBILE_MAX_SAVE_BYTES) {
        "Save download exceeds the aggregate size or file-count limit"
    }
    val remaining = MOBILE_MAX_SAVE_BYTES - total
    require(declared < 0 || declared <= remaining) { "Save download exceeds the aggregate size limit" }
    return remaining
}

class MobileSyncEngine(private val context: Context) {
    private val configStore = AgentConfigStore(context)

    fun syncOnce(): MobileSyncResult {
        check(syncLock.tryLock()) { "Synchronization is already running" }
        try {
            requireActive()
            return syncOnceLocked()
        } finally {
            syncLock.unlock()
        }
    }

    private fun syncOnceLocked(): MobileSyncResult {
        val config = configStore.load() ?: error("Device is not paired")
        val origin = HttpJson.validateOrigin(config.serverUrl, config.allowHttp)
        var remote = HttpJson.request("GET", "$origin/api/v1/sync/config", config.accessToken)
        configStore.saveDriverOptions(remote.optJSONArray("bindings") ?: JSONArray())
        configStore.savePlatformOptions(remote.optJSONArray("platforms") ?: JSONArray())
        configStore.saveRuntimeOptions(remote)
        val runtimeOptions = parseAndroidRuntimeRequirements(remote)
        val attestations = buildAndroidRuntimeAttestations(runtimeOptions, config.runtimeFiles) { uri ->
            probeAndroidRuntimeFile(context.contentResolver, uri)
        }
        requireActive()
        HttpJson.request("POST", "$origin/api/v1/devices/${HttpJson.pathSegment(config.deviceId)}/heartbeat", config.accessToken,
            JSONObject().put("capabilities", JSONObject().put("runtime_probe", true)
                .put("runtime_file_grants_configured", config.runtimeFiles.isNotEmpty())
                .put("emulator_dir_configured", false).put("core_dir_configured", false)
                .put("emulator_installed", attestations.any { it.kind == "driver" })
                .put("retroarch_core_installed", attestations.any { it.kind == "core" }))
                .put("runtime_attestations", runtimeAttestationsJSON(attestations)))

        // The first response supplies only this Android device's current
        // requirements. Re-read the configuration after the complete
        // heartbeat snapshot so a newly verified bridge appears, while a
        // missing or changed file disappears before any local save is read.
        remote = HttpJson.request("GET", "$origin/api/v1/sync/config", config.accessToken)
        configStore.saveDriverOptions(remote.optJSONArray("bindings") ?: JSONArray())
        configStore.savePlatformOptions(remote.optJSONArray("platforms") ?: JSONArray())
        configStore.saveRuntimeOptions(remote)

        val inventory = enumerateInventory(config, remote)
        requireActive()
        val sets = enumerateSaveSets(config, remote, inventory.stems)
        val saves = JSONArray()
        sets.values.sortedBy { it.streamId }.forEach { set ->
            val state = configStore.streamState(set.streamId)
            saves.put(JSONObject().put("stream_id", set.streamId).put("base_revision_id", state.optString("revision_id"))
                .put("content_hash", set.contentHash).put("has_local_data", set.files.isNotEmpty()))
        }
        val request = JSONObject().put("device_id", config.deviceId).put("inventory", inventory.body).put("saves", saves)
        val fingerprint = sha256(request.toString())
        val response = HttpJson.request("POST", "$origin/api/v1/sync/sessions", config.accessToken, request, configStore.pendingKey(fingerprint))
        val session = response.getJSONObject("session")
        persistLaunchCatalog(remote, response.optJSONArray("inventory") ?: JSONArray(), inventory.uris)
        val sessionId = HttpJson.pathSegment(session.getString("id"))
        var uploaded = 0
        var downloaded = 0
        var conflicts = 0
        val operations = session.optJSONArray("operations") ?: JSONArray()
        for (index in 0 until operations.length()) {
            requireActive()
            val operation = operations.getJSONObject(index)
            val set = sets[operation.getString("stream_id")] ?: error("Server planned an unconfigured save stream")
            when (operation.getString("action")) {
                "upload" -> {
                    val result = upload(origin, config.accessToken, sessionId, HttpJson.pathSegment(operation.getString("id")), set)
                    val revision = result.getJSONObject("revision")
                    val revisionId = HttpJson.pathSegment(revision.getString("id"))
                    val revisionHash = HttpJson.contentHash(revision.getString("content_hash"))
                    require(revisionHash == set.contentHash) { "Server returned an inconsistent uploaded revision" }
                    configStore.saveStreamState(set.streamId, revisionId, revisionHash)
                    uploaded++
                }
                "download" -> {
                    val revision = download(origin, config.accessToken, sessionId, operation, set)
                    configStore.saveStreamState(set.streamId, revision.getString("id"), revision.getString("content_hash"))
                    downloaded++
                }
                "noop" -> if (operation.optString("target_revision_id").isNotBlank()) {
                    configStore.saveStreamState(
                        set.streamId,
                        HttpJson.pathSegment(operation.getString("target_revision_id")),
                        HttpJson.contentHash(operation.getString("expected_hash")),
                    )
                }
                "conflict" -> conflicts++
                else -> error("Server returned an unknown sync action")
            }
        }
        requireActive()
        configStore.clearPending()
        return MobileSyncResult(sessionId, uploaded, downloaded, conflicts)
    }

    private fun enumerateInventory(config: AgentConfig, remote: JSONObject): InventoryPayload {
        val output = JSONArray()
        val uris = linkedMapOf<String, android.net.Uri>()
        val stems = linkedMapOf<String, String>()
        if (config.romTrees.isEmpty()) return InventoryPayload(output, uris, stems)
        val registered = linkedMapOf<String, Set<String>>()
        val platforms = remote.optJSONArray("platforms") ?: JSONArray()
        for (index in 0 until platforms.length()) {
            val item = platforms.getJSONObject(index)
            val extensions = item.optJSONArray("extensions") ?: JSONArray()
            val values = mutableSetOf<String>()
            for (extensionIndex in 0 until extensions.length()) values += extensions.getString(extensionIndex).lowercase()
            registered[item.optString("id")] = values
        }
        val refreshedCache = linkedMapOf<String, ROMHashRecord>()
        for ((platformId, treeUri) in config.romTrees.toSortedMap()) {
            val declared = registered[platformId] ?: error("The configured ROM platform is not registered")
            val allowDirectories = declared.contains("directory")
            val allowed = declared.filter { it != "directory" }.map { if (it.startsWith('.')) it else ".$it" }.toSet()
            require(allowed.isNotEmpty() || allowDirectories) { "The configured ROM platform has no inventory shape" }
            val tree = SafTree(context.contentResolver, treeUri)
            val items: List<Pair<String, SafDocument>> = if (allowDirectories) {
                tree.children(tree.root).filterNot { it.name.startsWith('.') }.filter { document ->
                    document.isDirectory || allowed.any { document.name.lowercase().endsWith(it) }
                }.sortedBy { it.name }.map { it.name to it }
            } else {
                tree.walkFiles().filter { (path, _) -> allowed.any { path.lowercase().endsWith(it) } }.sortedBy { it.first }
            }
            require(output.length() + items.size <= 10_000) { "ROM inventory exceeds the 10000 item limit" }
            for ((logical, document) in items) {
                val clientId = sha256(platformId + "\u0000" + logical)
                val kind = if (document.isDirectory) "directory" else "file"
                // The cache key is opaque and local-only: no URI, document ID,
                // or file name is written to preferences or sent.
                val cacheKey = sha256(treeUri.toString() + "\u0000" + platformId + "\u0000" + document.id)
                val before = if (document.isDirectory) quickDirectorySignal(tree, document) else tree.quickFileSignal(document)?.let { it to document.size }
                val cached = before?.let { configStore.cachedROMHash(cacheKey, kind, it.second, document.modified, it.first) }
                val fingerprint = if (cached != null) cached.checksum to cached.size else if (document.isDirectory) hashDirectory(tree, document) else hashStream(tree.open(document))
                if (cached == null && before != null) {
                    val after = if (document.isDirectory) quickDirectorySignal(tree, document) else tree.quickFileSignal(document)?.let { it to document.size }
                    require(after == before && fingerprint.second == before.second) { "ROM changed while its identity was being calculated" }
                    refreshedCache[cacheKey] = ROMHashRecord(fingerprint.second, document.modified, fingerprint.first, kind, before.first, System.currentTimeMillis() / 1000)
                } else if (cached != null) {
                    refreshedCache[cacheKey] = cached
                }
                val (checksum, size) = fingerprint
                output.put(JSONObject().put("client_item_id", clientId).put("platform_id", platformId).put("sha256", checksum).put("size", size))
                uris[clientId] = document.uri
                val stem = if (document.isDirectory) document.name else document.name.substringBeforeLast('.', document.name)
                val stemKey = "$platformId\u0000$checksum"
                val previous = stems[stemKey]
                stems[stemKey] = if (previous == null || previous == stem) stem else ""
            }
        }
        // Commit only after a complete successful enumeration; interrupted
        // scans retain the previous known-good cache.
        configStore.replaceROMHashCache(refreshedCache)
        return InventoryPayload(output, uris, stems)
    }

    private fun persistLaunchCatalog(remote: JSONObject, inventory: JSONArray, uris: Map<String, android.net.Uri>) {
        val matched = linkedMapOf<String, android.net.Uri>()
        for (index in 0 until inventory.length()) {
            val item = inventory.getJSONObject(index)
            if (item.optString("match_status") == "matched" && item.optString("matched_edition_id").isNotBlank()) {
                uris[item.getString("client_item_id")]?.let { matched[item.getString("matched_edition_id")] = it }
            }
        }
        val stored = JSONArray()
        val launches = remote.optJSONArray("launches") ?: JSONArray()
        for (index in 0 until launches.length()) {
            val launch = launches.getJSONObject(index)
            val uri = matched[launch.optString("edition_id")] ?: continue
            val intent = launch.optJSONObject("driver")?.optJSONObject("launch")?.optJSONObject("android_intent") ?: continue
            val item = JSONObject().put("edition_id", launch.getString("edition_id")).put("platform_id", launch.optString("platform_id"))
                .put("rom_uri", uri.toString()).put("intent", intent)
            val core = launch.optJSONObject("core_resolution")?.optJSONObject("core")
            item.put("core_library", core?.optJSONArray("library_names")?.optString(0) ?: "")
            item.put("core_id", core?.optString("id") ?: "")
            item.put("core_name", core?.optString("name") ?: "")
            stored.put(item)
        }
        configStore.saveLaunches(stored)
    }

    private fun enumerateSaveSets(config: AgentConfig, remote: JSONObject, localROMStems: Map<String, String>): Map<String, SaveSet> {
        val bindings = remote.optJSONArray("bindings") ?: JSONArray()
        if (bindings.length() == 0) return emptyMap()
        val result = linkedMapOf<String, SaveSet>()
        for (index in 0 until bindings.length()) {
            val descriptor = bindings.getJSONObject(index)
            val binding = descriptor.getJSONObject("binding")
            val paths = binding.getJSONArray("local_paths")
            require(paths.length() == 1) { "Android SAF currently requires one binding root per save stream" }
            val template = paths.getString(0)
            val driverId = descriptor.getJSONObject("driver").getString("id").lowercase()
            val usesDriverRoot = template.contains("{{driver.user_dir}}")
            val treeUri = (if (usesDriverRoot) config.driverSaveTrees[driverId] else config.saveTree)
                ?: error("Save folder permission is required")
            val tree = SafTree(context.contentResolver, treeUri)
            val relative = renderSavePath(template, descriptor, remote.optJSONObject("device_profile"), localROMStems, usesDriverRoot)
            val target = tree.find(relative)
            val files = mutableListOf<SaveFile>()
            if (target != null) {
                val discoveryMode = binding.optJSONObject("discovery")?.optString("mode")?.trim()?.lowercase().orEmpty()
                val save = descriptor.getJSONObject("driver").optJSONObject("save") ?: JSONObject()
                val platformLayout = save.optJSONObject("layout_by_platform")?.optString(descriptor.getString("platform_id"))?.trim()?.lowercase().orEmpty()
                val driverLayout = platformLayout.ifBlank { save.optString("layout").trim().lowercase() }
                val singleFile = isSingleFileSaveBinding(discoveryMode, driverLayout)
                require(!singleFile || !target.isDirectory) { "Single-file save binding resolved to a directory" }
                val listed = if (target.isDirectory) tree.walkFiles(target, MOBILE_MAX_SAVE_FILES) else listOf(target.name to target)
                var total = 0L
                for ((logical, document) in listed.sortedBy { it.first }) {
                    val (checksum, size) = hashStream(tree.open(document)); total += size
                    require(total <= MOBILE_MAX_SAVE_BYTES && files.size < MOBILE_MAX_SAVE_FILES) { "Save set exceeds the size or file-count limit" }
                    // Match the desktop Agent contract. A device-local ROM stem
                    // must not become central revision metadata, and identical
                    // bytes on differently named devices must hash identically.
                    val logicalPath = if (singleFile) stableSingleFileLogicalPath(document.name) else portableSaveLogicalPath(logical)
                    files += SaveFile(logicalPath, document, checksum, size)
                }
            }
            val streamId = descriptor.getJSONObject("stream").getString("id")
            val candidate = SaveSet(streamId, descriptor.getString("edition_id"), treeUri, relative, target, files, contentHash(files))
            val existing = result[streamId]
            if (existing != null) {
                requireCompatibleSharedSaveSet(
                    existing.treeUri.toString(), existing.targetRelative, existing.contentHash,
                    candidate.treeUri.toString(), candidate.targetRelative, candidate.contentHash,
                )
            } else {
                result[streamId] = candidate
            }
        }
        return result
    }

    private fun renderSavePath(template: String, descriptor: JSONObject, profile: JSONObject?, localROMStems: Map<String, String>, usesDriverRoot: Boolean): String {
        val platform = descriptor.getString("platform_id")
        val romHash = descriptor.optString("rom_match_sha256").lowercase()
        val romStem = if (romHash.isBlank()) "" else localROMStems["$platform\u0000$romHash"].orEmpty()
        val saveDir = profile?.optJSONObject("paths")?.optString("save_dir")?.trim('/') ?: ""
        return renderAndroidSavePath(template, AndroidSavePathValues(
            editionId = descriptor.getString("edition_id"), saveNamespace = descriptor.getString("save_namespace"),
            serial = descriptor.optString("serial"), productCode = descriptor.optString("product_code"),
            titleId = descriptor.optString("title_id"), titleIdHigh = descriptor.optString("title_id_high"), titleIdLow = descriptor.optString("title_id_low"),
            platformId = platform, romStem = romStem, driverId = descriptor.getJSONObject("driver").getString("id"),
        ), saveDir, usesDriverRoot)
    }

    private fun contentHash(files: List<SaveFile>): String {
        return saveSetContentHash(files.map { SaveContentIdentity(it.logicalPath, it.checksum, it.size) })
    }

    private fun upload(origin: String, token: String, sessionId: String, operationId: String, set: SaveSet): JSONObject {
        require(set.files.isNotEmpty())
        val boundary = "----Varkiv${UUID.randomUUID()}"
        val connection = HttpJson.openConnection("$origin/api/v1/sync/sessions/$sessionId/operations/$operationId/upload", token, "POST")
        connection.doOutput = true
        connection.setChunkedStreamingMode(64 * 1024)
        connection.setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
        connection.outputStream.use { output ->
            fun line(value: String) { output.write(value.toByteArray()); output.write("\r\n".toByteArray()) }
            val manifestFiles = JSONArray()
            set.files.forEach { manifestFiles.put(JSONObject().put("logical_path", it.logicalPath).put("mtime_ns", it.document.modified * 1_000_000).put("mode", 384)) }
            line("--$boundary"); line("Content-Disposition: form-data; name=\"manifest\""); line("Content-Type: application/json"); line("")
            line(JSONObject().put("edition_id", set.editionId).put("files", manifestFiles).toString())
            set.files.forEach { file ->
                line("--$boundary"); line("Content-Disposition: form-data; name=\"files\"; filename=\"save.bin\"")
                line("Content-Type: application/octet-stream"); line("")
                SafTree(context.contentResolver, set.treeUri).open(file.document).use { input ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        requireActive()
                        val count = input.read(buffer)
                        if (count < 0) break
                        output.write(buffer, 0, count)
                    }
                }
                line("")
            }
            line("--$boundary--")
        }
        return HttpJson.readJSONObject(connection)
    }

    private fun download(origin: String, token: String, sessionId: String, operation: JSONObject, set: SaveSet): JSONObject {
        val operationId = HttpJson.pathSegment(operation.getString("id"))
        val revisionId = HttpJson.pathSegment(operation.getString("target_revision_id"))
        val revision = HttpJson.request("GET", "$origin/api/v1/save-revisions/$revisionId", token)
        require(revision.getString("id") == revisionId) { "Server returned a different save revision" }
        val files = revision.getJSONArray("files")
        remainingMobileSaveDownloadBudget(0, -1, files.length())
        val stage = java.io.File(context.cacheDir, "sync-${UUID.randomUUID()}").apply { require(mkdir()) }
        try {
            val downloaded = mutableListOf<Triple<String, java.io.File, Pair<String, Long>>>()
            var total = 0L
            for (index in 0 until files.length()) {
                val metadata = files.getJSONObject(index)
                val logical = portableLogical(metadata.getString("logical_path"))
                val target = java.io.File(stage, index.toString())
                val fileId = HttpJson.pathSegment(metadata.getString("id"))
                val connection = HttpJson.openConnection(
                    "$origin/api/v1/sync/sessions/$sessionId/operations/$operationId/files/$fileId/content",
                    token,
                    accept = "application/octet-stream",
                )
                val digest = MessageDigest.getInstance("SHA-256"); var size = 0L
                try {
                    require(connection.responseCode == 200) { "Save download failed" }
                    val declared = connection.contentLengthLong
                    remainingMobileSaveDownloadBudget(total, declared, files.length())
                    target.outputStream().use { output -> BufferedInputStream(connection.inputStream).use { input ->
                        val buffer = ByteArray(64 * 1024); while (true) { requireActive(); val count = input.read(buffer); if (count < 0) break; size += count; total += count; require(total <= MOBILE_MAX_SAVE_BYTES); digest.update(buffer, 0, count); output.write(buffer, 0, count) }
                    } }
                } finally {
                    connection.disconnect()
                }
                downloaded += Triple(logical, target, digest.digest().joinToString("") { "%02x".format(it) } to size)
            }
            val verification = downloaded.map { SaveFile(it.first, SafDocument(android.net.Uri.EMPTY, "", "", "", it.third.second, 0), it.third.first, it.third.second) }
            require(contentHash(verification) == revision.getString("content_hash") && contentHash(verification) == operation.getString("expected_hash")) { "Downloaded save set failed verification" }
            requireActive()
            installDownloaded(SafTree(context.contentResolver, set.treeUri), set, downloaded)
            requireActive()
            HttpJson.request("POST", "$origin/api/v1/sync/sessions/$sessionId/operations/$operationId/ack", token, JSONObject().put("actual_hash", revision.getString("content_hash")))
            return revision
        } finally { stage.deleteRecursively() }
    }

    private fun installDownloaded(tree: SafTree, set: SaveSet, files: List<Triple<String, java.io.File, Pair<String, Long>>>) {
        val current = tree.find(set.targetRelative)
        require((set.target == null) == (current == null)) { "Local save changed during download; refusing to overwrite it" }
        if (current != null) {
            val checked = if (current.isDirectory) {
                tree.walkFiles(current, MOBILE_MAX_SAVE_FILES).sortedBy { it.first }.map { (logical, document) ->
                    val (checksum, size) = hashStream(tree.open(document)); SaveFile(portableSaveLogicalPath(logical), document, checksum, size)
                }
            } else {
                // enumerateSaveSets deliberately replaces the private device-local
                // basename with a stable logical role such as primary.srm. Reuse
                // that identity here or an unchanged single-file save can never
                // pass the pre-install no-overwrite check.
                require(set.files.size == 1) { "Single-file save snapshot is inconsistent" }
                val (checksum, size) = hashStream(tree.open(current))
                listOf(SaveFile(set.files.single().logicalPath, current, checksum, size))
            }
            require(contentHash(checked) == set.contentHash) { "Local save changed during download; refusing to overwrite it" }
        }
        val (parent, name) = tree.parentAndName(set.targetRelative)
        val suffix = UUID.randomUUID().toString().take(8)
        val tempPrefix = ".varkiv-tmp-$suffix-"
        var temp: SafDocument? = null
        var backup: SafDocument? = null
        try {
            temp = if (set.target?.isDirectory == true || files.size > 1) {
                val dir = tree.create(parent, android.provider.DocumentsContract.Document.MIME_TYPE_DIR, tempPrefix + name)
                files.forEach { (logical, source, _) ->
                    var destinationParent = dir
                    val parts = portableLogical(logical).split('/')
                    parts.dropLast(1).forEach { part ->
                        destinationParent = tree.children(destinationParent).firstOrNull { it.name == part && it.isDirectory }
                            ?: tree.create(destinationParent, android.provider.DocumentsContract.Document.MIME_TYPE_DIR, part)
                    }
                    tree.create(destinationParent, "application/octet-stream", parts.last()).also { document ->
                        source.inputStream().use { input -> tree.write(document, input) }
                    }
                }
                dir
            } else {
                tree.create(parent, "application/octet-stream", tempPrefix + name).also { document ->
                    files.single().second.inputStream().use { input -> tree.write(document, input) }
                }
            }
            if (current != null) {
                backup = tree.rename(current, ".varkiv-backup-${System.currentTimeMillis()}-$name")
            }
            tree.rename(requireNotNull(temp), name)
            // The prior file remains recoverable until the replacement has been
            // published under its final name. Once that succeeds, remove only the
            // backup created by this transaction; failure to clean it is harmless
            // and must not turn an installed revision into a failed sync.
            if (backup != null) try { tree.deleteOwned(backup, ".varkiv-backup-") } catch (_: Exception) { }
        } catch (error: Exception) {
            if (backup != null) try { tree.rename(backup, name) } catch (_: Exception) { }
            if (temp != null) try { tree.deleteOwned(temp, tempPrefix) } catch (_: Exception) { }
            throw error
        }
    }

    private fun portableLogical(value: String): String {
        return portableSaveLogicalPath(value)
    }

    private fun sha256(value: String): String = MessageDigest.getInstance("SHA-256").digest(value.toByteArray()).joinToString("") { "%02x".format(it) }

    private fun requireActive() {
        if (Thread.currentThread().isInterrupted) throw java.io.InterruptedIOException("Synchronization was cancelled")
    }

    companion object {
        private val syncLock = ReentrantLock()
    }
}
