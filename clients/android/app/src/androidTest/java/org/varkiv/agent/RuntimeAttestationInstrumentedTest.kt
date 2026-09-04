package org.varkiv.agent

import android.content.Context
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class RuntimeAttestationInstrumentedTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    @Test fun contentResolverProbeHashesOnlyTheGrantedFile() {
        val fixture = File(context.cacheDir, "runtime-attestation-fixture.bin")
        try {
            fixture.writeBytes("runtime-fixture".toByteArray())
            val identity = probeAndroidRuntimeFile(context.contentResolver, Uri.fromFile(fixture))
            assertEquals("3366a4dc6028756236dabffb76d79dd654a44cbeb1b1f14a61519ad84c09ff83", identity.first)
            assertEquals(15L, identity.second)
        } finally {
            fixture.delete()
        }
    }
}
