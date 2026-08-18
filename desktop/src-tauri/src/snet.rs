use serde_json::{json, Value};

const CTL_DEFAULT: &str = "http://127.0.0.1:19432";

mod service;

fn ctl_err(msg: &str) -> String {
    use std::io::Write;
    if let Ok(mut f) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(std::env::temp_dir().join("snet-gui-errors.log"))
    {
        let _ = writeln!(f, "[{}] {}", std::process::id(), msg);
    }
    msg.to_string()
}

pub(crate) fn ctl(method: &str, path: &str, body: Option<Value>) -> Result<Value, String> {
    let url = format!("{CTL_DEFAULT}{path}");
    let req = ureq::request(method, &url);
    let req = match &body {
        Some(b) => req.send_json(b.clone()),
        None => req.send_string(""),
    };
    match req {
        // Many control endpoints answer 204 No Content with an empty body;
        // parse it as an empty JSON object instead of failing.
        Ok(resp) => {
            let text = resp
                .into_string()
                .unwrap_or_default();
            if text.trim().is_empty() {
                return Ok(json!({}));
            }
            serde_json::from_str(&text).map_err(|e| ctl_err(&format!("ctl {method} {path}: {e}")))
        }
        Err(ureq::Error::Status(code, resp)) => {
            let msg = resp
                .into_string()
                .unwrap_or_default();
            Err(ctl_err(&format!("ctl {code}: {msg}")))
        }
        Err(e) => Err(ctl_err(&format!("ctl {method} {path}: {e}"))),
    }
}

// Used by the Linux and Windows service backends; the macOS backend uses the
// stricter daemon_healthy (which also rejects active-but-broken networks).
#[allow(dead_code)]
pub(crate) fn daemon_reachable() -> bool {
    ctl("GET", "/ctl/status", None).is_ok()
}

/// Percent-encode a server address for the invite link's `server` query
/// parameter, mirroring Go's url.QueryEscape for the reserved set.
fn percent_encode(s: &str) -> String {
    const SAFE: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~";
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        if SAFE.contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

pub(crate) fn wait_healthy(healthy: &dyn Fn() -> bool, seconds: u64) -> bool {
    for _ in 0..(seconds * 4) {
        if healthy() {
            return true;
        }
        std::thread::sleep(std::time::Duration::from_millis(250));
    }
    false
}

#[tauri::command]
pub async fn daemon_status() -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(|| ctl("GET", "/ctl/status", None))
        .await
        .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn ensure_daemon() -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(|| service::ensure_daemon())
        .await
        .map_err(|e| e.to_string())?
}

/// Gracefully stop the daemon: it closes all tunnels and exits with code 0,
/// so launchd (KeepAlive SuccessfulExit=false) leaves the job stopped.
#[tauri::command]
pub async fn stop_daemon() -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(|| {
        ctl("POST", "/ctl/shutdown", None)?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn create_network(server: String, port: u16, ca: String, name: String, subnet: String, approval_required: Option<bool>) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let approval = approval_required.unwrap_or(false);
        let resp = ctl(
            "POST",
            "/ctl/create",
            Some(json!({ "server": server, "port": port, "name": name, "subnet": subnet, "approvalRequired": approval, "ca": ca })),
        )?;
        // an invite link carries the network's server so unbound joiners can
        // target it directly; keep the parameter absent for legacy servers
        let mut link = format!("snet://join?nid={}&code={}", resp["networkId"], resp["pairingCode"]);
        if !server.trim().is_empty() {
            link.push_str(&format!("&server={}", percent_encode(&server)));
        }
        Ok(json!({
            "networkId": resp["networkId"],
            "nodeId": resp["nodeId"],
            "ip": resp["ip"],
            "pairingCode": resp["pairingCode"],
            "link": link,
        }))
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn join_network(server: String, port: u16, link: String, ca: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl(
            "POST",
            "/ctl/join",
            Some(json!({ "server": server, "port": port, "link": link, "ca": ca })),
        )
    })
    .await
    .map_err(|e| e.to_string())?
}

/// The list of joined networks, pulled from the daemon status snapshot.
#[tauri::command]
pub async fn networks() -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(|| {
        let st = ctl("GET", "/ctl/status", None)?;
        Ok(st["networks"].clone())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn leave_nid(nid: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/leave", Some(json!({ "nid": nid })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn rejoin_nid(nid: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/rejoin", Some(json!({ "nid": nid })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn remove_nid(nid: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/remove", Some(json!({ "nid": nid })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn rename_nid(nid: String, name: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/rename", Some(json!({ "nid": nid, "name": name })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn update_settings(nid: String, name: String, subnet: String, approval_required: Option<bool>) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let mut body = json!({ "nid": nid, "name": name, "subnet": subnet });
        if let Some(v) = approval_required {
            body["approvalRequired"] = json!(v);
        }
        ctl("POST", "/ctl/settings", Some(body))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn approve_pending(nid: String, pending_id: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/approve", Some(json!({ "nid": nid, "pendingId": pending_id })))
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn deny_pending(nid: String, pending_id: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/deny", Some(json!({ "nid": nid, "pendingId": pending_id })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn cancel_pending(pending_id: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/cancel-pending", Some(json!({ "pendingId": pending_id })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

/// Pending join requests, pulled from the daemon status snapshot.
#[tauri::command]
pub async fn pending_joins() -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(|| {
        let st = ctl("GET", "/ctl/status", None)?;
        Ok(st["pendingJoins"].clone())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn delete_nid(nid: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/delete", Some(json!({ "nid": nid })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn kick_nid(nid: String, node_id: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/kick", Some(json!({ "nid": nid, "nodeId": node_id })))?;
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn reset_code(nid: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl("POST", "/ctl/reset-code", Some(json!({ "nid": nid })))
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn netinfo(nid: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || ctl("GET", &format!("/ctl/netinfo?nid={nid}"), None))
        .await
        .map_err(|e| e.to_string())?
}

/// Live peer list for a network; works for any member (read-only), unlike
/// netinfo which requires the network owner.
#[tauri::command]
pub async fn peers(nid: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || ctl("GET", &format!("/ctl/peers?nid={nid}"), None))
        .await
        .map_err(|e| e.to_string())?
}

/// The machine's stable device identity.
#[tauri::command]
pub async fn device_id() -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(|| ctl("POST", "/ctl/device-id", Some(json!({}))))
        .await
        .map_err(|e| e.to_string())?
}

/// Link this device to a custom coordination server using an admin-generated
/// device authorization code. The code is sent once to the daemon and never
/// stored on disk.
#[tauri::command]
pub async fn bind_server(server: String, ca: String, code: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        ctl(
            "POST",
            "/ctl/bind",
            Some(json!({ "server": server, "ca": ca, "code": code })),
        )
    })
    .await
    .map_err(|e| e.to_string())?
}
