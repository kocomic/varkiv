package org.varkiv.agent

import android.app.Activity
import android.app.AlertDialog
import android.annotation.SuppressLint
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.content.pm.PackageManager
import android.provider.Settings
import android.view.View
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ArrayAdapter
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.TextView
import java.io.ByteArrayInputStream
import java.time.Instant
import java.util.Locale
import java.util.concurrent.Executors

class MainActivity : Activity() {
    private val executor = Executors.newSingleThreadExecutor()
    private lateinit var configStore: AgentConfigStore
    private lateinit var server: EditText
    private lateinit var code: EditText
    private lateinit var deviceName: EditText
    private lateinit var platform: Spinner
    private lateinit var romFolderButton: Button
    private lateinit var allowHttp: CheckBox
    private lateinit var saveState: TextView
    private lateinit var saveDriver: Spinner
    private lateinit var driverSaveState: TextView
    private lateinit var runtimeFile: Spinner
    private lateinit var runtimeFileButton: Button
    private lateinit var runtimeFileState: TextView
    private lateinit var romState: TextView
    private lateinit var status: TextView
    private lateinit var launchChoice: Spinner
    private lateinit var automaticSync: CheckBox
    private lateinit var automaticState: TextView
    private lateinit var acceptanceState: TextView
    private lateinit var acceptanceExportButton: Button
    private val acceptanceChecks = linkedMapOf<String, CheckBox>()
    private var pendingAcceptance: AndroidAcceptancePayload? = null
    private var renderingAutomaticSync = false
    private var saveDriverOptions: List<SaveDriverOption> = emptyList()
    private var runtimeFileOptions: List<RuntimeFileOption> = emptyList()
    private var platformOptions: List<PlatformOption> = emptyList()
    private var platformRefreshRequested = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        configStore = AgentConfigStore(this)
        setContentView(buildContent())
        renderStoredState()
    }

    private fun buildContent(): View {
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(22), dp(24), dp(22), dp(32))
            setBackgroundColor(Color.rgb(16, 17, 20))
        }
        content.addView(text(getString(R.string.title), 25f, Color.WHITE).apply { setTypeface(typeface, Typeface.BOLD) })
        content.addView(text(getString(R.string.subtitle), 14f, Color.rgb(170, 173, 181)).apply { setPadding(0, dp(8), 0, dp(20)) })
        server = field(R.string.server_url, "https://varkiv.example")
        code = field(R.string.pairing_code, "ABCDE-FGHIJ")
        deviceName = field(R.string.device_name, Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME) ?: "Android handheld")
        listOf(server, code, deviceName).forEach(content::addView)
        allowHttp = CheckBox(this).apply {
            text = getString(R.string.allow_http); setTextColor(Color.rgb(200, 202, 208)); buttonTintList = android.content.res.ColorStateList.valueOf(Color.rgb(151, 184, 86))
        }
        content.addView(allowHttp)
        content.addView(button(R.string.pair) { pair() })
        saveState = text("", 12f, Color.rgb(139, 143, 152))
        saveDriver = Spinner(this).apply {
            setBackgroundColor(Color.rgb(31, 33, 39)); layoutParams = LinearLayout.LayoutParams(-1, dp(46)).apply { topMargin = dp(12) }
        }
        driverSaveState = text("", 12f, Color.rgb(139, 143, 152))
        romState = text("", 12f, Color.rgb(139, 143, 152))
        content.addView(button(R.string.save_folder) { chooseTree(REQUEST_SAVE_TREE) }); content.addView(saveState)
        content.addView(text(getString(R.string.emulator_save_driver), 12f, Color.rgb(170, 173, 181)).apply { setPadding(0, dp(14), 0, dp(4)) })
        content.addView(saveDriver)
        content.addView(button(R.string.emulator_save_folder) { chooseTree(REQUEST_DRIVER_SAVE_TREE) }); content.addView(driverSaveState)
        content.addView(text(getString(R.string.runtime_verification_title), 12f, Color.rgb(170, 173, 181)).apply { setPadding(0, dp(14), 0, dp(4)) })
        runtimeFile = Spinner(this).apply {
            setBackgroundColor(Color.rgb(31, 33, 39)); layoutParams = LinearLayout.LayoutParams(-1, dp(46))
        }
        content.addView(runtimeFile)
        runtimeFileButton = button(R.string.runtime_verification_file) { chooseRuntimeFile() }
        runtimeFileState = text("", 12f, Color.rgb(139, 143, 152))
        content.addView(runtimeFileButton); content.addView(runtimeFileState)
        content.addView(text(getString(R.string.rom_platform), 12f, Color.rgb(170, 173, 181)).apply { setPadding(0, dp(14), 0, dp(4)) })
        platform = Spinner(this).apply {
            setBackgroundColor(Color.rgb(31, 33, 39)); layoutParams = LinearLayout.LayoutParams(-1, dp(46))
        }
        content.addView(platform)
        romFolderButton = button(R.string.rom_folder) { chooseTree(REQUEST_ROM_TREE) }
        content.addView(romFolderButton); content.addView(romState)
        content.addView(button(R.string.sync_now) { syncNow() })
        automaticSync = CheckBox(this).apply {
            text = getString(R.string.schedule_sync)
            setTextColor(Color.rgb(220, 222, 228))
            buttonTintList = android.content.res.ColorStateList.valueOf(Color.rgb(151, 184, 86))
            setPadding(0, dp(12), 0, dp(4))
            setOnCheckedChangeListener { _, checked ->
                if (!renderingAutomaticSync) setAutomaticSync(checked)
            }
        }
        automaticState = text("", 12f, Color.rgb(139, 143, 152))
        content.addView(automaticSync)
        content.addView(automaticState)
        launchChoice = Spinner(this).apply {
            setBackgroundColor(Color.rgb(31, 33, 39)); layoutParams = LinearLayout.LayoutParams(-1, dp(46)).apply { topMargin = dp(12) }
        }
        content.addView(launchChoice)
        content.addView(button(R.string.launch_game) { launchSelected() })
        content.addView(text(getString(R.string.acceptance_title), 19f, Color.WHITE).apply {
            setTypeface(typeface, Typeface.BOLD); setPadding(0, dp(28), 0, dp(6))
        })
        content.addView(text(getString(R.string.acceptance_intro), 12f, Color.rgb(139, 143, 152)).apply { setPadding(0, 0, 0, dp(8)) })
        val acceptanceLabels = linkedMapOf(
            "frontend-launch" to R.string.acceptance_frontend_launch,
            "rom-launch" to R.string.acceptance_rom_launch,
            "emulator-exit" to R.string.acceptance_emulator_exit,
            "saf-rom-root" to R.string.acceptance_saf_rom,
            "saf-save-tree" to R.string.acceptance_saf_save,
            "keystore-token" to R.string.acceptance_keystore,
            "retroarch-intent" to R.string.acceptance_retroarch,
            "ppsspp-intent" to R.string.acceptance_ppsspp,
            "background-recovery" to R.string.acceptance_background,
            "upgrade" to R.string.acceptance_upgrade,
        )
        acceptanceLabels.forEach { (id, label) ->
            val check = CheckBox(this).apply {
                text = getString(label); setTextColor(Color.rgb(220, 222, 228))
                buttonTintList = android.content.res.ColorStateList.valueOf(Color.rgb(151, 184, 86))
                setOnCheckedChangeListener { _, _ -> renderAcceptanceState() }
            }
            acceptanceChecks[id] = check
            content.addView(check)
        }
        acceptanceState = text("", 12f, Color.rgb(139, 143, 152))
        acceptanceExportButton = button(R.string.acceptance_export) { chooseAcceptanceExportDirectory() }
        content.addView(acceptanceState)
        content.addView(acceptanceExportButton)
        status = text("", 13f, Color.rgb(175, 208, 111)).apply { setPadding(0, dp(16), 0, dp(14)) }
        content.addView(status)
        content.addView(text(getString(R.string.privacy_note), 12f, Color.rgb(126, 130, 140)).apply { setPadding(0, dp(10), 0, 0) })
        content.addView(secondaryButton(R.string.third_party_licenses) { showThirdPartyLicenses() })
        return ScrollView(this).apply { addView(content) }
    }

    private fun field(labelId: Int, hintValue: String): EditText = EditText(this).apply {
        hint = getString(labelId) + " · " + hintValue
        setHintTextColor(Color.rgb(110, 113, 121)); setTextColor(Color.WHITE); setSingleLine(true)
        setPadding(dp(12), dp(11), dp(12), dp(11)); setBackgroundColor(Color.rgb(31, 33, 39))
        layoutParams = LinearLayout.LayoutParams(-1, -2).apply { bottomMargin = dp(9) }
    }

    private fun button(labelId: Int, action: () -> Unit): Button = Button(this).apply {
        text = getString(labelId); isAllCaps = false; setTextColor(Color.rgb(18, 20, 15)); setBackgroundColor(Color.rgb(176, 216, 99))
        layoutParams = LinearLayout.LayoutParams(-1, dp(48)).apply { topMargin = dp(10) }
        setOnClickListener { action() }
    }

    private fun secondaryButton(labelId: Int, action: () -> Unit): Button = Button(this).apply {
        text = getString(labelId); isAllCaps = false; setTextColor(Color.rgb(220, 222, 228)); setBackgroundColor(Color.rgb(31, 33, 39))
        layoutParams = LinearLayout.LayoutParams(-1, dp(46)).apply { topMargin = dp(16) }
        setOnClickListener { action() }
    }

    private fun showThirdPartyLicenses() {
        try {
            val document = ThirdPartyLicenses.compose(
                readBundledText(ThirdPartyLicenses.NOTICES_ASSET),
                readBundledText(ThirdPartyLicenses.APACHE_ASSET),
                getString(R.string.apache_license_heading),
            )
            val viewer = text(document, 12f, Color.rgb(220, 222, 228)).apply {
                typeface = Typeface.MONOSPACE
                setTextIsSelectable(true)
                setPadding(dp(18), dp(12), dp(18), dp(20))
            }
            AlertDialog.Builder(this)
                .setTitle(R.string.third_party_licenses_title)
                .setView(ScrollView(this).apply { addView(viewer) })
                .setPositiveButton(android.R.string.ok, null)
                .show()
        } catch (_: Exception) {
            AlertDialog.Builder(this)
                .setTitle(R.string.third_party_licenses_title)
                .setMessage(R.string.third_party_licenses_unavailable)
                .setPositiveButton(android.R.string.ok, null)
                .show()
        }
    }

    private fun readBundledText(name: String): String = assets.open(name).bufferedReader(Charsets.UTF_8).use { it.readText() }

    private fun text(value: String, size: Float, color: Int) = TextView(this).apply { text = value; textSize = size; setTextColor(color) }
    private fun dp(value: Int): Int = (value * resources.displayMetrics.density).toInt()

    private fun renderStoredState() {
        val config = try { configStore.load() } catch (_: Exception) { null }
        if (config != null) {
            server.setText(config.serverUrl); allowHttp.isChecked = config.allowHttp
            status.text = getString(R.string.paired)
        }
        saveState.text = if (config?.saveTree != null) getString(R.string.granted) else getString(R.string.not_granted)
        saveDriverOptions = configStore.driverSaveOptions()
        saveDriver.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, saveDriverOptions.map { it.name })
        saveDriver.isEnabled = saveDriverOptions.isNotEmpty()
        driverSaveState.text = if (config != null && config.driverSaveTrees.isNotEmpty()) resources.getQuantityString(R.plurals.driver_save_folders_granted, config.driverSaveTrees.size, config.driverSaveTrees.size) else getString(R.string.not_granted)
        renderRuntimeFileOptions(config)
        romState.text = if (config != null && config.romTrees.isNotEmpty()) resources.getQuantityString(R.plurals.rom_folders_granted, config.romTrees.size, config.romTrees.size) else getString(R.string.not_granted)
        renderPlatformOptions(config)
        if (config != null && !platformRefreshRequested) refreshPlatformOptions(config)
        renderAutomaticSync()
        renderLaunchChoices()
        renderAcceptanceState()
    }

    private fun renderPlatformOptions(config: AgentConfig?) {
        val previous = platformOptions.getOrNull(platform.selectedItemPosition)?.id
        platformOptions = configStore.platformOptions()
        val labels = if (platformOptions.isEmpty()) listOf(getString(R.string.rom_platform_unavailable))
            else platformOptions.map { it.label(Locale.getDefault().language) }
        platform.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, labels)
        val preferred = previous ?: config?.romTrees?.keys?.sorted()?.firstOrNull()
        val selected = platformOptions.indexOfFirst { it.id == preferred }
        if (selected >= 0) platform.setSelection(selected)
        val available = config != null && platformOptions.isNotEmpty()
        platform.isEnabled = available
        romFolderButton.isEnabled = available
        romFolderButton.alpha = if (available) 1f else 0.45f
    }

    private fun refreshPlatformOptions(config: AgentConfig) {
        platformRefreshRequested = true
        executor.execute {
            try {
                val origin = HttpJson.validateOrigin(config.serverUrl, config.allowHttp)
                val remote = HttpJson.request("GET", "$origin/api/v1/sync/config", config.accessToken)
                configStore.savePlatformOptions(remote.optJSONArray("platforms") ?: org.json.JSONArray())
                configStore.saveRuntimeOptions(remote)
                runOnUiThread {
                    val refreshed = try { configStore.load() } catch (_: Exception) { null }
                    renderPlatformOptions(refreshed); renderRuntimeFileOptions(refreshed)
                }
            } catch (_: Exception) {
                runOnUiThread {
                    val refreshed = try { configStore.load() } catch (_: Exception) { null }
                    renderPlatformOptions(refreshed); renderRuntimeFileOptions(refreshed)
                }
            }
        }
    }

    private fun renderRuntimeFileOptions(config: AgentConfig?) {
        val previous = runtimeFileOptions.getOrNull(runtimeFile.selectedItemPosition)?.key
        runtimeFileOptions = configStore.runtimeFileOptions()
        val labels = if (runtimeFileOptions.isEmpty()) listOf(getString(R.string.runtime_verification_unavailable))
            else runtimeFileOptions.map { "${it.name} · v${it.contractVersion}" }
        runtimeFile.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, labels)
        val selected = runtimeFileOptions.indexOfFirst { it.key == previous }
        if (selected >= 0) runtimeFile.setSelection(selected)
        val available = config != null && runtimeFileOptions.isNotEmpty()
        runtimeFile.isEnabled = available
        runtimeFileButton.isEnabled = available
        runtimeFileButton.alpha = if (available) 1f else 0.45f
        val configured = config?.runtimeFiles?.keys?.count { key -> runtimeFileOptions.any { it.key == key } } ?: 0
        runtimeFileState.text = if (runtimeFileOptions.isEmpty()) getString(R.string.runtime_files_not_required)
            else resources.getQuantityString(R.plurals.runtime_files_granted, runtimeFileOptions.size, configured, runtimeFileOptions.size)
    }

    private fun setAutomaticSync(enabled: Boolean) {
        try {
            if (enabled) SyncJobService.schedule(this) else SyncJobService.cancel(this)
            show(getString(if (enabled) R.string.sync_scheduled else R.string.sync_unscheduled), false)
        } catch (error: Exception) {
            show(getString(R.string.sync_failed) + ": " + safeError(error), true)
        } finally {
            renderAutomaticSync()
        }
    }

    private fun renderAutomaticSync() {
        val state = configStore.backgroundSyncStatus()
        renderingAutomaticSync = true
        automaticSync.isChecked = SyncJobService.isEnabled(this)
        renderingAutomaticSync = false
        val label = when (state.state) {
            "scheduled" -> getString(R.string.auto_state_scheduled)
            "running" -> getString(R.string.auto_state_running)
            "complete" -> getString(R.string.auto_state_complete, state.uploaded, state.downloaded, state.conflicts)
            "failed" -> getString(R.string.auto_state_failed, failureLabel(state.failureCode))
            "deferred" -> getString(R.string.auto_state_deferred)
            else -> getString(R.string.auto_state_disabled)
        }
        automaticState.text = label
    }

    private fun pair() {
        if (try { configStore.load() } catch (_: Exception) { null } != null) {
            AlertDialog.Builder(this)
                .setTitle(R.string.repair_title)
                .setMessage(R.string.repair_message)
                .setNegativeButton(android.R.string.cancel, null)
                .setPositiveButton(R.string.repair_confirm) { _, _ -> pairConfirmed() }
                .show()
            return
        }
        pairConfirmed()
    }

    private fun pairConfirmed() {
        status.text = "…"
        executor.execute {
            try {
                val origin = HttpJson.validateOrigin(server.text.toString(), allowHttp.isChecked)
                val result = PairingClient.pair(origin, code.text.toString(), deviceName.text.toString(), allowHttp.isChecked)
                configStore.saveIdentity(origin, result.deviceId, result.deviceProfileId, result.accessToken, allowHttp.isChecked)
                platformRefreshRequested = false
                runOnUiThread { code.text.clear(); show(getString(R.string.paired), false); renderStoredState() }
            } catch (error: Exception) { runOnUiThread { show(getString(R.string.pair_failed) + ": " + safeError(error), true) } }
        }
    }

    private fun chooseTree(request: Int) {
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION or Intent.FLAG_GRANT_PREFIX_URI_PERMISSION)
        startActivityForResult(intent, request)
    }

    private fun chooseRuntimeFile() {
        require(runtimeFileOptions.getOrNull(runtimeFile.selectedItemPosition) != null) { getString(R.string.runtime_verification_unavailable) }
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
        startActivityForResult(intent, REQUEST_RUNTIME_FILE)
    }

    @Deprecated("Activity result is used to avoid an external AndroidX dependency")
    @SuppressLint("WrongConstant") // The incoming grant mask is reduced to the two constants accepted by this API.
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (resultCode != RESULT_OK) {
            if (requestCode == REQUEST_ACCEPTANCE_EXPORT) pendingAcceptance = null
            return
        }
        val uri: Uri = data?.data ?: return
        if (requestCode == REQUEST_ACCEPTANCE_EXPORT) {
            writeAcceptanceReport(uri)
            return
        }
        val requestedFlags = if (requestCode == REQUEST_RUNTIME_FILE) Intent.FLAG_GRANT_READ_URI_PERMISSION
            else Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
        val flags = data.flags and requestedFlags
        try {
            contentResolver.takePersistableUriPermission(uri, flags)
            when (requestCode) {
                REQUEST_SAVE_TREE -> configStore.saveTree("save", uri)
                REQUEST_RUNTIME_FILE -> {
                    val selected = runtimeFileOptions.getOrNull(runtimeFile.selectedItemPosition) ?: error(getString(R.string.runtime_verification_unavailable))
                    // Verify readability and the bounded-file contract before
                    // retaining the grant. The digest itself is recomputed on
                    // every sync and never stored with the URI.
                    probeAndroidRuntimeFile(contentResolver, uri)
                    configStore.saveRuntimeFile(selected.kind, selected.runtimeId, uri)
                }
                REQUEST_DRIVER_SAVE_TREE -> {
                    val driver = saveDriverOptions.getOrNull(saveDriver.selectedItemPosition) ?: error(getString(R.string.emulator_save_driver))
                    configStore.saveTree("driver-save", uri, driver.id)
                }
                else -> {
                    val selected = platformOptions.getOrNull(platform.selectedItemPosition) ?: error(getString(R.string.rom_platform_unavailable))
                    configStore.saveTree("rom", uri, selected.id)
                }
            }
            renderStoredState()
        } catch (error: Exception) { show(safeError(error), true) }
    }

    private fun syncNow() {
        show(getString(R.string.sync_running), false)
        executor.execute {
            try {
                val result = MobileSyncEngine(applicationContext).syncOnce()
                runOnUiThread { show(getString(R.string.sync_complete) + " · " + result, false); renderPlatformOptions(try { configStore.load() } catch (_: Exception) { null }); renderLaunchChoices() }
            } catch (error: Exception) { runOnUiThread { renderPlatformOptions(try { configStore.load() } catch (_: Exception) { null }); show(getString(R.string.sync_failed) + ": " + safeError(error), true) } }
        }
    }

    private fun renderLaunchChoices() {
        val launches = try { configStore.launches() } catch (_: Exception) { org.json.JSONArray() }
        val labels = mutableListOf<String>()
        for (index in 0 until launches.length()) {
            val item = launches.getJSONObject(index)
            labels += item.optString("platform_id") + " · " + item.optString("edition_id").take(8)
        }
        if (labels.isEmpty()) labels += getString(R.string.no_launches)
        launchChoice.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, labels)
        launchChoice.isEnabled = launches.length() > 0
    }

    private fun launchSelected() {
        try {
            val launches = configStore.launches()
            require(launches.length() > 0) { getString(R.string.no_launches) }
            IntentLauncher.launch(this, launches.getJSONObject(launchChoice.selectedItemPosition))
        } catch (error: Exception) { show(safeError(error), true) }
    }

    private fun packageInstalled(packageName: String): Boolean = try {
        if (Build.VERSION.SDK_INT >= 33) packageManager.getPackageInfo(packageName, PackageManager.PackageInfoFlags.of(0))
        else @Suppress("DEPRECATION") packageManager.getPackageInfo(packageName, 0)
        true
    } catch (_: PackageManager.NameNotFoundException) { false }

    private fun hasPersistedGrant(uri: Uri, write: Boolean = false): Boolean = contentResolver.persistedUriPermissions.any {
        it.uri == uri && it.isReadPermission && (!write || it.isWritePermission)
    }

    private fun normalizedArchitecture(): String {
        val abi = Build.SUPPORTED_ABIS.firstOrNull().orEmpty().lowercase()
        return when {
            abi.startsWith("arm64") || abi == "aarch64" -> "arm64"
            abi.startsWith("armeabi") || abi == "arm" -> "arm"
            abi == "x86_64" || abi == "amd64" -> "x86_64"
            else -> "android"
        }
    }

    private fun acceptancePayload(): AndroidAcceptancePayload {
        val config = configStore.load() ?: error(getString(R.string.acceptance_requires_pairing))
        val drivers = listOf(
            AcceptanceRuntimeItem("builtin-driver-retroarch", "RetroArch", if (packageInstalled("com.retroarch.aarch64")) "installed" else "missing"),
            AcceptanceRuntimeItem("builtin-driver-ppsspp", "PPSSPP", if (packageInstalled("org.ppsspp.ppsspp")) "installed" else "missing"),
        )
        val cores = linkedMapOf<String, AcceptanceRuntimeItem>()
        val launches = configStore.launches()
        for (index in 0 until launches.length()) {
            val launch = launches.getJSONObject(index)
            val id = launch.optString("core_id")
            if (id.isNotBlank()) cores[id] = AcceptanceRuntimeItem(id, launch.optString("core_name", id), "installed")
        }
        val payload = AndroidHardwareAcceptance.build(AndroidAcceptanceInput(
            generatedAt = Instant.now().toString(), agentVersion = BuildConfig.VERSION_NAME, hostArchitecture = normalizedArchitecture(),
            configProtected = config.accessToken.isNotBlank(), agentRootReal = filesDir.isDirectory,
            romRootsConfigured = config.romTrees.size, romRootsReal = config.romTrees.isNotEmpty() && config.romTrees.values.all { hasPersistedGrant(it) },
            saveRootReal = (listOfNotNull(config.saveTree) + config.driverSaveTrees.values).let { roots -> roots.isNotEmpty() && roots.all { hasPersistedGrant(it, true) } },
            drivers = drivers, cores = cores.values.toList(), observations = acceptanceChecks.filterValues { it.isChecked }.keys,
        ))
        require(payload.softwarePreflightPassed) { getString(R.string.acceptance_incomplete) }
        return payload
    }

    private fun chooseAcceptanceExportDirectory() {
        try {
            pendingAcceptance = acceptancePayload()
            startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION), REQUEST_ACCEPTANCE_EXPORT)
        } catch (error: Exception) {
            pendingAcceptance = null
            show(safeAcceptanceError(error), true)
        }
    }

    private fun writeAcceptanceReport(directory: Uri) {
        val payload = pendingAcceptance ?: return
        pendingAcceptance = null
        var created: SafDocument? = null
        try {
            val tree = SafTree(contentResolver, directory)
            created = tree.create(tree.root, "application/json", AndroidHardwareAcceptance.fileName(payload))
            val bytes = AndroidHardwareAcceptance.toJSONObject(payload).toString(2).toByteArray(Charsets.UTF_8)
            tree.write(created, ByteArrayInputStream(bytes))
            show(getString(R.string.acceptance_exported), false)
        } catch (error: Exception) {
            if (created != null) try { SafTree(contentResolver, directory).deleteOwned(created, "varkiv-android-acceptance-") } catch (_: Exception) { }
            show(safeAcceptanceError(error), true)
        }
    }

    private fun renderAcceptanceState() {
        if (!::acceptanceState.isInitialized) return
        val completed = acceptanceChecks.values.count { it.isChecked }
        acceptanceState.text = resources.getQuantityString(R.plurals.acceptance_progress, AndroidHardwareAcceptance.requiredObservations.size, completed, AndroidHardwareAcceptance.requiredObservations.size)
        acceptanceExportButton.isEnabled = completed == AndroidHardwareAcceptance.requiredObservations.size
        acceptanceExportButton.alpha = if (acceptanceExportButton.isEnabled) 1f else 0.45f
    }

    private fun safeAcceptanceError(error: Exception): String = when {
        error.message == getString(R.string.acceptance_requires_pairing) -> getString(R.string.acceptance_requires_pairing)
        error.message == getString(R.string.acceptance_incomplete) -> getString(R.string.acceptance_incomplete)
        else -> getString(R.string.acceptance_export_failed)
    }

    private fun show(message: String, error: Boolean) { status.setTextColor(if (error) Color.rgb(238, 132, 156) else Color.rgb(175, 208, 111)); status.text = message }
    private fun safeError(error: Exception): String = failureLabel(backgroundFailureCode(error))

    private fun failureLabel(code: String): String = getString(when (code) {
        "permission_denied" -> R.string.auto_error_permission
        "network_timeout" -> R.string.auto_error_timeout
        "network_unavailable" -> R.string.auto_error_network
        "network_or_storage_error" -> R.string.auto_error_io
        "configuration_or_protocol_error" -> R.string.auto_error_configuration
        else -> R.string.auto_error_generic
    })

    override fun onDestroy() { executor.shutdownNow(); super.onDestroy() }

    companion object {
        private const val REQUEST_SAVE_TREE = 100
        private const val REQUEST_ROM_TREE = 101
        private const val REQUEST_ACCEPTANCE_EXPORT = 102
        private const val REQUEST_DRIVER_SAVE_TREE = 103
        private const val REQUEST_RUNTIME_FILE = 104
    }
}
