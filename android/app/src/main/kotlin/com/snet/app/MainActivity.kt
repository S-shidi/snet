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
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanIntentResult
import com.journeyapps.barcodescanner.ScanOptions
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CancellationException
import java.util.concurrent.atomic.AtomicReference

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

    // Pending QR scan future (settled from scan result or camera-permission result).
    private val scanQRFutureRef = AtomicReference<CompletableFuture<String>>(null)

    private val cameraPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        val f = scanQRFutureRef.getAndSet(null) ?: return@registerForActivityResult
        if (granted) {
            runOnUiThread { launchZXingScan() }
        } else {
            val msg = "需要相机权限才能扫码"
            f.completeExceptionally(SecurityException(msg))
            safeScanResolve(null, msg)
        }
    }

    private val scanResultLauncher = registerForActivityResult(
        ScanContract()
    ) { result: ScanIntentResult ->
        val f = scanQRFutureRef.getAndSet(null) ?: return@registerForActivityResult
        val contents = result.contents
        if (contents != null) {
            f.complete(contents)
            safeScanResolve(contents, null)
        } else {
            f.completeExceptionally(CancellationException("扫码已取消"))
            safeScanResolve(null, "扫码已取消")
        }
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

    /** Asks the WebView JS layer to refresh its state (used after background
     *  daemon ops complete so the optimistic UI reconciles quickly). */
    fun refreshWebView() {
        if (::webView.isInitialized) {
            webView.evaluateJavascript(
                "if (typeof snetRefresh === 'function') snetRefresh();",
                null,
            )
        }
    }

    fun requestVpnPermission() {
        // Safe to call from any thread (JavascriptInterface methods run on a
        // WebView background thread): prepare()/launch() must run on the UI
        // thread because startActivityForResult requires an Activity context.
        runOnUiThread {
            val intent = VpnService.prepare(this)
            if (intent != null) {
                vpnPermissionLauncher.launch(intent)
            } else {
                val startIntent = Intent(this, SnetVpnService::class.java)
                startIntent.action = "START"
                startForegroundService(startIntent)
            }
        }
    }

    /**
     * Starts a native QR scan and returns a [CompletableFuture] that completes
     * with the scanned text, or fails exceptionally if the user cancels or denies
     * the camera permission. Safe to call from any thread (WebBridge runs on a
     * background thread).
     */
    fun startScanQR(): CompletableFuture<String> {
        val f = CompletableFuture<String>()
        scanQRFutureRef.set(f)
        runOnUiThread {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA)
                == PackageManager.PERMISSION_GRANTED
            ) {
                launchZXingScan()
            } else {
                cameraPermissionLauncher.launch(Manifest.permission.CAMERA)
            }
        }
        return f
    }

    private fun launchZXingScan() {
        val options = ScanOptions().apply {
            setDesiredBarcodeFormats(ScanOptions.QR_CODE)
            setPrompt("对准二维码进行扫描")
            setBeepEnabled(false)
        }
        scanResultLauncher.launch(options)
    }

    // Push the scan result back to the shared UI via the one-shot JS callback.
    private fun safeScanResolve(text: String?, err: String?) {
        runOnUiThread {
            val t = text?.let { "\"${it.replace("\"", "\\\"").replace("\\", "\\\\")}\"" } ?: "null"
            val e = err?.let { "\"${it.replace("\"", "\\\"").replace("\\", "\\\\")}\"" } ?: "null"
            webView.evaluateJavascript(
                "window.__snetScanResolve && window.__snetScanResolve($t, $e)", null)
        }
    }

    fun evaluateJs(script: String) {
        runOnUiThread { webView.evaluateJavascript(script, null) }
    }
}
