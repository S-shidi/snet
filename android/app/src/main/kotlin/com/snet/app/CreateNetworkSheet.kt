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

class CreateNetworkSheet : BottomSheetDialogFragment() {

    private lateinit var nameInput: TextInputEditText
    private lateinit var subnetInput: TextInputEditText
    private lateinit var createBtn: MaterialButton
    private lateinit var progressBar: ProgressBar
    private lateinit var errorText: TextView

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_create_network, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        nameInput = view.findViewById(R.id.nameInput)
        subnetInput = view.findViewById(R.id.subnetInput)
        createBtn = view.findViewById(R.id.createBtn)
        progressBar = view.findViewById(R.id.progressBar)
        errorText = view.findViewById(R.id.errorText)

        createBtn.setOnClickListener { doCreate() }
        view.findViewById<View>(R.id.cancelBtn)?.setOnClickListener { dismiss() }
    }

    private fun doCreate() {
        val name = nameInput.text.toString().trim()
        if (name.isEmpty()) {
            nameInput.error = "名称必填"; return
        }
        val subnet = subnetInput.text.toString().trim()

        createBtn.isEnabled = false
        progressBar.visibility = View.VISIBLE
        errorText.visibility = View.GONE

        Thread {
            val act = activity ?: return@Thread
            val result = SnetBridge.createNetwork(name, subnet, false, "", 0)
            act.runOnUiThread {
                createBtn.isEnabled = true
                progressBar.visibility = View.GONE
                try {
                    val json = JSONObject(result)
                    if (json.has("networkId")) {
                        val code = json.optString("pairingCode", "")
                        val msg = if (code.isNotEmpty()) "网络已创建，邀请码: $code" else "网络已创建"
                        ToastHelper.showSuccess(act, msg)
                        (act as? MainActivity)?.refreshHeader()
                        dismiss()
                    } else {
                        errorText.text = json.optString("error", "创建失败")
                        errorText.visibility = View.VISIBLE
                    }
                } catch (e: Exception) {
                    errorText.text = "未知错误: ${e.message}"
                    errorText.visibility = View.VISIBLE
                }
            }
        }.start()
    }

    companion object {
        fun newInstance() = CreateNetworkSheet()
    }
}
