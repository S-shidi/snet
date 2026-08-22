package com.snet.app

import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import com.google.android.material.bottomsheet.BottomSheetDialogFragment
import com.google.android.material.button.MaterialButton
import org.json.JSONObject

class MembersBottomSheet : BottomSheetDialogFragment() {

    private val handler = Handler(Looper.getMainLooper())
    private var networkID = ""
    private var isOwner = false

    private lateinit var memberList: LinearLayout
    private lateinit var memberEmpty: TextView
    private lateinit var progressBar: ProgressBar
    private lateinit var refreshBtn: MaterialButton

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        networkID = arguments?.getString("network_id") ?: ""
        isOwner = arguments?.getBoolean("is_owner", false) ?: false
        setStyle(STYLE_NORMAL, R.style.SnetBottomSheet)
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_members, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        memberList = view.findViewById(R.id.memberList)
        memberEmpty = view.findViewById(R.id.memberEmpty)
        progressBar = view.findViewById(R.id.progressBar)
        refreshBtn = view.findViewById(R.id.refreshBtn)

        view.findViewById<View>(R.id.closeBtn)?.setOnClickListener { dismiss() }
        refreshBtn.setOnClickListener { loadMembers() }

        loadMembers()
    }

    private fun loadMembers() {
        progressBar.visibility = View.VISIBLE
        memberEmpty.visibility = View.GONE
        memberList.removeAllViews()

        Thread {
            val act = activity ?: return@Thread
            val result = SnetBridge.peers(networkID)
            act.runOnUiThread {
                progressBar.visibility = View.GONE
                try {
                    val obj = JSONObject(result)
                    val peers = obj.optJSONArray("peers") ?: obj.optJSONArray("nodes")

                    if (peers == null || peers.length() == 0) {
                        memberEmpty.visibility = View.VISIBLE
                        return@runOnUiThread
                    }

                    for (i in 0 until peers.length()) {
                        val peer = peers.getJSONObject(i)
                        val itemView = layoutInflater.inflate(R.layout.item_member, memberList, false)

                        val nameText = itemView.findViewById<TextView>(R.id.memberName)
                        val infoText = itemView.findViewById<TextView>(R.id.memberInfo)
                        val statusText = itemView.findViewById<TextView>(R.id.memberStatus)
                        val kickBtn = itemView.findViewById<MaterialButton>(R.id.kickBtn)

                        nameText.text = peer.optString("id", "unknown")
                        infoText.text = "${peer.optString("ip", "?")}  |  ${peer.optString("publicKey", "?").take(16)}..."

                        val online = peer.optBoolean("online", false)
                        statusText.text = if (online) "在线" else "离线"
                        statusText.setTextColor(if (online) 0xFF3fb68b.toInt() else 0xFF6b7690.toInt())

                        if (isOwner && peer.optBoolean("self", false) != true) {
                            kickBtn.visibility = View.VISIBLE
                            kickBtn.setOnClickListener {
                                ConfirmHelper.show(act, "踢出节点 ${peer.optString("id", "")}?") {
                                    kickMember(peer.optString("id", ""))
                                }
                            }
                        } else {
                            kickBtn.visibility = View.GONE
                        }

                        memberList.addView(itemView)
                    }
                } catch (e: Exception) {
                    memberEmpty.text = "加载失败"
                    memberEmpty.visibility = View.VISIBLE
                }
            }
        }.start()
    }

    private fun kickMember(nodeID: String) {
        val act = activity ?: return
        Thread {
            val result = SnetBridge.kick(networkID, nodeID)
            act.runOnUiThread {
                try {
                    val json = JSONObject(result)
                    if (json.optBoolean("ok", false)) {
                        ToastHelper.showSuccess(act, "已踢出")
                        loadMembers()
                    } else {
                        ToastHelper.showError(act, json.optString("error", "操作失败"))
                    }
                } catch (e: Exception) {
                    ToastHelper.showError(act, "操作失败: ${e.message}")
                }
            }
        }.start()
    }

    companion object {
        fun newInstance(networkID: String, isOwner: Boolean) = MembersBottomSheet().apply {
            arguments = Bundle().apply {
                putString("network_id", networkID)
                putBoolean("is_owner", isOwner)
            }
        }
    }
}
