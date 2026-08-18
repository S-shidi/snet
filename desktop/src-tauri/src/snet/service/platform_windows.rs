//! Windows: snetd runs as a native service under LocalSystem via sc.exe.
//!
//! The daemon is a real SCM service (svc.Run): a clean /ctl/shutdown reports
//! SERVICE_STOPPED so the crash-recovery actions below do not restart it,
//! while a crash exit triggers the restart/restart/restart recovery policy.

use std::process::Command;

const DAEMON_PATH: &str = r"C:\Program Files\SNET\snetd.exe";
const DAEMON_SERVICE: &str = "snetd";
const DAEMON_CONFIG: &str = r"C:\ProgramData\SNET\daemon.json";
const DEVICE_ID_FILE: &str = r"C:\ProgramData\SNET\device.id";
const FIREWALL_RULE: &str = "SNET WireGuard";

/// Run a PowerShell script with elevation (UAC prompt). The script is written
/// to a temp .ps1 file and launched via Start-Process -Verb RunAs to avoid
/// fragile nested quoting; the elevated process's exit code is propagated.
fn run_admin(ps_script: &str) -> Result<(), String> {
    let path = std::env::temp_dir().join("snet-install-daemon.ps1");
    std::fs::write(&path, ps_script).map_err(|e| format!("写脚本: {e}"))?;
    let launcher = format!(
        "$p = Start-Process -FilePath powershell -Verb RunAs -Wait -PassThru -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','{}'; exit $p.ExitCode",
        path.display()
    );
    let out = Command::new("powershell")
        .args(["-NoProfile", "-NonInteractive", "-Command", &launcher])
        .output()
        .map_err(|e| format!("powershell: {e}"))?;
    let _ = std::fs::remove_file(&path);
    if out.status.success() {
        Ok(())
    } else {
        Err(String::from_utf8_lossy(&out.stderr).trim().to_string())
    }
}

/// Ensure the snetd service is installed and running. When unhealthy, the
/// service is recreated (stop/delete/create), the crash-recovery policy and
/// the inbound UDP firewall rule are (re)applied, then the service is started.
pub fn ensure_daemon() -> Result<(), String> {
    if super::super::daemon_reachable() {
        return Ok(());
    }
    let script = format!(
        r#"$ErrorActionPreference = 'Stop'
$daemon = '{daemon}'
$config = '{config}'
$deviceId = '{device}'
New-Item -ItemType Directory -Force -Path 'C:\ProgramData\SNET' | Out-Null
Stop-Service -Name {svc} -ErrorAction SilentlyContinue
& sc.exe delete {svc} 2>$null | Out-Null
$binPath = '"' + $daemon + '" -config "' + $config + '" -device-id-file "' + $deviceId + '"'
New-Service -Name {svc} -BinaryPathName $binPath -StartupType Automatic -DisplayName 'SNET snetd' | Out-Null
& sc.exe failure {svc} reset= 0 actions= restart/5000/restart/10000/restart/30000 | Out-Null
Remove-NetFirewallRule -Name '{rule}' -ErrorAction SilentlyContinue
New-NetFirewallRule -Name '{rule}' -DisplayName '{rule}' -Direction Inbound -Action Allow -Program $daemon | Out-Null
Start-Service -Name {svc}
"#,
        daemon = DAEMON_PATH,
        svc = DAEMON_SERVICE,
        config = DAEMON_CONFIG,
        device = DEVICE_ID_FILE,
        rule = FIREWALL_RULE,
    );
    run_admin(&script)?;
    if super::super::wait_healthy(&super::super::daemon_reachable, 30) {
        return Ok(());
    }
    Err("snetd 服务安装后启动超时，请在事件查看器中查看服务日志".to_string())
}
