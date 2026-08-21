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

class JoinNetworkSheet : BottomSheetDialogFragment() {

    private lateinit var codeInput: TextInputEditText
    private lateinit var networkIdInput: TextInputEditText
    private lateinit var joinBtn: MaterialButton
    private lateinit var progressBar: ProgressBar
    private lateinit var errorText: TextView

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_join_network, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        codeInput = view.findViewById(R.id.codeInput)
        networkIdInput = view.findViewById(R.id.networkIdInput)
        joinBtn = view.findViewById(R.id.joinBtn)
        progressBar = view.findViewById(R.id.progressBar)
        errorText = view.findViewById(R.id.errorText)

        joinBtn.setOnClickListener { doJoin() }
        view.findViewById<View>(R.id.cancelBtn)?.setOnClickListener { dismiss() }
    }

    private fun doJoin() {
        val nid = networkIdInput.text.toString().trim()
        val code = codeInput.text.toString().trim()
        if (nid.isEmpty()) { networkIdInput.error = "网络ID必填"; return }
        if (code.isEmpty()) { codeInput.error = "邀请码必填"; return }

        joinBtn.isEnabled = false
        progressBar.visibility = View.VISIBLE
        errorText.visibility = View.GONE

        Thread {
            val act = activity ?: return@Thread
            val result = SnetBridge.joinNetwork(nid, code, "", 0)
            act.runOnUiThread {
                joinBtn.isEnabled = true
                progressBar.visibility = View.GONE
                try {
                    val json = JSONObject(result)
                    if (json.has("error")) {
                        errorText.text = json.optString("error", "加入失败")
                        errorText.visibility = View.VISIBLE
                    } else if (json.optString("status") == "pending") {
                        ToastHelper.showSuccess(act, "已提交加入申请，等待审批")
                        dismiss()
                    } else if (json.has("networkId")) {
                        ToastHelper.showSuccess(act, "已加入网络")
                        (act as? MainActivity)?.refreshHeader()
                        dismiss()
                    } else {
                        errorText.text = "未知响应"
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
        fun newInstance() = JoinNetworkSheet()
    }
}
