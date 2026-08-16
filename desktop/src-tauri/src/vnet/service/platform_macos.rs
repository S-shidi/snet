//! macOS: vnetd runs as a root LaunchDaemon (com.vnet.daemon).

use std::process::Command;

const DAEMON_PATH: &str = "/usr/local/vnet/bin/vnetd";
const DAEMON_LABEL: &str = "com.vnet.daemon";
const DAEMON_PLIST: &str = "/Library/LaunchDaemons/com.vnet.daemon.plist";
const DAEMON_CONFIG: &str = "/usr/local/vnet/daemon.json";

/// Run a root command via the macOS authorization prompt (osascript).
fn run_admin(shell_script: &str) -> Result<(), String> {
    let out = Command::new("/usr/bin/osascript")
        .args(["-e", &format!("do shell script {} with administrator privileges", quote(shell_script))])
        .output()
        .map_err(|e| format!("osascript: {e}"))?;
    if out.status.success() {
        Ok(())
    } else {
        Err(String::from_utf8_lossy(&out.stderr).trim().to_string())
    }
}

fn quote(s: &str) -> String {
    format!("\"{}\"", s.replace('\\', "\\\\").replace('"', "\\\""))
}

fn plist(label: &str, prog: &[&str], log: &str) -> String {
    let mut args = String::new();
    for a in prog {
        args.push_str(&format!("        <string>{a}</string>\n"));
    }
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>{label}</string>
    <key>ProgramArguments</key>
    <array>
{args}    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key><false/>
        <key>UnsuccessfulExit</key><true/>
    </dict>
    <key>StandardOutPath</key><string>{log}</string>
    <key>StandardErrorPath</key><string>{log}</string>
    <key>UserName</key><string>root</string>
</dict>
</plist>
"#
    )
}

/// Ensure the LaunchDaemon is installed and running. When unhealthy, the plist
/// is (re)written and the service (re)bootstrapped so upgrades take effect.
fn ensure_service(
    label: &str,
    plist_path: &str,
    xml: &str,
    healthy: &dyn Fn() -> bool,
) -> Result<(), String> {
    if healthy() {
        return Ok(());
    }
    let tmp = std::env::temp_dir().join(format!("{label}.plist"));
    let tmp = tmp.to_string_lossy().to_string();
    std::fs::write(&tmp, xml).map_err(|e| format!("写 plist: {e}"))?;
    let script = format!(
        "install -m 644 {tmp} {dst} && launchctl bootout system/{label} 2>/dev/null; launchctl bootstrap system {dst} && launchctl enable system/{label}",
        tmp = quote(&tmp),
        dst = quote(plist_path),
    );
    run_admin(&script)?;
    if super::super::wait_healthy(healthy, 30) {
        return Ok(());
    }
    Err(format!("{label} 安装后启动超时，查看 /var/log 对应日志"))
}

pub fn ensure_daemon() -> Result<(), String> {
    ensure_service(
        DAEMON_LABEL,
        DAEMON_PLIST,
        &plist(
            DAEMON_LABEL,
            &[DAEMON_PATH, "-config", DAEMON_CONFIG],
            "/var/log/vnetd.log",
        ),
        &super::super::daemon_reachable,
    )
}
