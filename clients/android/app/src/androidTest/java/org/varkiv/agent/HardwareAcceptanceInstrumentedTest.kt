package org.varkiv.agent

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class HardwareAcceptanceInstrumentedTest {
    @Test
    fun exportedJsonMatchesPrivacyMinimizedServerContract() {
        val payload = AndroidHardwareAcceptance.build(AndroidAcceptanceInput(
            generatedAt = "2026-08-27T08:00:00Z",
            agentVersion = "0.1.0-preview.2",
            hostArchitecture = "arm64",
            configProtected = true,
            agentRootReal = true,
            romRootsConfigured = 2,
            romRootsReal = true,
            saveRootReal = true,
            drivers = listOf(
                AcceptanceRuntimeItem("builtin-driver-retroarch", "RetroArch", "installed"),
                AcceptanceRuntimeItem("builtin-driver-ppsspp", "PPSSPP", "installed"),
            ),
            cores = listOf(AcceptanceRuntimeItem("builtin-core-ppsspp", "PPSSPP", "installed")),
            observations = AndroidHardwareAcceptance.requiredObservations,
        ))
        val json = AndroidHardwareAcceptance.toJSONObject(payload)
        assertEquals("varkiv-hardware-acceptance-v1", json.getString("format"))
        assertEquals("android", json.getString("target"))
        assertTrue(json.getBoolean("software_preflight_passed"))
        assertEquals(2, json.getJSONObject("runtime").getInt("installed_drivers"))
        assertEquals(1, json.getJSONObject("runtime").getInt("installed_cores"))
        assertEquals(10, json.getJSONArray("observed_on_hardware").length())
        assertFalse(json.getBoolean("contains_private_data"))
        val encoded = json.toString()
        listOf("server_url", "access_token", "device_id", "content://", "/storage/", "rom_name", "save_name").forEach {
            assertFalse("private field leaked: $it", encoded.contains(it))
        }
    }
}
