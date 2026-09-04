package org.varkiv.agent

import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context
import java.util.concurrent.Executors
import java.util.concurrent.Future

class SyncJobService : JobService() {
    private val executor = Executors.newSingleThreadExecutor()
    private val stateLock = Any()
    @Volatile private var stopped = false
    private var future: Future<*>? = null

    override fun onStartJob(params: JobParameters): Boolean {
        val store = AgentConfigStore(applicationContext)
        if (!store.backgroundSyncStatus().enabled) return false
        synchronized(stateLock) {
            stopped = false
            store.saveBackgroundSyncStatus(BackgroundSyncStatus(true, "running", now()))
        }
        future = executor.submit {
            try {
                val result = MobileSyncEngine(applicationContext).syncOnce()
                synchronized(stateLock) {
                    if (!stopped) store.saveBackgroundSyncStatus(BackgroundSyncStatus(true, "complete", now(), result.uploaded, result.downloaded, result.conflicts))
                }
            } catch (error: Exception) {
                synchronized(stateLock) {
                    if (!stopped) store.saveBackgroundSyncStatus(BackgroundSyncStatus(true, "failed", now(), failureCode = backgroundFailureCode(error)))
                }
            }
            synchronized(stateLock) {
                if (!stopped) jobFinished(params, false)
            }
        }
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean {
        val store = AgentConfigStore(applicationContext)
        val enabled: Boolean
        synchronized(stateLock) {
            stopped = true
            future?.cancel(true)
            enabled = store.backgroundSyncStatus().enabled
            if (enabled) store.saveBackgroundSyncStatus(BackgroundSyncStatus(true, "deferred", now()))
        }
        return enabled
    }

    override fun onDestroy() {
        executor.shutdownNow()
        super.onDestroy()
    }

    companion object {
        private const val jobId = 0x4c4d
        fun schedule(context: Context) {
            val store = AgentConfigStore(context.applicationContext)
            val config = store.load() ?: error("Device is not paired")
            require(config.saveTree != null || config.driverSaveTrees.isNotEmpty()) { "Save folder permission is required" }
            val scheduler = context.getSystemService(JobScheduler::class.java)
            val info = JobInfo.Builder(jobId, ComponentName(context, SyncJobService::class.java))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .setRequiresBatteryNotLow(true)
                .setRequiresStorageNotLow(true)
                .setPersisted(true)
                .setPeriodic(15 * 60 * 1000L)
                .build()
            // Persist the enabled state before publishing the job. Android may
            // call onStartJob as soon as schedule() succeeds; writing
            // "scheduled" afterwards could overwrite a real running/terminal
            // state from that callback.
            store.saveBackgroundSyncStatus(BackgroundSyncStatus(true, "scheduled", now()))
            try {
                val result = scheduler.schedule(info)
                require(result == JobScheduler.RESULT_SUCCESS) { "Android rejected the sync schedule" }
            } catch (error: Exception) {
                scheduler.cancel(jobId)
                store.saveBackgroundSyncStatus(BackgroundSyncStatus(false, "disabled", now()))
                throw error
            }
        }

        fun cancel(context: Context) {
            // Disable durably before cancellation can invoke onStopJob. That
            // callback must observe enabled=false and must not replace the
            // explicit user choice with a late "deferred" state.
            AgentConfigStore(context.applicationContext).saveBackgroundSyncStatus(BackgroundSyncStatus(false, "disabled", now()))
            context.getSystemService(JobScheduler::class.java).cancel(jobId)
        }

        fun isEnabled(context: Context): Boolean {
            val stored = AgentConfigStore(context.applicationContext).backgroundSyncStatus().enabled
            val scheduled = context.getSystemService(JobScheduler::class.java).getPendingJob(jobId) != null
            return stored && scheduled
        }

        private fun now(): Long = System.currentTimeMillis() / 1000
    }
}
