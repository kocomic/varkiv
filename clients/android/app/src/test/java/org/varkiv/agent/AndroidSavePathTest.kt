package org.varkiv.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class AndroidSavePathTest {
    @Test fun rendersRetroArchStemInsideTheDefaultSafTree() {
        val values = AndroidSavePathValues(
            editionId = "edition", saveNamespace = "stable-save", platformId = "gba",
            romStem = "renamed-on-device", driverId = "builtin-driver-retroarch",
        )
        assertEquals(
            "renamed-on-device.srm",
            renderAndroidSavePath("{{device.save_dir}}/{{rom.stem}}.srm", values, "saves", false),
        )
    }

    @Test fun rendersPpssppProductCodeInsideItsExplicitDriverTree() {
        val values = AndroidSavePathValues(
            editionId = "edition", saveNamespace = "stable-save", productCode = "ULUS-00000",
            platformId = "psp", driverId = "builtin-driver-ppsspp",
        )
        assertEquals(
            "PSP/SAVEDATA/ULUS-00000",
            renderAndroidSavePath("{{driver.user_dir}}/PSP/SAVEDATA/{{edition.product_code}}", values, "", true),
        )
    }

    @Test fun refusesMissingPrivateIdentityAndTraversal() {
        val values = AndroidSavePathValues(editionId = "edition", saveNamespace = "save", platformId = "psp", driverId = "driver")
        assertThrows(IllegalArgumentException::class.java) {
            renderAndroidSavePath("{{driver.user_dir}}/{{edition.product_code}}", values, "", true)
        }
        assertThrows(IllegalArgumentException::class.java) {
            renderAndroidSavePath("{{device.save_dir}}/../private", values, "saves", false)
        }
        assertThrows(IllegalArgumentException::class.java) {
            renderAndroidSavePath("{{device.save_dir}}/{{rom.stem}}.srm", values, "saves", false)
        }
        assertThrows(IllegalArgumentException::class.java) {
            renderAndroidSavePath("{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}", values.copy(titleIdHigh = "GGGGGGGG", titleIdLow = "12345678"), "", true)
        }
    }
}
