//! RD: devices registered over WebSocket.
//!
//! Upstream hbbs registers devices only over UDP 21116 and answers
//! RegisterPk over TCP/WebSocket with NOT_SUPPORT. For sites that only allow
//! outbound 443, RD clients keep one WebSocket to `/ws/id` (behind Caddy) and
//! register over it. This module maps each such connection's address to a
//! sender, so every message hbbs would send to that address over UDP
//! (register responses, punch-hole and relay requests) goes down the
//! WebSocket instead. UDP-registered devices are unaffected.

use hbb_common::{
    bytes::Bytes, protobuf::Message as _, rendezvous_proto::RendezvousMessage, tokio::sync::mpsc,
    try_into_v4, udp::FramedSocket, ResultType,
};
use std::{collections::HashMap, net::SocketAddr, sync::Mutex};

type Tx = mpsc::UnboundedSender<Bytes>;

lazy_static::lazy_static! {
    static ref PEERS: Mutex<HashMap<SocketAddr, Tx>> = Default::default();
}

pub fn add(addr: SocketAddr, tx: Tx) {
    if let Ok(mut m) = PEERS.lock() {
        m.insert(try_into_v4(addr), tx);
    }
}

pub fn remove(addr: SocketAddr) {
    if let Ok(mut m) = PEERS.lock() {
        m.remove(&try_into_v4(addr));
    }
}

fn get(addr: SocketAddr) -> Option<Tx> {
    PEERS.lock().ok()?.get(&try_into_v4(addr)).cloned()
}

/// Sends `msg` down the WebSocket of `addr` if there is one. Returns false
/// when `addr` is not a WebSocket peer (the caller then uses UDP).
pub fn try_send(msg: &RendezvousMessage, addr: SocketAddr) -> bool {
    let Some(tx) = get(addr) else {
        return false;
    };
    match msg.write_to_bytes() {
        Ok(b) => tx.send(Bytes::from(b)).is_ok(),
        Err(_) => false,
    }
}

/// Drop-in for `socket.send(msg, addr)` in the UDP handlers.
pub async fn send(
    socket: &mut FramedSocket,
    msg: &RendezvousMessage,
    addr: SocketAddr,
) -> ResultType<()> {
    if try_send(msg, addr) {
        return Ok(());
    }
    socket.send(msg, addr).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn routes_only_registered_addresses() {
        let a: SocketAddr = "203.0.113.5:40000".parse().unwrap();
        let b: SocketAddr = "203.0.113.5:40001".parse().unwrap();
        let (tx, mut rx) = mpsc::unbounded_channel();
        add(a, tx);
        let msg = RendezvousMessage::new();
        assert!(try_send(&msg, a));
        assert!(rx.try_recv().is_ok());
        assert!(!try_send(&msg, b));
        // IPv4-mapped IPv6 form of the same address is the same peer
        let mapped: SocketAddr = "[::ffff:203.0.113.5]:40000".parse().unwrap();
        assert!(try_send(&msg, mapped));
        remove(a);
        assert!(!try_send(&msg, a));
    }
}
