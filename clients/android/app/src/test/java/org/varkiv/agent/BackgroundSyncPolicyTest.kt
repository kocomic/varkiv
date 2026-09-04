package org.varkiv.agent

import java.net.ConnectException
import java.net.SocketTimeoutException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class BackgroundSyncPolicyTest {
    @Test fun mapsFailuresToFixedPrivacySafeCodes() {
        val sensitive = "https://192.0.2.1/private/roms/game.zip?token=secret"
        val cases = listOf(
            SecurityException(sensitive) to "permission_denied",
            SocketTimeoutException(sensitive) to "network_timeout",
            ConnectException(sensitive) to "network_unavailable",
            IllegalStateException(sensitive) to "configuration_or_protocol_error",
            RuntimeException(sensitive) to "sync_failed",
        )
        cases.forEach { (error, expected) ->
            val actual = backgroundFailureCode(error)
            assertEquals(expected, actual)
            assertFalse(actual.contains("192.0.2.1"))
            assertFalse(actual.contains("token"))
            assertFalse(actual.contains("game.zip"))
        }
    }
}
