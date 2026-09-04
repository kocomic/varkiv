package org.varkiv.agent

import android.app.job.JobInfo
import android.app.job.JobScheduler
import android.content.Context
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
@Suppress("DEPRECATION") // NETWORK_TYPE_ANY is asserted for the minSdk-compatible JobInfo contract.
class BackgroundSyncInstrumentedTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    @After fun cancelFixtureJob() {
        SyncJobService.cancel(context)
    }

    @Test fun periodicJobIsExplicitConstrainedAndCancellable() {
        val scheduler = context.getSystemService(JobScheduler::class.java)
        val store = AgentConfigStore(context)
        store.saveIdentity(
            "https://fixture.invalid",
            "00000000-0000-4000-8000-000000000001",
            "builtin-device-android-handheld",
            "fixture-device-token-never-sent",
            false,
        )
        store.saveTree("save", Uri.parse("content://org.varkiv.fixture/tree/saves"))
        store.saveTree("driver-save", Uri.parse("content://org.varkiv.fixture/tree/ppsspp"), "builtin-driver-ppsspp")
        store.saveRuntimeFile("core", "builtin-core-snes9x", Uri.parse("content://org.varkiv.fixture/document/snes9x"))
        store.savePlatformOptions(JSONArray()
            .put(JSONObject().put("id", "gba").put("name", "Nintendo Game Boy Advance").put("name_zh", "Game Boy Advance"))
            .put(JSONObject().put("id", "3ds").put("name", "Nintendo 3DS").put("name_zh", "Nintendo 3DS")))
        store.saveRuntimeOptions(JSONObject()
            .put("drivers", JSONArray())
            .put("retroarch_cores", JSONArray().put(JSONObject().put("id", "builtin-core-snes9x").put("name", "Snes9x")))
            .put("runtime_attestation_requirements", JSONArray().put(JSONObject()
                .put("kind", "core").put("runtime_id", "builtin-core-snes9x").put("contract_version", 3))))
        val configured = requireNotNull(store.load())
        assertEquals(Uri.parse("content://org.varkiv.fixture/tree/ppsspp"), configured.driverSaveTrees["builtin-driver-ppsspp"])
        assertEquals(Uri.parse("content://org.varkiv.fixture/document/snes9x"), configured.runtimeFiles["core|builtin-core-snes9x"])
        assertEquals(listOf("core|builtin-core-snes9x"), store.runtimeFileOptions().map { it.key })
        assertEquals(listOf("3ds", "gba"), store.platformOptions().map { it.id }.sorted())

        SyncJobService.schedule(context)

        val job = scheduler.allPendingJobs.single { it.service.className == SyncJobService::class.java.name }
        assertTrue(job.isPersisted)
        assertTrue(job.isRequireBatteryNotLow)
        assertTrue(job.isRequireStorageNotLow)
        assertEquals(JobInfo.NETWORK_TYPE_ANY, job.networkType)
        assertEquals(15 * 60 * 1000L, job.intervalMillis)
        assertTrue(SyncJobService.isEnabled(context))
        val activeStatus = store.backgroundSyncStatus()
        assertTrue(activeStatus.enabled)
        assertTrue(
            "unexpected active state: ${activeStatus.state}",
            activeStatus.state in setOf("scheduled", "running", "complete", "failed", "deferred"),
        )

        val rawStatus = context.getSharedPreferences("agent_config", 0).getString("background_sync_v1", "") ?: ""
        assertFalse(rawStatus.contains("fixture.invalid"))
        assertFalse(rawStatus.contains("fixture-device-token"))
        assertFalse(rawStatus.contains("content://"))

        SyncJobService.cancel(context)
        assertFalse(SyncJobService.isEnabled(context))
        assertEquals("disabled", store.backgroundSyncStatus().state)
        assertTrue(scheduler.allPendingJobs.none { it.service.className == SyncJobService::class.java.name })
        Thread.sleep(250)
        assertFalse(store.backgroundSyncStatus().enabled)
        assertEquals("disabled", store.backgroundSyncStatus().state)
    }
}
