//! Inventory reports and self-update (Windows service only).
//!
//! - Inventory: `inventory.ps1` a few minutes after start and every 6 hours,
//!   posted to `/api/agent/inventory` signed with the device key.
//! - Update: an "update" job (signed by the server's job key, see agent.rs)
//!   names a release zip and its SHA-256. The zip is downloaded, checked,
//!   unpacked, and `RD.exe --update` from it is started through Task
//!   Scheduler, because that command stops this service.
//!
//! The protocol matches `server/internal/api/fleet.go`.

use super::agent::{api, device_sign, now, run_with_timeout, CREATE_NO_WINDOW};
use hbb_common::{config::Config, log};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{
    os::windows::process::CommandExt,
    path::{Path, PathBuf},
    process::Command,
    thread,
    time::Duration,
};

const INVENTORY_SCRIPT: &str = include_str!("inventory.ps1");
const INVENTORY_FIRST: Duration = Duration::from_secs(180);
const INVENTORY_EVERY: Duration = Duration::from_secs(6 * 3600);
const TASK_NAME: &str = "RD-Update";
const SYNC_SCRIPT: &str = include_str!("syncthing.ps1");
const SYNC_FIRST: Duration = Duration::from_secs(90);
const SYNC_EVERY: Duration = Duration::from_secs(300);

pub fn start_inventory() {
    thread::spawn(|| {
        thread::sleep(INVENTORY_FIRST);
        loop {
            if let Err(e) = send_inventory() {
                log::warn!("RD agent: inventory not sent: {}", e);
            }
            thread::sleep(INVENTORY_EVERY);
        }
    });
}

/// Syncthing status every 5 minutes; see syncthing.ps1 and server/internal/api/sync.go.
pub fn start_sync() {
    thread::spawn(|| {
        thread::sleep(SYNC_FIRST);
        loop {
            if let Err(e) = send_sync() {
                log::warn!("RD agent: Syncthing status not sent: {}", e);
            }
            thread::sleep(SYNC_EVERY);
        }
    });
}

fn send_sync() -> Result<(), String> {
    let url = api();
    if url.is_empty() {
        return Ok(());
    }
    let (ok, code, out) = powershell(SYNC_SCRIPT, Duration::from_secs(120));
    if !ok {
        return Err(format!("syncthing script exited with {}", code));
    }
    let report = out.trim().to_owned();
    serde_json::from_str::<serde_json::Value>(&report)
        .map_err(|e| format!("Syncthing report is not JSON: {}", e))?;
    let id = Config::get_id();
    let ts = now().to_string();
    let msg = format!(
        "rd-sync\n{}\n{}\n{}",
        id,
        hex(&Sha256::digest(report.as_bytes())),
        ts
    );
    let sig = device_sign(&msg).ok_or("no device key")?;
    let body = serde_json::json!({ "id": id, "ts": ts, "report": report, "sig": sig }).to_string();
    crate::post_request_sync(format!("{}/api/agent/sync", url), body, "")
        .map(|_| ())
        .map_err(|e| e.to_string())
}

fn hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{:02x}", x)).collect()
}

fn powershell(script: &str, timeout: Duration) -> (bool, i32, String) {
    let mut cmd = Command::new("powershell.exe");
    cmd.args([
        "-NoLogo",
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy",
        "Bypass",
        "-Command",
        script,
    ]);
    run_with_timeout(&mut cmd, timeout)
}

