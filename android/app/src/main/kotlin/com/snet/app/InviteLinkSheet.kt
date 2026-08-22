package com.snet.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ProgressBar
import android.widget.TextView
import com.google.android.material.bottomsheet.BottomSheetDialogFragment
import com.google.android.material.button.MaterialButton
import org.json.JSONObject

class InviteLinkSheet : BottomSheetDialogFragment() {

    private var networkID = ""
    private var networkName = ""

    private lateinit var linkText: TextView
    private lateinit var codeText: TextView
    private lateinit var copyLinkBtn: MaterialButton
    private lateinit var copyCodeBtn: MaterialButton
    private lateinit var shareBtn: MaterialButton
    private lateinit var progressBar: ProgressBar
    private lateinit var errorText: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        networkID = arguments?.getString("network_id") ?: ""
        networkName = arguments?.getString("network_name") ?: ""
        setStyle(STYLE_NORMAL, R.style.SnetBottomSheet)
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_invite_link, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        linkText = view.findViewById(R.id.linkText)
        codeText = view.findViewById(R.id.codeText)
        copyLinkBtn = view.findViewById(R.id.copyLinkBtn)
        copyCodeBtn = view.findViewById(R.id.copyCodeBtn)
        shareBtn = view.findViewById(R.id.shareBtn)
        progressBar = view.findViewById(R.id.progressBar)
        errorText = view.findViewById(R.id.errorText)

        view.findViewById<View>(R.id.closeBtn)?.setOnClickListener { dismiss() }

        loadInviteInfo()

        copyLinkBtn.setOnClickListener {
            copyToClipboard("snet://join/$networkID", "邀请链接已复制")
        }

        copyCodeBtn.setOnClickListener {
            val code = codeText.text.toString()
            if (code.isNotEmpty() && code != "--") {
                copyToClipboard(code, "邀请码已复制")
            }
        }

        shareBtn.setOnClickListener {
            val link = "snet://join/$networkID"
            val shareIntent = Intent(Intent.ACTION_SEND).apply {
                type = "text/plain"
                putExtra(Intent.EXTRA_TEXT, "加入网络「$networkName」\n\nsnet://join/$networkID\n\n邀请码: ${codeText.text}")
                putExtra(Intent.EXTRA_SUBJECT, "SNET 邀请")
            }
            startActivity(Intent.createChooser(shareIntent, "分享邀请"))
        }
    }

    private fun loadInviteInfo() {
        progressBar.visibility = View.VISIBLE
        errorText.visibility = View.GONE

        Thread {
            val act = activity ?: return@Thread
            val result = SnetBridge.info(networkID)
            act.runOnUiThread {
                progressBar.visibility = View.GONE
                try {
                    val json = JSONObject(result)
                    val code = json.optString("pairingCode", "")
                    val name = json.optString("name", networkName)

                    linkText.text = "snet://join/$networkID"
                    codeText.text = if (code.isNotEmpty()) code else "--"
                    networkName = name
                } catch (e: Exception) {
                    linkText.text = "snet://join/$networkID"
                    codeText.text = "--"
                }
            }
        }.start()
    }

    private fun copyToClipboard(text: String, msg: String) {
        val ctx = context ?: return
        val clipboard = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("snet_invite", text))
        ToastHelper.showSuccess(ctx, msg)
    }

    companion object {
        fun newInstance(networkID: String, networkName: String) = InviteLinkSheet().apply {
            arguments = Bundle().apply {
                putString("network_id", networkID)
                putString("network_name", networkName)
            }
        }
    }
}
