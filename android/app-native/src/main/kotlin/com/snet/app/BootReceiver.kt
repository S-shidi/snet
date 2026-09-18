package com.snet.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) {
            Log.d("BootReceiver", "Boot completed, checking for auto-connect")

            val prefs = context.getSharedPreferences("snet_prefs", Context.MODE_PRIVATE)
            val autoConnect = prefs.getBoolean("auto_connect", false)

            if (autoConnect) {
                Log.d("BootReceiver", "Auto-connect enabled, starting VPN service")
                val serviceIntent = Intent(context, SnetVpnService::class.java).apply {
                    action = "START"
                }
                try {
                    context.startForegroundService(serviceIntent)
                } catch (e: Exception) {
                    Log.e("BootReceiver", "Failed to start VPN service on boot", e)
                }
            }
        }
    }
}
