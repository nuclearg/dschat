package com.dschat.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import mobile.Mobile
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Foreground service that owns the single in-process chatsvr engine.
 *
 * The Go engine lives as long as this service does, not as long as the
 * activity: backgrounding the app or letting the activity be recreated keeps
 * in-flight chat turns and the HTTP server alive. The activity reads the
 * loopback URL from [EngineService.url] once it is published.
 *
 * The engine is started on a worker thread because [Mobile.start] performs
 * disk I/O (SQLite initialization) and must never run on the main thread.
 */
class EngineService : Service() {

    private val worker = Executors.newSingleThreadExecutor()

    @Volatile
    private var destroyed = false

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForegroundCompat()
        if (intent?.action == ACTION_RESTART) {
            synchronized(ERROR_LOCK) { lastError = null }
        }
        ensureEngine()
        return START_STICKY
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        Log.i(TAG, "task removed; stopping engine")
        stopSelf()
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        Log.i(TAG, "service destroyed; stopping engine")
        destroyed = true
        // Stop synchronously so a later create cannot race a delayed Stop().
        // Mobile.stop is idempotent and only closes the DB / cancels contexts.
        try {
            Mobile.stop()
        } catch (t: Throwable) {
            Log.w(TAG, "Mobile.stop failed", t)
        }
        url = null
        worker.shutdown()
        super.onDestroy()
    }

    private fun startForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(
                NOTIFICATION_ID,
                buildNotification(),
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE,
            )
        } else {
            startForeground(NOTIFICATION_ID, buildNotification())
        }
    }

    private fun ensureEngine() {
        if (url != null) return
        if (!starting.compareAndSet(false, true)) return

        worker.execute {
            try {
                val dataDir = filesDir.absolutePath
                Log.i(TAG, "starting engine with data dir $dataDir")
                val engineUrl = Mobile.start(dataDir)
                // The activity may have gone away while SQLite was opening; if
                // the service is already gone, do not leak a running engine.
                if (destroyed) {
                    Log.i(TAG, "service destroyed during startup; stopping engine")
                    Mobile.stop()
                    return@execute
                }
                url = engineUrl
                synchronized(ERROR_LOCK) { lastError = null }
                Log.i(TAG, "engine ready")
            } catch (t: Throwable) {
                Log.e(TAG, "engine failed to start", t)
                synchronized(ERROR_LOCK) { lastError = t.message ?: t.toString() }
            } finally {
                starting.set(false)
            }
        }
    }

    private fun createChannel() {
        val manager = getSystemService(NotificationManager::class.java)
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.channel_name),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = getString(R.string.channel_description)
            setShowBadge(false)
        }
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(): Notification {
        val openApp = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_engine)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(getString(R.string.notification_text))
            .setContentIntent(openApp)
            .setOngoing(true)
            .setShowWhen(false)
            .build()
    }

    companion object {
        private const val TAG = "DSChatEngine"
        private const val CHANNEL_ID = "dschat-engine"
        private const val NOTIFICATION_ID = 1
        private const val ACTION_RESTART = "com.dschat.android.action.RESTART_ENGINE"

        private val ERROR_LOCK = Any()
        private val starting = AtomicBoolean(false)

        /** Loopback URL once the engine is listening; null while starting. */
        @Volatile
        var url: String? = null
            private set

        /** Last engine start failure, or null if it has not failed. */
        @Volatile
        var lastError: String? = null
            private set

        /** Starts (or reuses) the engine service. Safe to call repeatedly. */
        fun start(context: Context) {
            send(context, null)
        }

        /** Clears a previous start error and asks the service to try again. */
        fun retry(context: Context) {
            synchronized(ERROR_LOCK) { lastError = null }
            send(context, ACTION_RESTART)
        }

        /** Stops the engine and the service. Safe to call when not running. */
        fun stop(context: Context) {
            context.stopService(Intent(context, EngineService::class.java))
        }

        private fun send(context: Context, action: String?) {
            val intent = Intent(context, EngineService::class.java)
            if (action != null) intent.action = action
            context.startForegroundService(intent)
        }
    }
}
