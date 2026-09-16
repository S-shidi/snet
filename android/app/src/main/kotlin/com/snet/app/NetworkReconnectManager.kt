package com.snet.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Handler
import android.os.Looper
import android.util.Log
import java.util.concurrent.atomic.AtomicInteger

/**
 * Network reconnection manager with exponential backoff strategy.
 * Monitors network changes and automatically reconnects VPN when network is restored.
 */
class NetworkReconnectManager(
    private val context: Context,
    private val onReconnect: () -> Boolean
) {
    companion object {
        private const val TAG = "NetworkReconnect"
        
        // Exponential backoff parameters
        private const val INITIAL_DELAY = 1000L  // 1 second
        private const val MAX_DELAY = 60000L      // 60 seconds
        private const val BACKOFF_MULTIPLIER = 2.0
        
        // Network check parameters
        private const val NETWORK_CHECK_INTERVAL = 5000L  // 5 seconds
    }

    private val handler = Handler(Looper.getMainLooper())
    private val connectivityManager = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
    
    private val attemptCount = AtomicInteger(0)
    private var isReconnecting = false
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    
    @Volatile
    private var isNetworkAvailable = true

    /**
     * Start monitoring network changes.
     */
    fun startMonitoring() {
        Log.d(TAG, "Starting network monitoring")
        
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
            .build()

        networkCallback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                Log.d(TAG, "Network available: $network")
                isNetworkAvailable = true
                
                // If we were reconnecting, network is restored
                if (isReconnecting) {
                    handler.post {
                        attemptReconnect()
                    }
                }
            }

            override fun onLost(network: Network) {
                Log.w(TAG, "Network lost: $network")
                isNetworkAvailable = false
                
                // Network lost, mark for reconnection when it returns
                if (SnetVpnService.isRunning) {
                    isReconnecting = true
                    notifyNetworkLost()
                }
            }

            override fun onUnavailable() {
                Log.w(TAG, "Network unavailable")
                isNetworkAvailable = false
            }
        }

        try {
            connectivityManager.registerNetworkCallback(request, networkCallback!!)
        } catch (e: Exception) {
            Log.e(TAG, "Failed to register network callback", e)
        }

        // Initial network state check
        checkNetworkState()
    }

    /**
     * Stop monitoring network changes.
     */
    fun stopMonitoring() {
        Log.d(TAG, "Stopping network monitoring")
        
        networkCallback?.let {
            try {
                connectivityManager.unregisterNetworkCallback(it)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to unregister network callback", e)
            }
        }
        networkCallback = null
        
        handler.removeCallbacksAndMessages(null)
        isReconnecting = false
        attemptCount.set(0)
    }

    /**
     * Check current network state.
     */
    fun checkNetworkState(): Boolean {
        val network = connectivityManager.activeNetwork
        val capabilities = connectivityManager.getNetworkCapabilities(network)
        
        isNetworkAvailable = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true &&
                capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        
        Log.d(TAG, "Network state: available=$isNetworkAvailable")
        return isNetworkAvailable
    }

    /**
     * Schedule a reconnection attempt with exponential backoff.
     */
    fun scheduleReconnect() {
        if (!isNetworkAvailable) {
            Log.d(TAG, "Network not available, waiting for network restoration")
            isReconnecting = true
            return
        }

        val attempt = attemptCount.incrementAndGet()
        val delay = calculateBackoffDelay(attempt)
        
        Log.d(TAG, "Scheduling reconnect attempt $attempt in ${delay}ms")
        
        handler.postDelayed({
            attemptReconnect()
        }, delay)
    }

    /**
     * Attempt to reconnect.
     */
    private fun attemptReconnect() {
        if (!isNetworkAvailable) {
            Log.w(TAG, "Network still not available, skipping reconnect")
            return
        }

        if (!SnetVpnService.isRunning) {
            Log.d(TAG, "VPN not running, attempting reconnect")
            
            try {
                val success = onReconnect()
                if (success) {
                    Log.d(TAG, "Reconnect successful")
                    isReconnecting = false
                    attemptCount.set(0)
                } else {
                    Log.w(TAG, "Reconnect failed, will retry")
                    scheduleReconnect()
                }
            } catch (e: Exception) {
                Log.e(TAG, "Reconnect error", e)
                scheduleReconnect()
            }
        } else {
            Log.d(TAG, "VPN already running, no need to reconnect")
            isReconnecting = false
            attemptCount.set(0)
        }
    }

    /**
     * Calculate exponential backoff delay.
     */
    private fun calculateBackoffDelay(attempt: Int): Long {
        val delay = (INITIAL_DELAY * Math.pow(BACKOFF_MULTIPLIER, (attempt - 1).toDouble())).toLong()
        return minOf(delay, MAX_DELAY)
    }

    /**
     * Notify UI about network loss.
     */
    private fun notifyNetworkLost() {
        // Notify the WebView about network loss
        (context as? MainActivity)?.evaluateJs(
            "if (typeof onNetworkLost === 'function') onNetworkLost();"
        )
    }

    /**
     * Reset reconnection state after successful connection.
     */
    fun reset() {
        isReconnecting = false
        attemptCount.set(0)
        handler.removeCallbacksAndMessages(null)
    }

    /**
     * Check if currently reconnecting.
     */
    fun isReconnecting(): Boolean = isReconnecting

    /**
     * Get current attempt count.
     */
    fun getAttemptCount(): Int = attemptCount.get()
}