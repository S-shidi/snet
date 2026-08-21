package com.snet.app

import android.content.Context
import androidx.appcompat.app.AlertDialog

object ConfirmHelper {
    fun show(context: Context, message: String, onConfirm: () -> Unit) {
        AlertDialog.Builder(context, com.google.android.material.R.style.ThemeOverlay_Material3_MaterialAlertDialog)
            .setTitle("确认")
            .setMessage(message)
            .setPositiveButton("确定") { _, _ -> onConfirm() }
            .setNegativeButton("取消", null)
            .show()
    }
}
