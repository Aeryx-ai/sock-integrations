#!/usr/bin/env python3
"""Offline package checks. Never deploys, connects, uploads or creates credentials."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import sys
from urllib.parse import urlsplit
import zlib

from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parent
PACKAGE = ROOT / "sock"


def check():
    failures = []
    checks = []

    def require(ok, message):
        (checks if ok else failures).append(message)

    data = {}
    for name in ("plugin", "mcp"):
        schema = json.loads((ROOT / "schemas" / (name + ".schema.json")).read_text())
        Draft202012Validator.check_schema(schema)
        data[name] = json.loads((PACKAGE / (name + ".json")).read_text())
        errors = sorted(Draft202012Validator(schema).iter_errors(data[name]), key=lambda e: str(e.path))
        require(not errors, name + ": official Agent Plugins 1.0.0 JSON Schema")
        failures.extend(name + ": " + e.message for e in errors)

    plugin, mcp = data["plugin"], data["mcp"]
    require(PACKAGE.name == plugin["name"] == "sock", "package directory and plugin identity match")
    require(bool(re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", plugin["version"])), "strict semantic version")
    ext = plugin["extensions"]["com.openai"]
    interface = ext["interface"]
    for key, limit in {"displayName": 30, "shortDescription": 30, "longDescription": 4000, "developerName": 80}.items():
        value = interface.get(key)
        require(isinstance(value, str) and bool(value.strip()) and len(value) <= limit, key + ": nonblank and within limit")
    prompts = interface["defaultPrompt"]
    require(1 <= len(prompts) <= 3 and all(isinstance(x, str) and x.strip() and len(x) <= 128 and "\n" not in x for x in prompts), "starter prompt shape and lengths")
    require(len(set(" ".join(x.split()) for x in prompts)) == len(prompts), "unique starter prompts")
    for key in ("websiteURL", "supportURL", "privacyPolicyURL", "termsOfServiceURL"):
        url = urlsplit(interface[key])
        require(url.scheme == "https" and bool(url.hostname) and not url.username and not url.password and len(interface[key]) <= 1024, key + ": absolute HTTPS without credentials")
    servers = mcp["mcpServers"]
    require(list(servers) == ["sock"], "exactly one hosted MCP server")
    require(servers["sock"] == {"type": "streamable-http", "url": "https://mcp.joinsock.com/mcp"}, "hosted transport and source-declared production endpoint")
    cases = ext["review"]["test_cases"]
    for kind, count in (("positive", 5), ("negative", 3)):
        require(len(cases[kind]) == count, kind + ": required case count")
        keys = ("description", "prompt", "tools_triggered", "expected_behavior") if kind == "positive" else ("description", "prompt")
        require(all(all(isinstance(c.get(k), str) and c[k].strip() for k in keys) for c in cases[kind]), kind + ": nonblank string fields")
    require(len({c["prompt"] for cs in cases.values() for c in cs}) == 8, "distinct review prompts")
    require(bool(ext["publication"]["release_notes"].strip()), "release notes present")

    for field, minimum in (("logo", 256), ("composerIcon", 48)):
        relative = Path(interface[field])
        path = PACKAGE / relative
        require(not relative.is_absolute() and PACKAGE in path.resolve().parents and path.is_file() and not path.is_symlink(), field + ": contained existing asset")
        raw = path.read_bytes()
        require(raw[:8] == b"\x89PNG\r\n\x1a\n" and len(raw) <= 5 * 1024 * 1024, field + ": PNG within size limit")
        width, height = struct.unpack(">II", raw[16:24])
        require(width == height and minimum <= width <= 4096, field + ": square PNG within dimension limits")
        pos, compressed = 8, bytearray()
        while pos < len(raw):
            size = struct.unpack(">I", raw[pos:pos + 4])[0]
            kind = raw[pos + 4:pos + 8]
            chunk = raw[pos + 8:pos + 8 + size]
            crc = struct.unpack(">I", raw[pos + 8 + size:pos + 12 + size])[0]
            require((zlib.crc32(kind + chunk) & 0xffffffff) == crc, field + ": " + kind.decode() + " CRC")
            if kind == b"IDAT": compressed.extend(chunk)
            pos += size + 12
        depth, color, compression, filtering, interlace = raw[24:29]
        require(depth == 8 and color in (2, 6) and compression == filtering == interlace == 0, field + ": supported 8-bit RGB/RGBA non-interlaced PNG")
        pixels = zlib.decompress(compressed)
        stride = width * (4 if color == 6 else 3) + 1
        require(len(pixels) == stride * height, field + ": decoded scanline byte count matches dimensions")
        require(len(pixels) == stride * height and all(pixels[row * stride] <= 4 for row in range(height)), field + ": valid scanline filter bytes")
        require(raw[-12:] == b"\x00\x00\x00\x00IEND\xaeB`\x82", field + ": terminal IEND chunk")

    skill = (PACKAGE / "skills/sock/SKILL.md").read_text()
    require(skill.startswith("---\nname: sock\ndescription: ") and "\n---\n" in skill[4:] and len(skill.encode()) <= 1024 * 1024, "skill frontmatter and size")
    expected = {"plugin.json", "mcp.json", "LICENSE", "assets/sock.png", "skills/sock/SKILL.md"}
    actual = set()
    for path in PACKAGE.rglob("*"):
        require(not path.is_symlink(), "not a symlink: " + str(path.relative_to(PACKAGE)))
        if not path.is_file(): continue
        name = path.relative_to(PACKAGE).as_posix()
        actual.add(name)
        if path.suffix in (".json", ".md"):
            raw = path.read_text()
            require(not re.search(r"sock_(?:st|pt|mk|ct|ot|rt)_[A-Za-z0-9_-]+|whsec_[A-Za-z0-9+/=_-]+|-----BEGIN .*PRIVATE KEY-----", raw), name + ": no recognized credential values")
    require(actual == expected, "exact package inventory: no hooks, app bindings, marketplace, binaries or unrelated files")

    observations = json.loads((ROOT / "verification.json").read_text())
    for name, info in observations["schemas"].items():
        require(hashlib.sha256((ROOT / "schemas" / name).read_bytes()).hexdigest() == info["sha256"], name + ": matches retrieved schema digest")
    blockers = list(observations["submission_blockers"])
    for section, key, label in ((ext["review"], "demo_recording_url", "verified demo recording URL is missing"), (ext["review"], "commerce", "publisher commerce declaration is missing"), (ext["publication"], "countries", "publisher country targeting is unconfirmed")):
        if key not in section: blockers.append(label)
    return {"structural_validation": "failed" if failures else "passed", "checks_passed": len(checks), "failures": failures, "publication_readiness": "blocked" if blockers or failures else "requires portal verification", "blockers": blockers, "network_actions": "none", "zip_created": False}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--submission", action="store_true", help="also fail on unresolved publication gates")
    args = parser.parse_args()
    try:
        result = check()
    except (KeyError, ValueError, OSError, struct.error, zlib.error) as exc:
        print("Package validation failed: " + str(exc), file=sys.stderr)
        raise SystemExit(1)
    print(json.dumps(result, indent=2))
    raise SystemExit(1 if result["failures"] else 2 if args.submission and result["blockers"] else 0)
