package com.snet.app

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ProgressBar
import android.widget.TextView
import com.google.android.material.bottomsheet.BottomSheetDialogFragment
import com.google.android.material.button.MaterialButton
import com.google.android.material.textfield.TextInputEditText
import org.json.JSONObject

class NetworkSettingsSheet : BottomSheetDialogFragment() {

    private lateinit var nameInput: TextInputEditText
    private lateinit var subnetInput: TextInputEditText
    private lateinit var saveBtn: MaterialButton
    private lateinit var progressBar: ProgressBar
    private lateinit var errorText: TextView
    private lateinit var deleteBtn: MaterialButton
    private lateinit var inviteBtn: MaterialButton
    private lateinit var resetCodeBtn: MaterialButton

    private lateinit var networkID: String
    private lateinit var networkName: String
    private lateinit var role: String

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        networkID = arguments?.getString("network_id") ?: ""
        networkName = arguments?.getString("network_name") ?: ""
        role = arguments?.getString("role") ?: ""
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_network_settings, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        nameInput = view.findViewById(R.id.settingsNameInput)
        subnetInput = view.findViewById(R.id.settingsSubnetInput)
        saveBtn = view.findViewById(R.id.settingsSaveBtn)
        progressBar = view.findViewById(R.id.progressBar)
        errorText = view.findViewById(R.id.errorText)
        deleteBtn = view.findViewById(R.id.deleteBtn)
        inviteBtn = view.findViewById(R.id.inviteBtn)
        resetCodeBtn = view.findViewById(R.id.resetCodeBtn)

        nameInput.setText(networkName)

        if (role != "owner") {
            deleteBtn.visibility = View.GONE
            resetCodeBtn.visibility = View.GONE
        }

        saveBtn.setOnClickListener { doSave() }
        deleteBtn.setOnClickListener { doDelete() }
        inviteBtn.setOnClickListener { showInvite() }
        resetCodeBtn.setOnClickListener { doResetCode() }
        view.findViewById<MaterialButton>(R.id.inviteLinkBtn)?.setOnClickListener { showInviteLink() }
        view.findViewById<View>(R.id.cancelBtn)?.setOnClickListener { dismiss() }
    }

    private fun doSave() {
        val name = nameInput.text.toString().trim()
        if (name.isEmpty()) { nameInput.error = "名称必填"; return }

        // Read subnets on main thread before background work
        val subnets = subnetInput.text.toString().trim()
        val subnetList = if (subnets.isNotEmpty()) {
            subnets.lines().map { it.trim() }.filter { it.isNotEmpty() }
        } else emptyList()

        saveBtn.isEnabled = false
        progressBar.visibility = View.VISIBLE
        errorText.visibility = View.GONE

        Thread {
            val act = activity ?: return@Thread
            val result = SnetBridge.updateNetwork(act, networkID, name, subnetList)
            act.runOnUiThread {
                saveBtn.isEnabled = true
                progressBar.visibility = View.GONE
                try {
                    val json = JSONObject(result)
                    if (json.optBoolean("ok", false)) {
                        ToastHelper.showSuccess(act, "设置已保存")
                        dismiss()
                    } else {
                        errorText.text = json.optString("error", "保存失败")
                        errorText.visibility = View.VISIBLE
                    }
                } catch (e: Exception) {
                    errorText.text = "未知错误: ${e.message}"
                    errorText.visibility = View.VISIBLE
                }
            }
        }.start()
    }

    private fun doDelete() {
        val act = activity ?: return
        ConfirmHelper.show(act, "确定要删除网络 \"$networkName\" 吗？") {
            Thread {
                val result = SnetBridge.deleteNetwork(networkID)
                act.runOnUiThread {
                    try {
                        val json = JSONObject(result)
                        if (json.optBoolean("ok", false)) {
                            ToastHelper.showSuccess(act, "网络已删除")
                            (act as? MainActivity)?.refreshHeader()
                            dismiss()
                        } else {
                            ToastHelper.showError(act, json.optString("error", "删除失败"))
                        }
                    } catch (e: Exception) {
                        ToastHelper.showError(act, "删除失败: ${e.message}")
                    }
                }
            }.start()
        }
    }

    private fun showInvite() {
        val sheet = InviteSheet.newInstance(networkID)
        sheet.show(childFragmentManager, "invite")
    }

    private fun showInviteLink() {
        val act = activity ?: return
        val sheet = InviteLinkSheet.newInstance(networkID, networkName)
        sheet.show(childFragmentManager, "invite_link")
    }

    private fun doResetCode() {
        val act = activity ?: return
        ConfirmHelper.show(act, "确定要重置邀请码吗？旧码将失效") {
            Thread {
                val result = SnetBridge.resetCode(networkID)
                act.runOnUiThread {
                    try {
                        val json = JSONObject(result)
                        if (json.optBoolean("ok", false)) {
                            val newCode = json.optString("pairingCode", json.optString("code", ""))
                            ToastHelper.showSuccess(act, "邀请码已重置: $newCode")
                        } else {
                            ToastHelper.showError(act, json.optString("error", "重置失败"))
                        }
                    } catch (e: Exception) {
                        ToastHelper.showError(act, "重置失败: ${e.message}")
                    }
                }
            }.start()
        }
    }

    companion object {
        fun newInstance(networkID: String, networkName: String, role: String) = NetworkSettingsSheet().apply {
            arguments = Bundle().apply {
                putString("network_id", networkID)
                putString("network_name", networkName)
                putString("role", role)
            }
        }
    }
}
