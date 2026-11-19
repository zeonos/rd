//! RD job runner (Windows service only).
//!
//! Polls the RD API server for jobs, runs scripts and power actions, and
//! reports the result. Both directions are signed:
//! - requests are signed with this device's RustDesk Ed25519 key, which the
//!   server recorded from the first sysinfo after enrolment;
//! - each job is signed by the server's job key, built into the client
//!   (`client/rd/job-key.pub`). Unsigned or foreign jobs are never run.
//!
//! The protocol matches `server/internal/api/jobs.go`.

use hbb_common::{config::Config, log, sodiumoxide::crypto::sign};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{
    io::Read,
    os::windows::process::CommandExt,
    path::PathBuf,
    process::{Command, Stdio},
    sync::Once,
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

const JOB_KEY: &str = include_str!("../../rd/job-key.pub");
const ENV_JOB_KEY: Option<&str> = option_env!("RD_JOB_KEY");
const MAX_OUTPUT: usize = 256 * 1024;
pub(super) const CREATE_NO_WINDOW: u32 = 0x0800_0000;

static START: Once = Once::new();

pub fn start() {
    START.call_once(|| {
        if !crate::platform::is_installed() {
            return;
        }
        let Some(key) = job_key() else {
            log::error!("RD agent: no valid job key built in, job runner disabled");
            return;
        };
        thread::spawn(move || run(key));
        super::fleet::cleanup_update();
        super::fleet::enable_rdp_session_picker();
        super::fleet::start_inventory();
    });
}

fn job_key() -> Option<sign::PublicKey> {
    let b64 = ENV_JOB_KEY
        .filter(|s| !s.is_empty())
        .unwrap_or(JOB_KEY)
        .trim();
    let bytes = crate::decode64(b64).ok()?;
    sign::PublicKey::from_slice(&bytes)
}

pub(super) fn now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or_default()
}

pub(super) fn device_sign(msg: &str) -> Option<String> {
    let (sk, _) = Config::get_key_pair();
    let sk = sign::SecretKey::from_slice(&sk)?;
    Some(crate::encode64(
        sign::sign_detached(msg.as_bytes(), &sk).to_bytes(),
    ))
}

