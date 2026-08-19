mod snet;

use std::sync::atomic::{AtomicU64, Ordering};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, WindowEvent,
};
use snet::*;

const TRAY_OPEN: &str = "open";
const TRAY_DAEMON: &str = "daemon_state";
const TRAY_NET: &str = "net_state";
const TRAY_QUIT: &str = "quit";

const MENU_HIDE: &str = "menu_hide";
const MENU_CLOSE: &str = "menu_close";
const MENU_MIN: &str = "menu_min";
const MENU_QUIT: &str = "menu_quit";

static LAST_CLICK_MS: AtomicU64 = AtomicU64::new(0);

#[cfg(target_os = "macos")]
fn set_dock_visible(visible: bool) {
    use objc2::MainThreadMarker;
    use objc2_app_kit::{NSApplication, NSApplicationActivationPolicy};
    if let Some(mtm) = MainThreadMarker::new() {
        let app = NSApplication::sharedApplication(mtm);
        let policy = if visible {
            NSApplicationActivationPolicy::Regular
        } else {
            NSApplicationActivationPolicy::Accessory
        };
        let _ = app.setActivationPolicy(policy);
    }
}

#[cfg(not(target_os = "macos"))]
fn set_dock_visible(_visible: bool) {}

#[cfg(target_os = "macos")]
#[allow(deprecated)]
fn activate_app() {
    use objc2::MainThreadMarker;
    use objc2_app_kit::NSApplication;
    if let Some(mtm) = MainThreadMarker::new() {
        NSApplication::sharedApplication(mtm).activateIgnoringOtherApps(true);
    }
}

#[cfg(not(target_os = "macos"))]
fn activate_app() {}

// Menu accelerators use the Command key on macOS and Ctrl elsewhere.
#[cfg(target_os = "macos")]
fn accel(macos: &'static str, _other: &'static str) -> &'static str {
    macos
}
#[cfg(not(target_os = "macos"))]
fn accel(_macos: &'static str, other: &'static str) -> &'static str {
    other
}

fn hide_main(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.hide();
    }
    set_dock_visible(false);
}

fn show_main(app: &tauri::AppHandle) {
    set_dock_visible(true);
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
    activate_app();
}

fn tray_status() -> (String, String, String) {
    match ctl("GET", "/ctl/status", None) {
        Ok(st) => {
            let count = st["networks"]
                .as_array()
                .map(|a| {
                    a.iter()
                        .filter(|n| n["active"].as_bool().unwrap_or(false))
                        .count()
                })
                .unwrap_or(0);
            (
                "后台服务: 运行中".into(),
                format!("网络在线: {count} 个"),
                format!("Snet · 后台服务运行中 · {count} 个网络在线"),
            )
        }
        Err(_) => (
            "后台服务: 未运行".into(),
            "网络在线: -".into(),
            "Snet · 后台服务未运行".into(),
        ),
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            daemon_status,
            ensure_daemon,
            stop_daemon,
            create_network,
            join_network,
            networks,
            leave_nid,
            rejoin_nid,
            remove_nid,
            rename_nid,
            update_settings,
            update_subnets,
            approve_pending,
            deny_pending,
            cancel_pending,
            pending_joins,
            delete_nid,
            kick_nid,
            reset_code,
            netinfo,
            peers,
            device_id,
            bind_server
        ])
        .setup(|app| {
            let open = MenuItem::with_id(app, TRAY_OPEN, "打开主界面", true, None::<&str>)?;
            let daemon_state = MenuItem::with_id(app, TRAY_DAEMON, "后台服务: …", false, None::<&str>)?;
            let net_state = MenuItem::with_id(app, TRAY_NET, "网络在线: …", false, None::<&str>)?;
            let quit = MenuItem::with_id(app, TRAY_QUIT, "退出", true, None::<&str>)?;
            let tray_menu = Menu::with_items(
                app,
                &[
                    &open,
                    &PredefinedMenuItem::separator(app)?,
                    &daemon_state,
                    &net_state,
                    &PredefinedMenuItem::separator(app)?,
                    &quit,
                ],
            )?;

            let tray_icon = tauri::image::Image::from_bytes(
                include_bytes!("../icons/tray_template_32.png"),
            )
            .expect("failed to load tray template icon");

            let tray = TrayIconBuilder::new()
                .icon(tray_icon)
                .icon_as_template(true)
                .tooltip("SNET 虚拟组网")
                .menu(&tray_menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id().as_ref() {
                    TRAY_OPEN => show_main(app),
                    TRAY_QUIT => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let now = SystemTime::now()
                            .duration_since(UNIX_EPOCH)
                            .unwrap()
                            .as_millis() as u64;
                        let prev = LAST_CLICK_MS.swap(now, Ordering::Relaxed);
                        if now.saturating_sub(prev) < 300 {
                            show_main(tray.app_handle());
                        } else {
                            let _ = tray.with_inner_tray_icon(|inner| inner.show_menu());
                        }
                    }
                })
                .build(app)?;

            // 每 5 秒刷新托盘菜单状态行与提示
            let daemon_item = daemon_state.clone();
            let net_item = net_state.clone();
            thread::spawn(move || loop {
                let (d, n, tip) = tray_status();
                let _ = daemon_item.set_text(&d);
                let _ = net_item.set_text(&n);
                let _ = tray.set_tooltip(Some(&tip));
                thread::sleep(Duration::from_secs(5));
            });

            // 菜单栏：隐藏/退出/关闭窗口/最小化，附带编辑菜单保证剪贴板快捷键可用
            let app_menu = Submenu::with_items(
                app,
                "Snet",
                true,
                &[
                    &MenuItem::with_id(app, MENU_HIDE, "隐藏 Snet", true, Some(accel("Cmd+H", "Ctrl+H")))?,
                    &PredefinedMenuItem::separator(app)?,
                    &MenuItem::with_id(app, MENU_QUIT, "退出 Snet", true, Some(accel("Cmd+Q", "Ctrl+Q")))?,
                ],
            )?;
            let edit_menu = Submenu::with_items(
                app,
                "编辑",
                true,
                &[
                    &PredefinedMenuItem::undo(app, None)?,
                    &PredefinedMenuItem::redo(app, None)?,
                    &PredefinedMenuItem::separator(app)?,
                    &PredefinedMenuItem::cut(app, None)?,
                    &PredefinedMenuItem::copy(app, None)?,
                    &PredefinedMenuItem::paste(app, None)?,
                    &PredefinedMenuItem::select_all(app, None)?,
                ],
            )?;
            let window_menu = Submenu::with_items(
                app,
                "窗口",
                true,
                &[
                    &MenuItem::with_id(app, MENU_MIN, "最小化", true, Some(accel("Cmd+M", "Ctrl+M")))?,
                    &MenuItem::with_id(app, MENU_CLOSE, "关闭窗口", true, Some(accel("Cmd+W", "Ctrl+W")))?,
                ],
            )?;
            let menu = Menu::with_items(app, &[&app_menu, &edit_menu, &window_menu])?;
            app.set_menu(menu)?;

            Ok(())
        })
        .on_menu_event(|app, event| match event.id().as_ref() {
            MENU_HIDE | MENU_CLOSE => hide_main(app),
            MENU_MIN => {
                if let Some(w) = app.get_webview_window("main") {
                    let _ = w.minimize();
                }
            }
            MENU_QUIT => app.exit(0),
            _ => {}
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                hide_main(window.app_handle());
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
