package org.varkiv.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ThirdPartyLicensesTest {
    @Test
    fun usesTheAssetsEmbeddedByTheBuild() {
        assertEquals("THIRD_PARTY_NOTICES.md", ThirdPartyLicenses.NOTICES_ASSET)
        assertEquals("Apache-2.0.txt", ThirdPartyLicenses.APACHE_ASSET)
    }

    @Test
    fun composesNoticesBeforeTheFullLicenseWithoutChangingTheirText() {
        val result = ThirdPartyLicenses.compose("notice row\nsecond row\n", "license body\n", "Apache License 2.0")

        assertTrue(result.startsWith("notice row\nsecond row"))
        assertTrue(result.indexOf("notice row") < result.indexOf("Apache License 2.0"))
        assertTrue(result.indexOf("Apache License 2.0") < result.indexOf("license body"))
        assertTrue(result.endsWith("license body\n"))
    }
}
