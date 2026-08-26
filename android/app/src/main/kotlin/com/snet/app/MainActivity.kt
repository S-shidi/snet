package com.snet.app

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.webkit.ConsoleMessage
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat

class MainActivity : AppCompatActivity() {
    private companion object {
        const val TAG = "MainActivity"
    }

    private lateinit var webView: WebView

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            val intent = Intent(this, SnetVpnService::class.java)
            intent.action = "START"
            startForegroundService(intent)
        } else {
            Log.w(TAG, "VPN permission denied")
            webView.evaluateJavascript("""
                (function() {
                    if (typeof toast === 'function') toast('需要 VPN 权限', 'err');
                })();
            """, null)
        }
    }

    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        if (!granted) Log.w(TAG, "POST_NOTIFICATIONS permission denied")
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        if (SnetBridge.getStatus(this) == null) {
            SnetBridge.init(this, filesDir.absolutePath)
        }

        webView = WebView(this)
        setContentView(webView)

        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = true
            allowContentAccess = false
            cacheMode = WebSettings.LOAD_DEFAULT
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
        }

        webView.webChromeClient = object : WebChromeClient() {
            override fun onConsoleMessage(cm: ConsoleMessage?): Boolean {
                cm?.let { Log.d(TAG, "JS: [${it.messageLevel()}] ${it.message()} [${it.sourceId()}:${it.lineNumber()}]") }
                return true
            }
        }

        webView.webViewClient = object : WebViewClient() {
            override fun onPageStarted(view: WebView?, url: String?, favicon: android.graphics.Bitmap?) {
                Log.d(TAG, "onPageStarted: $url")
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                Log.d(TAG, "onPageFinished: $url")
            }

            override fun onReceivedError(view: WebView?, errorCode: Int, description: String?, failingUrl: String?) {
                Log.d(TAG, "onReceivedError: $errorCode $description $failingUrl")
            }
        }

        webView.addJavascriptInterface(WebBridge(this), "WebBridge")
        webView.loadUrl("file:///android_asset/web/index.html")

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (webView.canGoBack()) webView.goBack() else isEnabled = false
            }
        })

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED
            ) {
                notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
    }

    override fun onDestroy() {
        webView.destroy()
        super.onDestroy()
    }

    fun requestVpnPermission() {
        val intent = VpnService.prepare(this)
        if (intent != null) {
            vpnPermissionLauncher.launch(intent)
        } else {
            val startIntent = Intent(this, SnetVpnService::class.java)
            startIntent.action = "START"
            startForegroundService(startIntent)
        }
    }

    fun evaluateJs(script: String) {
        runOnUiThread { webView.evaluateJavascript(script, null) }
    }
}
