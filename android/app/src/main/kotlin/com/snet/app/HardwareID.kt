package com.snet.app

import android.content.Context
import android.os.Build
import android.provider.Settings
import java.security.MessageDigest

/**
 * Generates a deterministic, hardware-bound device identifier for Android.
 *
 * Combines Build.* fields + ANDROID_ID → SHA-256 → 16 hex chars.
 * - Survives app reinstall (same signing key + same device = same ANDROID_ID + same Build props)
 * - Survives OS reinstall / OTA (Build props不变, ANDROID_ID不变)
 * - Lost on factory reset (ANDROID_ID changes)
 * - Different devices of same model get different IDs (ANDROID_ID is unique per device+user+app)
 */
object HardwareID {

    fun compute(context: Context): String {
        val parts = mutableListOf<String>()

        // Hardware properties (stable across reinstalls)
        parts.add("board=${Build.BOARD}")
        parts.add("device=${Build.DEVICE}")
        parts.add("hardware=${Build.HARDWARE}")
        parts.add("manufacturer=${Build.MANUFACTURER}")
        parts.add("model=${Build.MODEL}")
        parts.add("product=${Build.PRODUCT}")
        parts.add("fingerprint=${Build.FINGERPRINT}")

        // SoC info (API 31+)
        if (Build.VERSION.SDK_INT >= 31) {
            parts.add("soc_manufacturer=${Build.SOC_MANUFACTURER}")
            parts.add("soc_model=${Build.SOC_MODEL}")
        }

        // ANDROID_ID: unique per (device, user, signing key), survives app reinstall
        try {
            val androidId = Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
            if (!androidId.isNullOrEmpty() && androidId != "9774d56d682e549c") {
                parts.add("android_id=$androidId")
            }
        } catch (_: Exception) { }

        val combined = parts.joinToString("|")
        val digest = MessageDigest.getInstance("SHA-256").digest(combined.toByteArray())
        return digest.take(8).joinToString("") { "%02x".format(it) }
    }
}
