package org.varkiv.agent

import android.os.Build
import org.json.JSONObject

data class PairResult(val deviceId: String, val deviceProfileId: String, val accessToken: String)

internal fun requireAndroidPairTarget(target: String) {
    require(target.trim().lowercase() == "android") { "Selected device profile is not an Android target" }
}

internal fun pairingDeviceFields(name: String, distribution: String, architecture: String, agentVersion: String): Map<String, Any> {
    val normalizedName = name.trim()
    require(normalizedName.isNotBlank()) { "Pairing code and device name are required" }
    return linkedMapOf(
        "name" to normalizedName,
        "os_family" to "android",
        "distribution" to distribution,
        "architecture" to architecture,
        "agent_version" to agentVersion,
    )
}

object PairingClient {
    fun pair(server: String, code: String, name: String, allowHttp: Boolean): PairResult {
        val origin = HttpJson.validateOrigin(server, allowHttp)
        require(code.trim().isNotBlank() && name.trim().isNotBlank()) { "Pairing code and device name are required" }
        val capabilities = JSONObject().put("save_streams", true).put("multi_file_saves", true)
            .put("atomic_no_overwrite", true).put("saf", true).put("android_intent", true)
        val fields = pairingDeviceFields(name, "android-${Build.VERSION.SDK_INT}", Build.SUPPORTED_ABIS.firstOrNull() ?: "unknown", BuildConfig.VERSION_NAME)
        val device = JSONObject()
        fields.forEach { (key, value) -> device.put(key, value) }
        device.put("capabilities", capabilities)
        val response = HttpJson.request("POST", "$origin/api/v1/pairing-codes/redeem", body = JSONObject().put("code", code.trim()).put("device", device))
        requireAndroidPairTarget(response.optString("device_target"))
        val pairedDevice = response.getJSONObject("device")
        val boundProfile = pairedDevice.optString("device_profile_id").trim()
        require(boundProfile.isNotBlank()) { "Pairing response did not include the selected device profile" }
        return PairResult(pairedDevice.getString("id"), boundProfile, response.getString("access_token"))
    }
}
