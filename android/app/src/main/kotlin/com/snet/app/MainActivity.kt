package com.snet.app

import android.annotation.SuppressLint
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.util.Log
import android.webkit.ConsoleMessage
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity

class MainActivity : AppCompatActivity() {
    private companion object {
        const val TAG = "MainActivity"
        const val VPN_REQUEST_CODE = 100
    }

    private lateinit var webView: WebView
    private var pendingVpnStart = false

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Initialize SnetBridge before WebView loads
        if (SnetBridge.getStatus(this) == null) {
            SnetBridge.init(this, filesDir.absolutePath)
        }

        // Simple full-screen WebView
        webView = WebView(this)
        setContentView(webView)

        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = true
            allowContentAccess = true
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

        // Register JavaScript interface
        webView.addJavascriptInterface(WebBridge(this), "WebBridge")

        // Load the shared web app from assets
        webView.loadUrl("file:///android_asset/web/index.html")
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == VPN_REQUEST_CODE) {
            if (resultCode == RESULT_OK) {
                // VPN permission granted, start the service
                val intent = Intent(this, SnetVpnService::class.java)
                intent.action = "START"
                startForegroundService(intent)
            } else {
                Log.w(TAG, "VPN permission denied")
                // Notify JavaScript
                webView.evaluateJavascript("""
                    (function() {
                        if (typeof toast === 'function') toast('需要 VPN 权限', 'err');
                    })();
                """, null)
            }
        }
    }

    override fun onBackPressed() {
        if (webView.canGoBack()) {
            webView.goBack()
        } else {
            super.onBackPressed()
        }
    }

    override fun onDestroy() {
        webView.destroy()
        super.onDestroy()
    }

    /**
     * Called from WebBridge to request VPN permission
     */
    fun requestVpnPermission() {
        val intent = VpnService.prepare(this)
        if (intent != null) {
            pendingVpnStart = true
            startActivityForResult(intent, VPN_REQUEST_CODE)
        } else {
            // Permission already granted
            val startIntent = Intent(this, SnetVpnService::class.java)
            startIntent.action = "START"
            startForegroundService(startIntent)
        }
    }

    /**
     * Called from JavaScript to evaluate code on the UI thread
     */
    fun evaluateJs(script: String) {
        runOnUiThread { webView.evaluateJavascript(script, null) }
    }
}
