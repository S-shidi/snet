//! Platform-specific daemon service management.
//!
//! Each platform installs/repairs the vnetd background daemon so it survives
//! reboots and restarts after crashes:
//! - macOS: a root LaunchDaemon via launchctl (osascript elevation prompt).
//! - Windows: a native service under LocalSystem via sc.exe (UAC elevation).
//! - other: no service supervisor yet; the daemon must be started manually.

#[cfg(target_os = "macos")]
mod platform_macos;
#[cfg(target_os = "windows")]
mod platform_windows;
#[cfg(not(any(target_os = "macos", target_os = "windows")))]
mod platform_other;

#[cfg(target_os = "macos")]
pub use platform_macos::ensure_daemon;
#[cfg(target_os = "windows")]
pub use platform_windows::ensure_daemon;
#[cfg(not(any(target_os = "macos", target_os = "windows")))]
pub use platform_other::ensure_daemon;
