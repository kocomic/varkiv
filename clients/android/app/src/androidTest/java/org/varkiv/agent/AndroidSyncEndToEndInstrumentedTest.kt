package org.varkiv.agent

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import android.provider.DocumentsContract
import android.util.Base64
import java.util.UUID
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class AndroidSyncEndToEndInstrumentedTest {
    @Test
    fun isolatedServerPairsUploadsDownloadsAndPreservesConflict() {
        val arguments = InstrumentationRegistry.getArguments()
        val server = arguments.getString("e2e_server").orEmpty()
        assumeTrue("Android sync E2E requires the isolated acceptance script", server.isNotBlank())
        val pairingCode = requireArgument(arguments.getString("e2e_pairing_code"), "pairing code")
        val adminToken = requireArgument(arguments.getString("e2e_admin_token"), "temporary admin token")
        val streamId = requireArgument(arguments.getString("e2e_stream_id"), "stream ID")
        val editionId = requireArgument(arguments.getString("e2e_edition_id"), "edition ID")
        val ppssppStreamId = requireArgument(arguments.getString("e2e_ppsspp_stream_id"), "PPSSPP stream ID")
        val ppssppEditionId = requireArgument(arguments.getString("e2e_ppsspp_edition_id"), "PPSSPP edition ID")
        val profileId = requireArgument(arguments.getString("e2e_profile_id"), "profile ID")
        val peerDeviceId = requireArgument(arguments.getString("e2e_peer_device_id"), "peer device ID")
        val romBytes = Base64.decode(requireArgument(arguments.getString("e2e_rom_base64"), "synthetic ROM fixture"), Base64.NO_WRAP)
        val ppssppRomBytes = Base64.decode(requireArgument(arguments.getString("e2e_ppsspp_rom_base64"), "synthetic PSP fixture"), Base64.NO_WRAP)
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val preferences = context.getSharedPreferences("agent_config", 0)
        val store = AgentConfigStore(context)
        val romName = "private-device-rom.gba"
        val fileName = "private-device-rom.srm"
        val romRelative = "roms/$romName"
        val saveRelative = "saves/$fileName"
        val ppssppRomName = "private-device-game.iso"
        val ppssppRomRelative = "psp-roms/$ppssppRomName"
        val ppssppSaveRoot = "ppsspp/PSP/SAVEDATA/ULUS-00000"
        val ppssppParamRelative = "$ppssppSaveRoot/PARAM.SFO"
        val ppssppDataRelative = "$ppssppSaveRoot/DATA.BIN"
        val initial = "android-avd-upload-v1".toByteArray()
        val serverSecond = "android-avd-server-v2".toByteArray()
        val serverThird = "android-avd-server-v3".toByteArray()
        val localDivergent = "android-avd-local-v3".toByteArray()
        val ppssppInitial = linkedMapOf(
            "PARAM.SFO" to "ppsspp-avd-param-v1".toByteArray(),
            "DATA.BIN" to "ppsspp-avd-data-v1".toByteArray(),
        )
        val ppssppServerSecond = linkedMapOf(
            "PARAM.SFO" to "ppsspp-avd-param-v2".toByteArray(),
            "DATA.BIN" to "ppsspp-avd-data-v2".toByteArray(),
        )
        val ppssppServerThird = linkedMapOf(
            "PARAM.SFO" to "ppsspp-avd-param-v3".toByteArray(),
            "DATA.BIN" to "ppsspp-avd-data-v3".toByteArray(),
        )
        val ppssppLocalDivergent = "ppsspp-avd-local-v3".toByteArray()

        preferences.edit().clear().commit()
        TestDocumentsProvider.reset(context, linkedMapOf(
            romRelative to romBytes,
            ppssppRomRelative to ppssppRomBytes,
            saveRelative to initial,
            ppssppParamRelative to requireNotNull(ppssppInitial["PARAM.SFO"]),
            ppssppDataRelative to requireNotNull(ppssppInitial["DATA.BIN"]),
        ))
        try {
            val paired = PairingClient.pair(server, pairingCode, "Disposable Android AVD", true)
            assertEquals(profileId, paired.deviceProfileId)
            store.saveIdentity(server, paired.deviceId, paired.deviceProfileId, paired.accessToken, true)
            store.saveTree("rom", TestDocumentsProvider.treeUri("roms"), "gba")
            store.saveTree("rom", TestDocumentsProvider.treeUri("psp-roms"), "psp")
            store.saveTree("save", TestDocumentsProvider.treeUri("saves"))
            store.saveTree("driver-save", TestDocumentsProvider.treeUri("ppsspp"), "builtin-driver-ppsspp")

            val upload = MobileSyncEngine(context).syncOnce()
            assertEquals(2, upload.uploaded)
            assertEquals(0, upload.downloaded)
            assertEquals(0, upload.conflicts)
            val firstRevision = store.streamState(streamId).getString("revision_id")
            val ppssppFirstRevision = store.streamState(ppssppStreamId).getString("revision_id")
            val inventory = HttpJson.request(
                "GET",
                "$server/api/v1/sync/sessions/${HttpJson.pathSegment(upload.sessionId)}/inventory?limit=20&offset=0",
                adminToken,
            )
            assertEquals(2, inventory.getJSONArray("data").length())
            val matchedEditions = (0 until inventory.getJSONArray("data").length()).map {
                inventory.getJSONArray("data").getJSONObject(it).getString("matched_edition_id")
            }.toSet()
            assertEquals(setOf(editionId, ppssppEditionId), matchedEditions)
            assertFalse(inventory.toString().contains(romName))
            assertFalse(inventory.toString().contains(ppssppRomName))
            assertFalse(inventory.toString().contains(fileName))
            val launches = store.launches()
            assertEquals(2, launches.length())
            val launchesByEdition = (0 until launches.length()).associate { launches.getJSONObject(it).getString("edition_id") to launches.getJSONObject(it) }
            val retroarchLaunch = requireNotNull(launchesByEdition[editionId])
            assertEquals("builtin-core-mgba", retroarchLaunch.getString("core_id"))
            assertEquals("com.retroarch.aarch64", retroarchLaunch.getJSONObject("intent").getString("package"))
            val launchURI = android.net.Uri.parse(retroarchLaunch.getString("rom_uri"))
            assertEquals("content", launchURI.scheme)
            assertEquals(TestDocumentsProvider.AUTHORITY, launchURI.authority)
            assertTrue(DocumentsContract.getDocumentId(launchURI).endsWith("/$romName"))
            val ppssppLaunch = requireNotNull(launchesByEdition[ppssppEditionId])
            assertEquals("org.ppsspp.ppsspp", ppssppLaunch.getJSONObject("intent").getString("package"))
            val ppssppLaunchURI = android.net.Uri.parse(ppssppLaunch.getString("rom_uri"))
            assertEquals(TestDocumentsProvider.AUTHORITY, ppssppLaunchURI.authority)
            assertTrue(DocumentsContract.getDocumentId(ppssppLaunchURI).endsWith("/$ppssppRomName"))

            val second = uploadAdminRevision(server, adminToken, streamId, editionId, peerDeviceId, firstRevision, linkedMapOf("primary.srm" to serverSecond))
            val secondRevision = second.getJSONObject("revision").getString("id")
            val ppssppSecond = uploadAdminRevision(server, adminToken, ppssppStreamId, ppssppEditionId, peerDeviceId, ppssppFirstRevision, ppssppServerSecond)
            val ppssppSecondRevision = ppssppSecond.getJSONObject("revision").getString("id")
            val download = MobileSyncEngine(context).syncOnce()
            assertEquals(0, download.uploaded)
            assertEquals(2, download.downloaded)
            assertEquals(0, download.conflicts)
            assertEquals(secondRevision, store.streamState(streamId).getString("revision_id"))
            assertEquals(ppssppSecondRevision, store.streamState(ppssppStreamId).getString("revision_id"))
            assertTrue(TestDocumentsProvider.read(context, saveRelative).contentEquals(serverSecond))
            assertTrue(TestDocumentsProvider.read(context, ppssppParamRelative).contentEquals(requireNotNull(ppssppServerSecond["PARAM.SFO"])))
            assertTrue(TestDocumentsProvider.read(context, ppssppDataRelative).contentEquals(requireNotNull(ppssppServerSecond["DATA.BIN"])))
            assertEquals(listOf("DATA.BIN", "PARAM.SFO"), TestDocumentsProvider.names(context, ppssppSaveRoot))
            assertEquals(listOf(fileName), TestDocumentsProvider.names(context, "saves"))
            assertEquals(listOf(romName), TestDocumentsProvider.names(context, "roms"))
            assertEquals(listOf(ppssppRomName), TestDocumentsProvider.names(context, "psp-roms"))

            TestDocumentsProvider.write(context, saveRelative, localDivergent)
            TestDocumentsProvider.write(context, ppssppDataRelative, ppssppLocalDivergent)
            uploadAdminRevision(server, adminToken, streamId, editionId, peerDeviceId, secondRevision, linkedMapOf("primary.srm" to serverThird))
            uploadAdminRevision(server, adminToken, ppssppStreamId, ppssppEditionId, peerDeviceId, ppssppSecondRevision, ppssppServerThird)
            val conflict = MobileSyncEngine(context).syncOnce()
            assertEquals(0, conflict.uploaded)
            assertEquals(0, conflict.downloaded)
            assertEquals(2, conflict.conflicts)
            assertTrue(TestDocumentsProvider.read(context, saveRelative).contentEquals(localDivergent))
            assertTrue(TestDocumentsProvider.read(context, ppssppDataRelative).contentEquals(ppssppLocalDivergent))

            val revisions = HttpJson.request("GET", "$server/api/v1/save-streams/${HttpJson.pathSegment(streamId)}/revisions?limit=20&offset=0", adminToken)
            assertEquals(3, revisions.getJSONArray("data").length())
            assertFalse(revisions.toString().contains(fileName))
            assertFalse(revisions.toString().contains(romName))
            assertFalse(revisions.toString().contains("private-device-name-never-stored.srm"))
            assertTrue(revisions.toString().contains("primary.srm"))
            val ppssppRevisions = HttpJson.request("GET", "$server/api/v1/save-streams/${HttpJson.pathSegment(ppssppStreamId)}/revisions?limit=20&offset=0", adminToken)
            assertEquals(3, ppssppRevisions.getJSONArray("data").length())
            assertTrue(ppssppRevisions.toString().contains("PARAM.SFO"))
            assertTrue(ppssppRevisions.toString().contains("DATA.BIN"))
            assertFalse(ppssppRevisions.toString().contains(ppssppRomName))
            assertFalse(ppssppRevisions.toString().contains("private-device-name-never-stored.bin"))
            val storedPreferences = preferences.all.toString()
            assertFalse(storedPreferences.contains(pairingCode))
            assertFalse(storedPreferences.contains(adminToken))
            assertFalse(storedPreferences.contains(paired.accessToken))
        } finally {
            preferences.edit().clear().commit()
            TestDocumentsProvider.clear(context)
        }
    }

    private fun uploadAdminRevision(
        server: String,
        adminToken: String,
        streamId: String,
        editionId: String,
        deviceId: String,
        baseRevisionId: String,
        files: LinkedHashMap<String, ByteArray>,
    ): JSONObject {
        val boundary = "----VarkivAndroidE2E${UUID.randomUUID()}"
        val connection = HttpJson.openConnection(
            "$server/api/v1/save-streams/${HttpJson.pathSegment(streamId)}/revisions",
            adminToken,
            "POST",
        )
        connection.doOutput = true
        connection.setChunkedStreamingMode(64 * 1024)
        connection.setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
        connection.outputStream.use { output ->
            fun line(value: String) {
                output.write(value.toByteArray(Charsets.UTF_8))
                output.write("\r\n".toByteArray(Charsets.UTF_8))
            }
            fun field(name: String, value: String) {
                line("--$boundary")
                line("Content-Disposition: form-data; name=\"$name\"")
                line("")
                line(value)
            }
            field("edition_id", editionId)
            field("device_id", deviceId)
            field("base_revision_id", baseRevisionId)
            val manifest = JSONArray()
            files.keys.forEach { manifest.put(JSONObject().put("logical_path", it)) }
            field("manifest", manifest.toString())
            files.values.forEach { bytes ->
                line("--$boundary")
                line("Content-Disposition: form-data; name=\"files\"; filename=\"private-device-name-never-stored.bin\"")
                line("Content-Type: application/octet-stream")
                line("")
                output.write(bytes)
                line("")
            }
            line("--$boundary--")
        }
        return HttpJson.readJSONObject(connection)
    }

    private fun requireArgument(value: String?, label: String): String = value.orEmpty().also {
        require(it.isNotBlank()) { "Android sync E2E requires $label" }
    }
}
