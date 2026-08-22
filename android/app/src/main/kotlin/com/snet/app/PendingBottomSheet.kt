package com.snet.app

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import com.google.android.material.bottomsheet.BottomSheetDialogFragment
import com.google.android.material.button.MaterialButton
import org.json.JSONObject

class PendingBottomSheet : BottomSheetDialogFragment() {

    private var networkID = ""
    private var isOwner = false

    private lateinit var pendingList: LinearLayout
    private lateinit var pendingEmpty: TextView
    private lateinit var progressBar: ProgressBar

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        networkID = arguments?.getString("network_id") ?: ""
        isOwner = arguments?.getBoolean("is_owner", false) ?: false
        setStyle(STYLE_NORMAL, R.style.SnetBottomSheet)
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.sheet_pending, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        pendingList = view.findViewById(R.id.pendingList)
        pendingEmpty = view.findViewById(R.id.pendingEmpty)
        progressBar = view.findViewById(R.id.progressBar)

        view.findViewById<View>(R.id.closeBtn)?.setOnClickListener { dismiss() }

        loadPending()
    }

    private fun loadPending() {
        progressBar.visibility = View.VISIBLE
        pendingEmpty.visibility = View.GONE
        pendingList.removeAllViews()

        Thread {
            val act = activity ?: return@Thread
            // Go Status() includes pendingJoins array
            val statusJson = SnetBridge.statusRaw()
            act.runOnUiThread {
                progressBar.visibility = View.GONE
                try {
                    val status = JSONObject(statusJson)
                    val pendingJoins = status.optJSONArray("pendingJoins") ?: org.json.JSONArray()

                    if (pendingJoins.length() == 0) {
                        pendingEmpty.visibility = View.VISIBLE
                        return@runOnUiThread
                    }

                    for (i in 0 until pendingJoins.length()) {
                        val pj = pendingJoins.getJSONObject(i)
                        val pjNid = pj.optString("networkId", "")

                        // Filter by network if specified
                        if (networkID.isNotEmpty() && pjNid != networkID) continue

                        val itemView = layoutInflater.inflate(R.layout.item_pending, pendingList, false)

                        val nameText = itemView.findViewById<TextView>(R.id.pendingName)
                        val infoText = itemView.findViewById<TextView>(R.id.pendingInfo)
                        val approveBtn = itemView.findViewById<MaterialButton>(R.id.approveBtn)
                        val denyBtn = itemView.findViewById<MaterialButton>(R.id.denyBtn)
                        val cancelBtn = itemView.findViewById<MaterialButton>(R.id.cancelBtn)

                        nameText.text = pj.optString("networkName", pj.optString("name", pjNid))
                        infoText.text = "设备: ${pj.optString("deviceId", "?")}"

                        if (isOwner) {
                            approveBtn.visibility = View.VISIBLE
                            denyBtn.visibility = View.VISIBLE
                            cancelBtn.visibility = View.GONE

                            approveBtn.setOnClickListener {
                                doAction("approve", pj.optString("pendingId", pj.optString("id", "")))
                            }
                            denyBtn.setOnClickListener {
                                doAction("deny", pj.optString("pendingId", pj.optString("id", "")))
                            }
                        } else {
                            approveBtn.visibility = View.GONE
                            denyBtn.visibility = View.GONE
                            cancelBtn.visibility = View.VISIBLE

                            cancelBtn.setOnClickListener {
                                doAction("cancel", pj.optString("pendingId", pj.optString("id", "")))
                            }
                        }

                        pendingList.addView(itemView)
                    }

                    if (pendingList.childCount == 0) {
                        pendingEmpty.visibility = View.VISIBLE
                    }
                } catch (e: Exception) {
                    pendingEmpty.text = "加载失败"
                    pendingEmpty.visibility = View.VISIBLE
                }
            }
        }.start()
    }

    private fun doAction(action: String, pendingID: String) {
        val act = activity ?: return
        Thread {
            val result = when (action) {
                "approve" -> SnetBridge.approvePending(networkID, pendingID)
                "deny" -> SnetBridge.denyPending(networkID, pendingID)
                "cancel" -> SnetBridge.cancelPending(pendingID)
                else -> """{"error":"unknown action"}"""
            }
            act.runOnUiThread {
                try {
                    val json = JSONObject(result)
                    if (json.optBoolean("ok", false)) {
                        ToastHelper.showSuccess(act, when (action) {
                            "approve" -> "已批准"
                            "deny" -> "已拒绝"
                            "cancel" -> "已取消"
                            else -> "操作完成"
                        })
                        loadPending()
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
        fun newInstance(networkID: String, isOwner: Boolean) = PendingBottomSheet().apply {
            arguments = Bundle().apply {
                putString("network_id", networkID)
                putBoolean("is_owner", isOwner)
            }
        }
    }
}
