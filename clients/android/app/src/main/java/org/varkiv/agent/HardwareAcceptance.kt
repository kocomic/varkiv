package org.varkiv.agent

import org.json.JSONArray
import org.json.JSONObject

data class AcceptanceRuntimeItem(
    val id: String,
    val name: String,
    val status: String,
)

data class AndroidAcceptanceInput(
    val generatedAt: String,
    val agentVersion: String,
    val hostArchitecture: String,
    val configProtected: Boolean,
    val agentRootReal: Boolean,
    val romRootsConfigured: Int,
    val romRootsReal: Boolean,
    val saveRootReal: Boolean,
    val drivers: List<AcceptanceRuntimeItem>,
    val cores: List<AcceptanceRuntimeItem>,
    val observations: Collection<String>,
)

data class AndroidAcceptancePayload(
    val generatedAt: String,
    val agentVersion: String,
    val hostArchitecture: String,
    val configProtected: Boolean,
    val agentRootReal: Boolean,
    val romRootsConfigured: Int,
    val romRootsReal: Boolean,
    val driverRootsReal: Boolean,
    val drivers: List<AcceptanceRuntimeItem>,
    val cores: List<AcceptanceRuntimeItem>,
    val observations: List<String>,
    val softwarePreflightPassed: Boolean,
)

object AndroidHardwareAcceptance {
    const val format = "varkiv-hardware-acceptance-v1"
    const val target = "android"
    val requiredObservations = listOf(
        "frontend-launch", "rom-launch", "emulator-exit", "saf-rom-root", "saf-save-tree",
        "keystore-token", "retroarch-intent", "ppsspp-intent", "background-recovery", "upgrade",
    )
    private val acceptedDriverIDs = setOf("builtin-driver-retroarch", "builtin-driver-ppsspp")
    private val safeIdentifier = Regex("^[A-Za-z0-9][A-Za-z0-9._+\\-]{0,79}$")

    fun build(input: AndroidAcceptanceInput): AndroidAcceptancePayload {
        require(input.generatedAt.isNotBlank() && input.agentVersion.matches(safeIdentifier)) { "Acceptance version or time is invalid" }
        require(input.hostArchitecture.matches(safeIdentifier)) { "Acceptance architecture is invalid" }
        require(input.romRootsConfigured in 0..128) { "Acceptance ROM root count is invalid" }
        require(input.drivers.size <= 128 && input.drivers.map { it.id }.distinct().size == input.drivers.size) { "Acceptance driver probe is invalid" }
        input.drivers.forEach {
            require(it.id in acceptedDriverIDs && it.name.length <= 160 && it.status in setOf("installed", "missing")) { "Acceptance driver probe is invalid" }
        }
        require(input.cores.size in 1..128 && input.cores.map { it.id }.distinct().size == input.cores.size) { "Acceptance core probe is invalid" }
        input.cores.forEach {
            require(it.id.matches(safeIdentifier) && it.name.length <= 160 && it.status == "installed") { "Acceptance core probe is invalid" }
        }
        val observations = input.observations.toSortedSet().toList()
        require(observations.all(requiredObservations::contains)) { "Acceptance observation is unsupported" }
        val installed = input.drivers.filter { it.status == "installed" }.map { it.id }.toSet()
        val preflight = input.configProtected && input.agentRootReal && input.romRootsConfigured > 0 && input.romRootsReal && input.saveRootReal &&
            acceptedDriverIDs.all(installed::contains) && requiredObservations.all(observations::contains)
        return AndroidAcceptancePayload(
            input.generatedAt, input.agentVersion, input.hostArchitecture, input.configProtected,
            input.agentRootReal, input.romRootsConfigured, input.romRootsReal, input.saveRootReal,
            input.drivers.sortedBy { it.id }, input.cores.sortedBy { it.id }, observations, preflight,
        )
    }

    fun toJSONObject(payload: AndroidAcceptancePayload): JSONObject {
        val drivers = JSONArray()
        payload.drivers.forEach { item ->
            drivers.put(JSONObject().put("id", item.id).put("name", item.name).put("status", item.status))
        }
        val observations = JSONArray()
        payload.observations.forEach(observations::put)
        val cores = JSONArray()
        payload.cores.forEach { item ->
            cores.put(JSONObject().put("id", item.id).put("name", item.name).put("status", item.status))
        }
        return JSONObject()
            .put("format", format)
            .put("generated_at", payload.generatedAt)
            .put("agent_version", payload.agentVersion)
            .put("host_os", "android")
            .put("host_architecture", payload.hostArchitecture)
            .put("target", target)
            .put("config_protected", payload.configProtected)
            .put("roots", JSONObject()
                .put("agent_root_real", payload.agentRootReal)
                .put("rom_roots_configured", payload.romRootsConfigured)
                .put("rom_roots_real", payload.romRootsReal)
                .put("driver_roots_configured", 0)
                .put("driver_roots_real", payload.driverRootsReal)
                .put("path_overrides", 0))
            .put("runtime", JSONObject()
                .put("target", target)
                .put("emulator_dir_configured", false)
                .put("core_dir_configured", false)
                .put("drivers", drivers)
                .put("retroarch_cores", cores)
                .put("installed_drivers", payload.drivers.count { it.status == "installed" })
                .put("installed_cores", payload.cores.size))
            .put("observed_on_hardware", observations)
            .put("software_preflight_passed", payload.softwarePreflightPassed)
            .put("evidence_level", "candidate")
            .put("requires_maintainer_review", true)
            .put("contains_private_data", false)
    }

    fun fileName(payload: AndroidAcceptancePayload): String {
        val stamp = payload.generatedAt.replace(Regex("[^0-9]"), "").take(14)
        require(stamp.length == 14) { "Acceptance time is invalid" }
        return "varkiv-android-acceptance-$stamp.json"
    }
}
