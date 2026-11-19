#!/usr/bin/env python3
"""Generate web/src/schema.json from RustDesk's .proto files.

The browser viewer encodes and decodes protobuf with a small schema-driven
codec (src/pb.ts) instead of a protobuf library. This reads the subset of
proto3 RustDesk uses: messages, nested enums, oneofs, repeated fields.
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROTOS = [
    ROOT / "client/libs/hbb_common/protos/rendezvous.proto",
    ROOT / "client/libs/base/protos/message.proto",
]


def strip_comments(text):
    text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
    return re.sub(r"//[^\n]*", "", text)


def tokens(text):
    return re.findall(r"[A-Za-z_][\w.]*|\d+|[{}=;<>\[\],]|\"[^\"]*\"", text)


def parse(text, messages, enums):
    toks = tokens(strip_comments(text))
    i = 0

    def block_enum(name):
        nonlocal i
        values = {}
        assert toks[i] == "{"
        i += 1
        while toks[i] != "}":
            if toks[i] in ("option", "reserved"):
                while toks[i] != ";":
                    i += 1
                i += 1
                continue
            vname, eq, num = toks[i], toks[i + 1], toks[i + 2]
            assert eq == "=", (name, toks[i:i + 4])
            values[vname] = int(num)
            i += 3
            while toks[i] != ";":
                i += 1
            i += 1
        i += 1
        enums[name] = values

    def block_message(name):
        nonlocal i
        fields = []
        local_enums = []
        assert toks[i] == "{"
        i += 1
        oneof = None
        depth_oneof = False
        while True:
            t = toks[i]
            if t == "}":
                i += 1
                if depth_oneof:
                    depth_oneof = False
                    oneof = None
                    continue
                break
            if t == "enum":
                ename = toks[i + 1]
                i += 2
                local_enums.append(ename)
                block_enum(f"{name}.{ename}")
                continue
            if t == "message":
                raise SystemExit(f"nested message in {name} not supported")
            if t == "oneof":
                oneof = toks[i + 1]
                depth_oneof = True
                i += 3  # oneof NAME {
                continue
            if t in ("option", "reserved"):
                while toks[i] != ";":
                    i += 1
                i += 1
                continue
            label = ""
            if t in ("repeated", "optional"):
                label = "repeated" if t == "repeated" else ""
                i += 1
            ftype, fname, eq, num = toks[i], toks[i + 1], toks[i + 2], toks[i + 3]
            assert eq == "=", (name, toks[i:i + 5])
            i += 4
            while toks[i] != ";":
                i += 1
            i += 1
            fields.append([int(num), fname, ftype, label, oneof])
        messages[name] = {"fields": fields, "enums": local_enums}

    while i < len(toks):
        t = toks[i]
        if t in ("syntax", "package", "import", "option"):
            while toks[i] != ";":
                i += 1
            i += 1
        elif t == "message":
            name = toks[i + 1]
            i += 2
            block_message(name)
        elif t == "enum":
            name = toks[i + 1]
            i += 2
            block_enum(name)
        else:
            raise SystemExit(f"unexpected token {t!r}")


SCALARS = {"double", "float", "int32", "int64", "uint32", "uint64", "sint32", "sint64",
           "fixed32", "fixed64", "sfixed32", "sfixed64", "bool", "string", "bytes"}


def main():
    messages, enums = {}, {}
    for p in PROTOS:
        parse(p.read_text(), messages, enums)
    out = {}
    for name, m in messages.items():
        fields = []
        for num, fname, ftype, label, oneof in m["fields"]:
            if ftype in SCALARS:
                kind = ftype
            elif ftype in m["enums"]:
                kind = "enum"
            elif ftype in enums:
                kind = "enum"
            elif ftype in messages:
                kind = "message:" + ftype
            elif "." in ftype and ftype in enums:
                kind = "enum"
            else:
                raise SystemExit(f"{name}.{fname}: unknown type {ftype}")
            fields.append([num, fname, kind, 1 if label == "repeated" else 0, oneof or ""])
        out[name] = fields
    dest = Path(__file__).resolve().parent / "src" / "schema.json"
    dest.write_text(json.dumps({"messages": out, "enums": enums}, separators=(",", ":"), sort_keys=True) + "\n")
    print(f"{dest}: {len(out)} messages, {len(enums)} enums", file=sys.stderr)


if __name__ == "__main__":
    main()
