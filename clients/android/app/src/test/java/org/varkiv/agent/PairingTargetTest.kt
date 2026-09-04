package org.varkiv.agent

import org.junit.Assert.assertFalse
import org.junit.Assert.assertEquals
import org.junit.Test

class PairingTargetTest {
    @Test
    fun androidTargetIsAccepted() {
        requireAndroidPairTarget(" Android ")
    }

    @Test
    fun missingOrForeignTargetsAreRejectedWithoutPrivateDetails() {
        for (target in listOf("", "windows", "rocknix")) {
            val error = runCatching { requireAndroidPairTarget(target) }.exceptionOrNull()
                ?: throw AssertionError("target was accepted: $target")
            assertFalse(error.message.orEmpty().contains(target).and(target.isNotEmpty()))
            assertFalse(error.message.orEmpty().contains("content://"))
        }
    }

    @Test
    fun pairingDeviceCannotOverrideAdministratorProfileSelection() {
        val fields = pairingDeviceFields(" My handheld ", "android-36", "arm64-v8a", "fixture-version")
        assertEquals("My handheld", fields["name"])
        assertEquals("android", fields["os_family"])
        assertFalse(fields.containsKey("device_profile_id"))
        assertFalse(fields.keys.any { it.contains("profile") })
    }
}
