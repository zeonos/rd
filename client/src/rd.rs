//! RD fork additions to the RustDesk client.
//!
//! Upstream files only get one-line hooks marked `// RD:` that call into this
//! module, so updating to a new RustDesk release stays a small merge.
//!
//! 1. `apply_builtin_config` bakes our server, its public key and our defaults
//!    into the binary (upstream needs a config file signed by RustDesk Ltd).
//! 2. `extend_sysinfo` reports a hash of the unattended-access password to our
//!    API server, which hands it only to logged-in admins (with TOTP) via the
//!    address book, so viewers connect without anyone knowing the password.
//! 3. `start_agent` runs jobs from the console (see `rd/agent.rs`).

use hbb_common::{
    config::{self, Config},
    log,
};
use std::collections::HashMap;

#[cfg(windows)]
mod agent;

/// `client/rd/config.json`, same format as RustDesk's custom client config:
/// `app-name`, `default-settings`, `override-settings`, plus hard settings.
const BUILTIN_CONFIG: &str = include_str!("../rd/config.json");

/// Build-time overrides, set by CI from repository variables/secrets.
const ENV_SERVER: Option<&str> = option_env!("RD_SERVER");
const ENV_SERVER_KEY: Option<&str> = option_env!("RD_SERVER_KEY");

pub fn apply_builtin_config() {
    let mut data: HashMap<String, serde_json::Value> = match serde_json::from_str(BUILTIN_CONFIG) {
        Ok(d) => d,
        Err(e) => {
            log::error!("RD: built-in config is invalid: {}", e);
            return;
        }
    };
    if let Some(serde_json::Value::Object(overrides)) = data.get_mut("override-settings") {
        if let Some(server) = ENV_SERVER.filter(|s| !s.is_empty()) {
            overrides.insert("custom-rendezvous-server".into(), server.into());
            overrides.insert("relay-server".into(), server.into());
            overrides.insert("api-server".into(), format!("https://{}", server).into());
        }
        if let Some(key) = ENV_SERVER_KEY.filter(|s| !s.is_empty()) {
            overrides.insert("key".into(), key.into());
        }
    }
    crate::common::apply_custom_client_map(data);
}

/// Adds `rd_pw_h1` (base64 of sha256(permanent password + salt)) to the
/// sysinfo upload. Only the hash leaves the machine, never the password.
///
/// Also adds `rd_pk`, the device's public Ed25519 key, which the server records
/// once after enrolment to verify signed agent requests (job polling).
pub fn extend_sysinfo(v: &mut serde_json::Value) {
    if let Some(h1) = unattended_h1() {
        v["rd_pw_h1"] = serde_json::json!(crate::encode64(h1));
    }
    let (_, pk) = Config::get_key_pair();
    if pk.len() == 32 {
        v["rd_pk"] = serde_json::json!(crate::encode64(pk));
    }
}

fn unattended_h1() -> Option<Vec<u8>> {
    let (storage, salt) = Config::get_local_permanent_password_storage_and_salt();
    if storage.is_empty() || salt.is_empty() {
        return None;
    }
    config::decode_permanent_password_h1_from_storage(&storage).map(|h1| h1.to_vec())
}

/// Starts the job runner (scripts and power actions from the RD console) in
/// the installed Windows service.
pub fn start_agent() {
    #[cfg(windows)]
    agent::start();
}

#[cfg(test)]
mod tests {
    #[test]
    fn builtin_config_parses() {
        let v: serde_json::Value = serde_json::from_str(super::BUILTIN_CONFIG).unwrap();
        assert!(v["override-settings"]["custom-rendezvous-server"].is_string());
        assert_eq!(v["override-settings"]["allow-websocket"], "Y");
    }
}
