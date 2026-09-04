package org.varkiv.agent

import java.io.ByteArrayInputStream
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class RuntimeAttestationTest {
    @Test fun parsesOnlyRequestedRuntimeIdentitiesWithFriendlyNames() {
        val items = normalizeAndroidRuntimeRequirements(listOf(
            AndroidRuntimeRequirement("driver", "builtin-driver-retroarch", 6, ""),
            AndroidRuntimeRequirement("core", "builtin-core-snes9x", 3, ""),
            AndroidRuntimeRequirement("core", "../private", 3, ""),
        ), mapOf("builtin-driver-retroarch" to "RetroArch"), mapOf("builtin-core-snes9x" to "Snes9x"))
        assertEquals(listOf("core|builtin-core-snes9x", "driver|builtin-driver-retroarch"), items.map { it.key })
        assertEquals(listOf("Snes9x · core", "RetroArch"), items.map { it.name })
    }

    @Test fun reportsOnlyGrantedReadableFilesAndNeverSerializesUris() {
        val requirements = listOf(
            RuntimeFileOption("driver", "builtin-driver-retroarch", 6, "RetroArch"),
            RuntimeFileOption("core", "builtin-core-snes9x", 3, "Snes9x"),
        )
        val grants = mapOf(
            runtimeGrantKey("driver", "builtin-driver-retroarch") to "private-runtime-driver",
            runtimeGrantKey("core", "builtin-core-snes9x") to "private-runtime-core",
        )
        val reports = buildAndroidRuntimeAttestations(requirements, grants) { grant ->
            if (grant.endsWith("core")) error("revoked grant")
            "a".repeat(64) to 14705288L
        }
        assertEquals(1, reports.size)
        assertEquals("driver", reports.single().kind)
        assertEquals("builtin-driver-retroarch", reports.single().runtimeId)
        assertFalse(reports.single().toString().contains("private-runtime"))
    }

    @Test fun boundedHashUsesExactBytesAndRejectsSizeOverflowWithoutAllocatingIt() {
        val identity = hashBoundedRuntimeFile(ByteArrayInputStream("runtime-fixture".toByteArray()))
        assertEquals("3366a4dc6028756236dabffb76d79dd654a44cbeb1b1f14a61519ad84c09ff83", identity.first)
        assertEquals(15L, identity.second)
        assertEquals(ANDROID_MAX_RUNTIME_FILE_BYTES, checkedRuntimeFileSize(ANDROID_MAX_RUNTIME_FILE_BYTES - 1, 1))
        assertThrows(IllegalArgumentException::class.java) { checkedRuntimeFileSize(ANDROID_MAX_RUNTIME_FILE_BYTES, 1) }
    }

    @Test fun grantKeysAreClosedAndPathFree() {
        assertTrue(validRuntimeGrantKey("driver|builtin-driver-retroarch"))
        assertTrue(validRuntimeGrantKey("core|builtin-core-snes9x"))
        listOf("driver|../retroarch", "package|retroarch", "driver|", "driver|retroarch|path").forEach {
            assertFalse(it, validRuntimeGrantKey(it))
        }
    }
}