#[derive(Deserialize)]
struct PollResponse {
    #[serde(default)]
    jobs: Vec<SignedJob>,
    #[serde(default)]
    poll_after: u64,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
struct SignedJob {
    payload: String,
    sig: String,
}

#[derive(Deserialize)]
struct Job {
    job_id: i64,
    device_id: String,
    kind: String,
    #[serde(default)]
    shell: String,
    #[serde(default)]
    body: String,
    #[serde(default)]
    run_as: String,
    timeout_sec: u64,
    #[serde(default)]
    action: String,
    expires_at: i64,
}

fn run(key: sign::PublicKey) {
    // Give the service time to register and upload sysinfo (which carries our key).
    thread::sleep(Duration::from_secs(20));
    loop {
        let wait = match poll_once(&key) {
            Ok(after) => after.clamp(5, 300),
            Err(e) => {
                hbb_common::throttled_log!(
                    Duration::from_secs(600),
                    warn,
                    "RD agent poll failed: {}",
                    e
                );
                60
            }
        };
        thread::sleep(Duration::from_secs(wait));
    }
}

pub(super) fn api() -> String {
    crate::ui_interface::get_api_server()
}

fn poll_once(key: &sign::PublicKey) -> Result<u64, String> {
    let url = api();
    if url.is_empty() {
        return Ok(60);
    }
    let id = Config::get_id();
    let ts = now().to_string();
    let sig = device_sign(&format!("rd-poll\n{}\n{}", id, ts)).ok_or("no device key")?;
    let body = serde_json::json!({ "id": id, "ts": ts, "sig": sig }).to_string();
    let text = crate::post_request_sync(format!("{}/api/agent/poll", url), body, "")
        .map_err(|e| e.to_string())?;
    let resp: PollResponse =
        serde_json::from_str(&text).map_err(|e| format!("bad response: {}", e))?;
    if !resp.error.is_empty() {
        return Err(resp.error);
    }
    for sj in resp.jobs {
        match verify(key, &sj, &id) {
            Ok(job) => execute(job, &id),
            Err(e) => log::error!("RD agent: rejected job: {}", e),
        }
    }
    Ok(if resp.poll_after == 0 {
        10
    } else {
        resp.poll_after
    })
}

fn verify(key: &sign::PublicKey, sj: &SignedJob, id: &str) -> Result<Job, String> {
    let sig = crate::decode64(&sj.sig).map_err(|_| "signature is not base64")?;
    let sig: [u8; 64] = sig
        .try_into()
        .map_err(|_| "signature has the wrong length")?;
    let sig = sign::Signature::from_bytes(&sig).map_err(|_| "invalid signature")?;
    if !sign::verify_detached(&sig, sj.payload.as_bytes(), key) {
        return Err("signature does not match the built-in job key".into());
    }
    let job: Job = serde_json::from_str(&sj.payload).map_err(|e| format!("bad payload: {}", e))?;
    if job.device_id != id {
        return Err(format!(
            "job {} is for device {}, not this one",
            job.job_id, job.device_id
        ));
    }
    if now() > job.expires_at + 300 {
        return Err(format!("job {} has expired", job.job_id));
    }
    Ok(job)
}

fn execute(job: Job, id: &str) {
    log::info!("RD agent: running job {} ({})", job.job_id, job.kind);
    let (ok, code, output) = match job.kind.as_str() {
        "script" if job.run_as == "user" => (
            false,
            -1,
            "Running as the logged-on user is not supported by this client version yet. Use SYSTEM.".to_owned(),
        ),
        "script" => run_script(&job),
        "power" => {
            // Report first: the machine may be gone a few seconds later.
            let (ok, code, out) = prepare_power(&job.action);
            report(id, job.job_id, ok, code, &out);
            if ok {
                start_power(&job.action);
            }
            return;
        }
        "update" => {
            match super::fleet::prepare_update(&job.body) {
                Ok((build, exe)) => {
                    // Report first: the update stops this service.
                    report(
                        id,
                        job.job_id,
                        true,
                        0,
                        &format!("Updating to build {}. The RD service restarts in a moment.", build),
                    );
                    super::fleet::start_update(&exe);
                }
                Err(e) => report(id, job.job_id, false, -1, &e),
            }
            return;
        }
        other => (false, -1, format!("Unknown job kind {}", other)),
    };
    report(id, job.job_id, ok, code, &output);
}

fn jobs_dir() -> PathBuf {
    let base = std::env::var("ProgramData").unwrap_or_else(|_| "C:\\ProgramData".to_owned());
    PathBuf::from(base).join("RD").join("jobs")
}

fn run_script(job: &Job) -> (bool, i32, String) {
    let dir = jobs_dir();
    if let Err(e) = std::fs::create_dir_all(&dir) {
        return (false, -1, format!("Cannot create {}: {}", dir.display(), e));
    }
    let (file, mut cmd) = if job.shell == "cmd" {
        let f = dir.join(format!("job-{}.cmd", job.job_id));
        let mut c = Command::new("cmd.exe");
        c.arg("/d").arg("/c").arg(&f);
        (f, c)
    } else {
        let f = dir.join(format!("job-{}.ps1", job.job_id));
        let mut c = Command::new("powershell.exe");
        c.args([
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
        ])
        .arg(&f);
        (f, c)
    };
    let script = if job.shell == "cmd" {
        format!("@chcp 65001 >nul\r\n{}", job.body)
    } else {
        // UTF-8 BOM so PowerShell 5.1 reads non-ASCII correctly; UTF-8 output.
        format!(
            "\u{feff}[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\r\n$ProgressPreference = 'SilentlyContinue'\r\n{}",
            job.body
        )
    };
    if let Err(e) = std::fs::write(&file, script) {
        return (false, -1, format!("Cannot write {}: {}", file.display(), e));
    }
    let result = run_with_timeout(&mut cmd, Duration::from_secs(job.timeout_sec.max(1)));
    std::fs::remove_file(&file).ok();
    result
}

pub(super) fn run_with_timeout(cmd: &mut Command, timeout: Duration) -> (bool, i32, String) {
    cmd.stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .creation_flags(CREATE_NO_WINDOW);
    let mut child = match cmd.spawn() {
        Ok(c) => c,
        Err(e) => return (false, -1, format!("Failed to start: {}", e)),
    };
    let reader = |r: Option<Box<dyn Read + Send>>| {
        thread::spawn(move || {
            let mut buf = Vec::new();
            if let Some(mut r) = r {
                r.take((MAX_OUTPUT + 1) as u64).read_to_end(&mut buf).ok();
            }
            buf
        })
    };
    let out = reader(
        child
            .stdout
            .take()
            .map(|s| Box::new(s) as Box<dyn Read + Send>),
    );
    let err = reader(
        child
            .stderr
            .take()
            .map(|s| Box::new(s) as Box<dyn Read + Send>),
    );
    let start = Instant::now();
    let status = loop {
        match child.try_wait() {
            Ok(Some(s)) => break Some(s),
            Ok(None) if start.elapsed() >= timeout => {
                child.kill().ok();
                child.wait().ok();
                break None;
            }
            Ok(None) => thread::sleep(Duration::from_millis(250)),
            Err(_) => break None,
        }
    };
    let mut text = String::from_utf8_lossy(&out.join().unwrap_or_default()).into_owned();
    let stderr = String::from_utf8_lossy(&err.join().unwrap_or_default()).into_owned();
    if !stderr.trim().is_empty() {
        if !text.is_empty() && !text.ends_with('\n') {
            text.push('\n');
        }
        text.push_str(&stderr);
    }
    match status {
        Some(s) => {
            let code = s.code().unwrap_or(-1);
            (code == 0, code, text)
        }
        None => {
            text.push_str(&format!(
                "\n[stopped after the {} s timeout]",
                timeout.as_secs()
            ));
            (false, -1, text)
        }
    }
}

fn report(id: &str, job_id: i64, ok: bool, code: i32, output: &str) {
    let mut output = output.to_owned();
    if output.len() > MAX_OUTPUT {
        let mut cut = MAX_OUTPUT;
        while !output.is_char_boundary(cut) {
            cut -= 1;
        }
        output.truncate(cut);
        output.push_str("\n[output truncated]");
    }
    let status = if ok { "success" } else { "failed" };
    let ts = now().to_string();
    let hash: String = Sha256::digest(output.as_bytes())
        .iter()
        .map(|b| format!("{:02x}", b))
        .collect();
    let msg = format!(
        "rd-result\n{}\n{}\n{}\n{}\n{}\n{}",
        id, job_id, status, code, hash, ts
    );
    let Some(sig) = device_sign(&msg) else {
        log::error!("RD agent: cannot sign result for job {}", job_id);
        return;
    };
    let body = serde_json::json!({
        "id": id, "ts": ts, "job_id": job_id, "status": status,
        "exit_code": code, "output": output, "sig": sig,
    })
    .to_string();
    for attempt in 0..3 {
        match crate::post_request_sync(format!("{}/api/agent/result", api()), body.clone(), "") {
            Ok(_) => return,
            Err(e) => {
                log::warn!(
                    "RD agent: result for job {} not sent (attempt {}): {}",
                    job_id,
                    attempt + 1,
                    e
                );
                thread::sleep(Duration::from_secs(10));
            }
        }
    }
}

/// Validates the action and does the reversible preparation; nothing restarts yet.
fn prepare_power(action: &str) -> (bool, i32, String) {
    match action {
        "reboot" => (true, 0, "Restarting in 10 seconds.".into()),
        "shutdown" => (true, 0, "Shutting down in 10 seconds.".into()),
        "logoff" => (true, 0, "Logging off the console user.".into()),
        "reboot-safe-mode" => {
            let svc = hbb_common::config::APP_NAME
                .read()
                .map(|n| n.clone())
                .unwrap_or_default();
            // Boot into Safe Mode with networking once: the RunOnce entry (the * prefix runs it in
            // Safe Mode too) clears the flag again, and our service is allowed to start in Safe Mode.
            let script = format!(
                "$ErrorActionPreference = 'Stop'\r\n\
                 bcdedit /set '{{current}}' safeboot network | Out-Null\r\n\
                 New-Item -Force 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\SafeBoot\\Network\\{svc}' -Value 'Service' | Out-Null\r\n\
                 Set-ItemProperty 'HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\RunOnce' -Name '*RDClearSafeBoot' -Value 'bcdedit /deletevalue {{current}} safeboot'\r\n\
                 'Safe Mode with networking set for the next boot only. Restarting in 10 seconds.'"
            );
            let mut cmd = Command::new("powershell.exe");
            cmd.args([
                "-NoLogo",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                &script,
            ]);
            run_with_timeout(&mut cmd, Duration::from_secs(60))
        }
        other => (false, -1, format!("Unknown power action {}", other)),
    }
}

fn start_power(action: &str) {
    let mut cmd = match action {
        "reboot" | "reboot-safe-mode" => {
            let mut c = Command::new("shutdown.exe");
            c.args([
                "/r",
                "/f",
                "/t",
                "10",
                "/d",
                "p:0:0",
                "/c",
                "Restart requested from RD",
            ]);
            c
        }
        "shutdown" => {
            let mut c = Command::new("shutdown.exe");
            c.args([
                "/s",
                "/f",
                "/t",
                "10",
                "/d",
                "p:0:0",
                "/c",
                "Shutdown requested from RD",
            ]);
            c
        }
        "logoff" => {
            let mut c = Command::new("powershell.exe");
            c.args([
                "-NoLogo",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "$s = (Get-CimInstance Win32_Process -Filter \"Name='explorer.exe'\" | Select-Object -First 1).SessionId; if ($s) { logoff $s }",
            ]);
            c
        }
        _ => return,
    };
    if let Err(e) = cmd.creation_flags(CREATE_NO_WINDOW).spawn() {
        log::error!("RD agent: {} failed to start: {}", action, e);
    }
}
