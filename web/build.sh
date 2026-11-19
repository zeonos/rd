#!/usr/bin/env bash
# Build the browser viewer into server/internal/webclient/static/app.js.
# Needs Node with esbuild and typescript (npm i -g esbuild typescript, or set
# ESBUILD/TSC). The server key comes from client/rd/config.json, like the client.
set -euo pipefail
cd "$(dirname "$0")"
ESBUILD="${ESBUILD:-esbuild}"
TSC="${TSC:-tsc}"
python3 gen_proto.py
KEY="$(python3 -c 'import json;print(json.load(open("../client/rd/config.json"))["override-settings"]["key"])')"
"$TSC" -p .
"$ESBUILD" src/app.ts --bundle --format=esm --target=es2022 --minify --legal-comments=eof \
  --external:crypto --define:RD_SERVER_KEY="\"$KEY\"" \
  --outfile=../server/internal/webclient/static/app.js
