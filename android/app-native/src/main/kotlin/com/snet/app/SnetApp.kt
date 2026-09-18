package com.snet.app

import android.app.Application

class SnetApp : Application() {
    companion object {
        @Volatile
        private var instance: SnetApp? = null
        fun getInstance(): SnetApp = instance ?: throw IllegalStateException("Application not initialized")
    }
    
    override fun onCreate() {
        super.onCreate()
        instance = this
        // 极速启动 - 不做任何初始化
    }
}
