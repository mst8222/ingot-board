package io.ingotboard.phone

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.IBinder
import androidx.core.app.NotificationCompat
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * 用户打开「保持接收」时挂住桌面的 SSE，任务进入待评审就通知一次。
 * 默认关闭，不在后台一直占着连接。
 */
class ReviewService : Service() {
    private val running = AtomicBoolean(false)
    private val client = OkHttpClient.Builder()
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .build()

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val prefs = getSharedPreferences("phone", MODE_PRIVATE)
        val base = prefs.getString("base_url", "").orEmpty().trimEnd('/')
        val token = prefs.getString("token", "").orEmpty()
        if (!UrlPolicy.accept(base) || !token.startsWith("atd_")) {
            stopSelf()
            return START_NOT_STICKY
        }
        startForeground(1, ongoing("正在接收桌面任务"))
        if (running.compareAndSet(false, true)) {
            Thread {
                while (running.get()) {
                    try {
                        read(base, token)
                    } catch (_: Exception) {
                        if (!running.get()) break
                        Thread.sleep(3000)
                    }
                }
            }.start()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        running.set(false)
        super.onDestroy()
    }

    private fun read(base: String, token: String) {
        val req = Request.Builder()
            .url("$base/api/v1/stream")
            .header("Authorization", "Bearer $token")
            .header("Accept", "text/event-stream")
            .build()
        client.newCall(req).execute().use { resp ->
            val body = resp.body ?: return
            body.source().use { source ->
                while (running.get() && !source.exhausted()) {
                    val line = source.readUtf8Line() ?: break
                    if (line.startsWith("data:") && line.contains("\"to_status\":\"review\"")) {
                        val manager = getSystemService(NotificationManager::class.java)
                        manager.notify(2, alert("有任务待评审"))
                    }
                }
            }
        }
    }

    private fun ongoing(text: String): Notification {
        ensureChannel()
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("Ingot Board")
            .setContentText(text)
            .setOngoing(true)
            .build()
    }

    private fun alert(text: String): Notification {
        ensureChannel()
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_notify_chat)
            .setContentTitle("待评审")
            .setContentText(text)
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
    }

    private fun ensureChannel() {
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL, "待评审", NotificationManager.IMPORTANCE_DEFAULT),
        )
    }

    companion object {
        private const val CHANNEL = "review"
    }
}
