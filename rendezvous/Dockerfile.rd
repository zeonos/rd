# RD build of hbbs/hbbr (rustdesk-server + WebSocket registration, see src/ws_peers.rs).
# Same command line and data layout as rustdesk/rustdesk-server: data in /root.
FROM rust:1-bookworm AS build
WORKDIR /src
COPY . .
RUN cargo build --release --locked --bin hbbs --bin hbbr

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/target/release/hbbs /src/target/release/hbbr /usr/bin/
WORKDIR /root
EXPOSE 21115 21116 21116/udp 21117 21118 21119
