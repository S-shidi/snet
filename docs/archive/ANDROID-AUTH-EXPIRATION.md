# Android 客户端授权码过期功能

## 功能概述

Android 客户端已实现授权码过期检测功能，与后端服务配合：

1. **后台定期检查**：每小时查询一次服务器授权状态
2. **过期提示 UI**：检测到过期时显示红色警告卡片
3. **自动停止网络**：后台检查循环检测到过期时自动停止所有网络

---

## 代码变更

### 1. **SnetBridge.kt** - 添加查询方法

`getAuthStatus` 曾是一段恒返回 `{}` 的 TODO stub；现已实现，转发到 Go 绑定层 `SnetCore.GetAuthStatus`（`snetbind/snetcore.go`），并使用固定协调服务器地址 `SnetRepository.SERVER_ADDR`（`https://snet.uizhi.eu.org:8090`）查询：

```kotlin
fun getAuthStatus(): String {
    val c = core ?: return """{"error":"core not initialized"}"""
    return try {
        c.getAuthStatus(SnetRepository.SERVER_ADDR, "") ?: "{}"
    } catch (e: Exception) {
        """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
    }
}
```

### 2. **Models.kt** - 添加数据类

```kotlin
data class AuthStatus(
    val bound: Boolean = false,
    val expired: Boolean = false,
    val authCodeId: String = ""
)
```

### 3. **SnetRepository.kt** - 添加查询接口

```kotlin
suspend fun checkAuthStatus(): AuthStatus = withContext(Dispatchers.IO) {
    try {
        val result = SnetBridge.getAuthStatus()
        val json = JSONObject(result)
        AuthStatus(
            bound = json.optBoolean("bound", false),
            expired = json.optBoolean("expired", false),
            authCodeId = json.optString("authCodeId", "")
        )
    } catch (e: Exception) {
        AuthStatus(bound = false, expired = false)
    }
}
```

### 4. **MainViewModel.kt** - 添加定期检查

```kotlin
private val _authExpired = MutableStateFlow(false)
val authExpired: StateFlow<Boolean> = _authExpired.asStateFlow()

private fun startAuthCheck() {
    viewModelScope.launch {
        while (true) {
            kotlinx.coroutines.delay(60 * 60 * 1000L) // 每小时检查一次
            try {
                val status = repository.checkAuthStatus()
                if (status.expired) {
                    _authExpired.value = true
                    showMessage("授权码已过期，所有网络已离线，请续期或更换授权码")
                }
            } catch (e: Exception) {
                Log.e(TAG, "授权状态检查失败: ${e.message}")
            }
        }
    }
}
```

### 5. **MainScreen.kt** - 添加过期提示 UI

```kotlin
if (authExpired) {
    Card(
        modifier = Modifier.fillMaxWidth().padding(16.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer)
    ) {
        Row(
            modifier = Modifier.padding(16.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(Icons.Default.Warning, contentDescription = "警告", ...)
            Column {
                Text("授权码已过期", ...)
                Text("所有网络已离线，请联系管理员续期或更换授权码", ...)
            }
        }
    }
}
```

---

## 编译步骤

### 前置要求

1. **Go 环境**：Go 1.21+
2. **Android SDK**：API 21+
3. **gomobile 工具**：`go install golang.org/x/mobile/cmd/gomobile@latest`

### 编译 AAR 包

```bash
# 1. 设置 Android SDK 路径
export ANDROID_HOME=/path/to/Android/sdk
export ANDROID_SDK_ROOT=$ANDROID_HOME

# 2. 编译 AAR
cd /Users/shidi/OpenWork/Snet
~/go/bin/gomobile bind -target=android -o android/app-native/libs/snet.aar ./snetbind/

# 3. 验证
ls -lh android/app-native/libs/snet.aar
```

### 编译 Android 应用

```bash
cd android/app-native
./gradlew assembleDebug

# APK 输出位置
ls -lh app/build/outputs/apk/debug/app-debug.apk
```

---

## 后端 API

### 1. **查询授权状态**