fn send_inventory() -> Result<(), String> {
    let url = api();
    if url.is_empty() {
        return Ok(());
    }
    let (ok, code, out) = powershell(INVENTORY_SCRIPT, Duration::from_secs(300));
    if !ok {
        return Err(format!("inventory script exited with {}", code));
    }
    let inventory = out.trim().to_owned();
    serde_json::from_str::<serde_json::Value>(&inventory)
        .map_err(|e| format!("inventory is not JSON: {}", e))?;
    let id = Config::get_id();
    let ts = now().to_string();
    let msg = format!(
        "rd-inventory\n{}\n{}\n{}",
        id,
        hex(&Sha256::digest(inventory.as_bytes())),
        ts
    );
    let sig = device_sign(&msg).ok_or("no device key")?;
    let body = serde_json::json!({ "id": id, "ts": ts, "inventory": inventory, "sig": sig })
        .to_string();
    crate::post_request_sync(format!("{}/api/agent/inventory", url), body, "")
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[derive(Deserialize)]
struct UpdateBody {
    build: i64,
    path: String,
    sha256: String,
}

fn update_dir() -> PathBuf {
    let base = std::env::var("ProgramData").unwrap_or_else(|_| "C:\\ProgramData".to_owned());
    PathBuf::from(base).join("RD").join("update")
}

fn ps_quote(p: &Path) -> String {
    format!("'{}'", p.display().to_string().replace('\'', "''"))
}

fn file_sha256(path: &Path) -> Result<String, String> {
    let mut f = std::fs::File::open(path).map_err(|e| e.to_string())?;
    let mut h = Sha256::new();
    std::io::copy(&mut f, &mut h).map_err(|e| e.to_string())?;
    Ok(hex(&h.finalize()))
}

/// Downloads, checks and unpacks the release; returns RD.exe of the new build.
pub fn prepare_update(body: &str) -> Result<(i64, PathBuf), String> {
    let u: UpdateBody = serde_json::from_str(body).map_err(|e| format!("bad update job: {}", e))?;
    if !u.path.starts_with("/api/releases/") || u.sha256.len() != 64 {
        return Err("bad update job".into());
    }
    let dir = update_dir();
    std::fs::remove_dir_all(&dir).ok();
    std::fs::create_dir_all(&dir).map_err(|e| format!("cannot create {}: {}", dir.display(), e))?;
    let zip = dir.join(format!("rd-{}.zip", u.build));
    let unpacked = dir.join(format!("rd-{}", u.build));
    let url = format!("{}{}", api(), u.path).replace('\'', "''");
    let (ok, _, out) = powershell(
        &format!(
            "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; \
             [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; \
             Invoke-WebRequest -UseBasicParsing -Uri '{}' -OutFile {}",
            url,
            ps_quote(&zip)
        ),
        Duration::from_secs(600),
    );
    if !ok {
        return Err(format!("download failed: {}", out.trim()));
    }
    let got = file_sha256(&zip)?;
    if !got.eq_ignore_ascii_case(&u.sha256) {
        std::fs::remove_file(&zip).ok();
        return Err(format!(
            "download does not match the signed SHA-256 ({} instead of {})",
            got, u.sha256
        ));
    }
    let (ok, _, out) = powershell(
        &format!(
            "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; \
             Expand-Archive -LiteralPath {} -DestinationPath {} -Force",
            ps_quote(&zip),
            ps_quote(&unpacked)
        ),
        Duration::from_secs(300),
    );
    if !ok {
        return Err(format!("unpacking failed: {}", out.trim()));
    }
    let exe = find_exe(&unpacked).ok_or("the release has no RD.exe")?;
    Ok((u.build, exe))
}

fn find_exe(dir: &Path) -> Option<PathBuf> {
    for entry in std::fs::read_dir(dir).ok()?.flatten() {
        let p = entry.path();
        if p.is_dir() {
            if let Some(f) = find_exe(&p) {
                return Some(f);
            }
        } else if p
            .file_name()
            .map(|n| n.eq_ignore_ascii_case("RD.exe"))
            .unwrap_or(false)
        {
            return Some(p);
        }
    }
    None
}

/// Runs `RD.exe --update` from the new build outside this service's process
/// tree: it stops the service, copies itself over the install and restarts it.
pub fn start_update(exe: &Path) {
    let tr = format!("\"{}\" --update", exe.display());
    let create = Command::new("schtasks.exe")
        .args([
            "/Create", "/F", "/TN", TASK_NAME, "/RU", "SYSTEM", "/RL", "HIGHEST", "/SC", "ONCE",
            "/ST", "00:00", "/TR",
        ])
        .arg(&tr)
        .creation_flags(CREATE_NO_WINDOW)
        .status();
    if !matches!(create, Ok(s) if s.success()) {
        log::error!("RD agent: cannot create the update task: {:?}", create);
        return;
    }
    if let Err(e) = Command::new("schtasks.exe")
        .args(["/Run", "/TN", TASK_NAME])
        .creation_flags(CREATE_NO_WINDOW)
        .status()
    {
        log::error!("RD agent: cannot start the update task: {}", e);
    }
}

/// After an update the service runs again: remove the one-shot task and files.
pub fn cleanup_update() {
    Command::new("schtasks.exe")
        .args(["/Delete", "/F", "/TN", TASK_NAME])
        .creation_flags(CREATE_NO_WINDOW)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .ok();
    std::fs::remove_dir_all(update_dir()).ok();
}

/// The RDP session picker in sessions needs RustDesk's "share RDP" setting.
pub fn enable_rdp_session_picker() {
    if !crate::platform::is_share_rdp() {
        crate::platform::set_share_rdp(true);
        log::info!("RD agent: enabled RDP session sharing (session picker)");
    }
}
