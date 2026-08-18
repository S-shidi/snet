//! Other platforms: no automatic daemon supervisor yet. The daemon must be
//! started manually (e.g. `snetd -config ... &`); if it is already reachable
//! the GUI works as normal.

pub fn ensure_daemon() -> Result<(), String> {
    if super::daemon_reachable() {
        return Ok(());
    }
    Err("当前平台不支持自动安装后台服务，请手动启动 snetd".to_string())
}