已绑定设备必须携带设备令牌，否则返回 401（防止任意 deviceId 探测绑定状态）：

```http
GET /api/v1/devices/auth-status?deviceId={deviceId}
X-Device-Token: {deviceToken}

响应:
{
  "bound": true,
  "expired": false,
  "authCodeId": "GGSC3U8K",
  "expiresAt": "2026-12-31T23:59:59Z"
}
```

- `expiresAt`：仅已绑定且授权码带期限时返回
- 未绑定设备：`{"bound":false,"expired":false}`；令牌缺失/错误：`401`

### 2. **生成带过期时间的授权码**

```http
POST /admin/devices/authcodes/generate
{
  "count": 10,
  "maxBindings": 3,
  "expiresAt": "2026-12-31T23:59:59Z"
}

响应:
{
  "codes": [
    {
      "id": "XXX",
      "code": "XXXXXX",
      "maxBindings": 3,
      "expiresAt": "2026-12-31T23:59:59Z",
      "status": "active"
    }
  ]
}
```

### 3. **续期授权码**

```http
PATCH /admin/devices/authcodes/{codeId}/renew
{
  "expiresAt": "2027-12-31T23:59:59Z"
}
```

---

## 测试验证

### 1. 创建一个即将过期的授权码

```bash
# 设置 1 小时后过期
curl -sk https://66.187.6.46:8090/admin/devices/authcodes/generate \
  -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"count":1,"maxBindings":1,"expiresAt":"2026-09-17T21:00:00Z"}'
```

### 2. 绑定设备

在 Android 客户端输入授权码绑定设备。

### 3. 等待过期

等待过期时间到达，客户端后台检查会发现过期并显示提示。

### 4. 验证过期状态

```bash
# 查询设备授权状态（已绑定设备需带 X-Device-Token）
curl -sk "https://66.187.6.46:8090/api/v1/devices/auth-status?deviceId=<deviceID>" \
  -H "X-Device-Token: <deviceToken>"
# 应返回 {"bound":true,"expired":true}
```

---

## 后台检查机制

### Go 层（已实现）

- **绑定检查循环**：每 10 秒检查一次
- **过期处理**：检测到过期时自动停止所有网络
- **代码位置**：`internal/client/daemon.go` 的 `verifyBinding()` 方法

### Kotlin 层（本轮已从 stub 落地为真实调用）

- **定期检查**：每小时查询一次（`startAuthCheck()`）
- **过期检测**：`checkAuthStatus()` → `SnetBridge.getAuthStatus()` → Go 绑定 `SnetCore.GetAuthStatus` → 服务端 `/api/v1/devices/auth-status`
- **UI 提示**：显示过期警告卡片（`MainScreen.kt`）
- **代码位置**：`MainViewModel.kt` 的 `startAuthCheck()`、`SnetBridge.kt` 的 `getAuthStatus()`

---

## 注意事项

1. **首次启动检查**：客户端启动时会立即检查一次授权状态
2. **网络错误处理**：网络错误不会触发过期提示，避免误报
3. **后台服务**：即使应用在后台，也会定期检查
4. **续期后恢复**：管理员续期后，客户端下次检查会恢复正常状态

---

## 文件清单

### Go 层
- `snetbind/snetcore.go` - Android 绑定方法
- `internal/client/control.go` - API 客户端
- `internal/client/daemon.go` - 后台检查逻辑

### Kotlin 层
- `android/app-native/src/main/kotlin/com/snet/app/SnetBridge.kt`
- `android/app-native/src/main/kotlin/com/snet/app/model/Models.kt`
- `android/app-native/src/main/kotlin/com/snet/app/repository/SnetRepository.kt`
- `android/app-native/src/main/kotlin/com/snet/app/viewmodel/MainViewModel.kt`
- `android/app-native/src/main/kotlin/com/snet/app/ui/MainScreen.kt`

---

## 下一步

1. 在有 Android SDK 的环境中编译 AAR 包
2. 编译 Android APK
3. 真机测试过期流程
4. 用户文档更新