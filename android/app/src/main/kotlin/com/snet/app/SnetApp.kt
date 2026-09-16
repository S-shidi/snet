package com.snet.app

import android.app.Application
import android.content.Context
import android.webkit.WebView
import android.webkit.WebSettings
import android.os.StrictMode

/**
 * SNET Application class for global initialization and WebView preloading.
 */
class SnetApp : Application() {

    companion object {
        private const val TAG = "SnetApp"
        
        @Volatile
        private var instance: SnetApp? = null

        fun getInstance(): SnetApp = instance ?: throw IllegalStateException("Application not initialized")
    }

    override fun onCreate() {
        super.onCreate()
        instance = this

        // Initialize SnetBridge early (Go daemon runtime)
        SnetBridge.init(this, filesDir.absolutePath)

        // Preload WebView for faster startup
        WebViewPreloader.init(this)
    }
}

/**
 * WebView preloader for faster startup.
 * Creates and configures a WebView instance during app initialization
 * so it's ready when MainActivity launches.
 */
object WebViewPreloader {
    private const val TAG = "WebViewPreloader"
    
    @Volatile
    private var webView: WebView? = null
    
    @Volatile
    private var isPreloaded = false

    /**
     * Initialize WebView in background thread during app startup.
     * This reduces MainActivity launch time by pre-creating the WebView.
     */
    fun init(context: Context) {
        if (isPreloaded) return

        // Create WebView in a background thread to avoid blocking app startup
        Thread {
            try {
                // Temporarily disable strict mode for WebView creation
                val oldPolicy = StrictMode.getThreadPolicy()
                StrictMode.setThreadPolicy(StrictMode.ThreadPolicy.Builder().permitAll().build())

                // Create WebView with application context
                val appContext = context.applicationContext
                webView = WebView(appContext)

                // Pre-configure WebView settings
                webView?.settings?.apply {
                    javaScriptEnabled = true
                    domStorageEnabled = true
                    allowFileAccess = true
                    allowContentAccess = false
                    cacheMode = WebSettings.LOAD_CACHE_ELSE_NETWORK // Prefer cache for faster load
                    mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
                    
                    // Performance optimizations
                    setSupportZoom(false)
                    builtInZoomControls = false
                    displayZoomControls = false
                    useWideViewPort = true
                    loadWithOverviewMode = true
                    
                    // Enable hardware acceleration
                    setLayerType(android.view.View.LAYER_TYPE_HARDWARE, null)
                }

                // Restore strict mode
                StrictMode.setThreadPolicy(oldPolicy)

                isPreloaded = true
                android.util.Log.d(TAG, "WebView preloaded successfully")
            } catch (e: Exception) {
                android.util.Log.e(TAG, "Failed to preload WebView", e)
                webView = null
            }
        }.start()
    }

    /**
     * Get the preloaded WebView instance.
     * Returns null if preloading failed or hasn't completed.
     */
    fun get(): WebView? = webView

    /**
     * Check if WebView is preloaded and ready.
     */
    fun isReady(): Boolean = webView != null && isPreloaded

    /**
     * Clear the preloaded WebView reference after MainActivity takes ownership.
     */
    fun clear() {
        webView = null
        isPreloaded = false
    }
}