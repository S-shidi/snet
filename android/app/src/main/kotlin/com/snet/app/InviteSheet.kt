package com.snet.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ProgressBar
import android.widget.TextView
import com.google.android.material.bottomsheet.BottomSheetDialogFragment
import com.google.android.material.button.MaterialButton
import org.json.JSONObject

class InviteSheet : BottomSheetDialogFragment() {

    private lateinit var networkID: String
    private lateinit var codeText: TextView
    private lateinit var copyBtn: MaterialButton
    private lateinit var progressBar: ProgressBar
    private lateinit var errorText: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        networkID = arguments?.getString("network_id") ?: ""
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_invite, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        codeText = view.findViewById(R.id.inviteCodeText)
        copyBtn = view.findViewById(R.id.copyBtn)
        progressBar = view.findViewById(R.id.progressBar)
        errorText = view.findViewById(R.id.errorText)

        loadInfo()

        copyBtn.setOnClickListener {
            val ctx = requireContext()
            val clipboard = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            clipboard.setPrimaryClip(ClipData.newPlainText("invite_code", codeText.text))
            ToastHelper.showSuccess(ctx, "已复制")
        }

        view.findViewById<View>(R.id.closeBtn)?.setOnClickListener { dismiss() }
    }

    private fun loadInfo() {
        progressBar.visibility = View.VISIBLE
        errorText.visibility = View.GONE

        Thread {
            val act = activity ?: return@Thread
            val result = SnetBridge.getInviteCode(act, networkID)
            act.runOnUiThread {
                progressBar.visibility = View.GONE
                try {
                    val json = JSONObject(result)
                    // Go Info() returns NetworkInfoResp which embeds Network struct
                    // Network struct has: id, pairingCode, name, subnet, etc.
                    val code = json.optString("pairingCode", "")
                    val name = json.optString("name", "")
                    val subnet = json.optString("subnet", "")

                    if (code.isNotEmpty()) {
                        codeText.text = code
                    } else {
                        val details = buildString {
                            appendLine("网络: $name")
                            appendLine("子网: $subnet")
                            appendLine("ID: $networkID")
                        }
                        codeText.text = details
                    }
                } catch (e: Exception) {
                    codeText.text = "网络 ID: $networkID"
                }
            }
        }.start()
    }

    companion object {
        fun newInstance(networkID: String) = InviteSheet().apply {
            arguments = Bundle().apply { putString("network_id", networkID) }
        }
    }
}
