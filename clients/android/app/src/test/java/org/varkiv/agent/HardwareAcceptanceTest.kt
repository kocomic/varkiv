package org.varkiv.agent

import org.junit.Assert.assertFalse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class HardwareAcceptanceTest {
    private fun input(observations: Collection<String> = AndroidHardwareAcceptance.requiredObservations) = AndroidAcceptanceInput(
        generatedAt = "2026-08-27T08:00:00Z",
        agentVersion = "0.1.0-preview.4",
        hostArchitecture = "arm64",
        configProtected = true,
        agentRootReal = true,
        romRootsConfigured = 2,
        romRootsReal = true,
        saveRootReal = true,
        drivers = listOf(
            AcceptanceRuntimeItem("builtin-driver-ppsspp", "PPSSPP", "installed"),
            AcceptanceRuntimeItem("builtin-driver-retroarch", "RetroArch", "installed"),
        ),
        cores = listOf(AcceptanceRuntimeItem("builtin-core-ppsspp", "PPSSPP", "installed")),
        observations = observations,
    )

    @Test
    fun completeAndroidBoundaryProducesCandidatePreflight() {
        val payload = AndroidHardwareAcceptance.build(input(AndroidHardwareAcceptance.requiredObservations.reversed() + "upgrade"))
        assertTrue(payload.softwarePreflightPassed)
        assertEquals(AndroidHardwareAcceptance.requiredObservations.sorted(), payload.observations)
        assertEquals(listOf("builtin-driver-ppsspp", "builtin-driver-retroarch"), payload.drivers.map { it.id })
        assertEquals("varkiv-android-acceptance-20260827080000.json", AndroidHardwareAcceptance.fileName(payload))
    }

    @Test
    fun missingIntentOrDriverKeepsPreflightClosed() {
        val missingIntent = AndroidHardwareAcceptance.build(input(AndroidHardwareAcceptance.requiredObservations - "ppsspp-intent"))
        assertFalse(missingIntent.softwarePreflightPassed)
        val missingDriver = input().copy(drivers = input().drivers.map { if (it.id == "builtin-driver-ppsspp") it.copy(status = "missing") else it })
        assertFalse(AndroidHardwareAcceptance.build(missingDriver).softwarePreflightPassed)
    }

    @Test(expected = IllegalArgumentException::class)
    fun unsupportedObservationIsRejected() {
        AndroidHardwareAcceptance.build(input(AndroidHardwareAcceptance.requiredObservations + "private-path-attached"))
    }

    @Test(expected = IllegalArgumentException::class)
    fun missingMatchedCoreIsRejected() {
        AndroidHardwareAcceptance.build(input().copy(cores = emptyList()))
    }
}
