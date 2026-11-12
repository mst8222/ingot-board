package io.ingotboard.phone

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.SwitchCompat
import androidx.core.content.edit

/**
 * 安卓壳只加载桌面提供的手机网页，不实现任务状态机。
 */
class MainActivity : AppCompatActivity() {
    private val prefs by lazy { getSharedPreferences("phone", MODE_PRIVATE) }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        val input = findViewById<EditText>(R.id.baseUrl)
        val web = findViewById<WebView>(R.id.web)
        val keep = findViewById<SwitchCompat>(R.id.keep)
        input.setText(prefs.getString("base_url", ""))
        keep.isChecked = prefs.getBoolean("keep_receiving", false)

        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true
        web.webViewClient = WebViewClient()
        web.webChromeClient = WebChromeClient()
        web.addJavascriptInterface(Bridge(), "AgentTodoAndroid")

        findViewById<Button>(R.id.open).setOnClickListener {
            val url = input.text.toString().trim()
            if (!UrlPolicy.accept(url)) {
                Toast.makeText(this, "远程地址须为 https 且以 .ts.net 结尾，局域网须为私网 http", Toast.LENGTH_LONG).show()
                return@setOnClickListener
            }
            prefs.edit { putString("base_url", url) }
            web.loadUrl(url)
        }

        keep.setOnCheckedChangeListener { _, checked ->
            prefs.edit { putBoolean("keep_receiving", checked) }
            val intent = Intent(this, ReviewService::class.java)
            if (!checked) {
                stopService(intent)
                return@setOnCheckedChangeListener
            }
            if (Build.VERSION.SDK_INT >= 33 &&
                checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
            ) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
            }
            startForegroundService(intent)
        }

        val saved = prefs.getString("base_url", "").orEmpty()
        if (UrlPolicy.accept(saved)) {
            web.loadUrl(saved)
        }
    }

    private inner class Bridge {
        @JavascriptInterface
        fun onPaired(token: String) {
            prefs.edit { putString("token", token) }
        }
    }
}
