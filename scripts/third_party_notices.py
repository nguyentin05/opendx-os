#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Write the license and NOTICE texts of third-party code shipped in builds.

Covers what the project's binaries and bundles contain: every Go module linked
into a command of operator/ or capabilities/ (and the Go standard library), and
the npm packages bundled into the Workspace UI, and Python AI runtime wheels.
Needs the Go module cache, workspace-ui/node_modules, and the locked AI venv.

    python3 scripts/third_party_notices.py            # rewrite the file
    python3 scripts/third_party_notices.py --check    # fail when it is stale
"""

from __future__ import annotations

import importlib.metadata
import json
import os
import pathlib
import platform
import re
import subprocess
import sys
import sysconfig
import tomllib

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "THIRD_PARTY_LICENSES.txt"
LEGAL = re.compile(r"(?i)^(licen[cs]e|copying|notice)([.-].*)?$")


PYTHON_LEGAL = re.compile(r"(?i)^(licen[cs]es?|copying|notice|copyright)([._-].*)?$")

CODE = (".go", ".js", ".mjs", ".cjs", ".ts", ".json", ".py")


def legal_files(directory: pathlib.Path) -> list[pathlib.Path]:
    return sorted(p for p in directory.iterdir()
                  if p.is_file() and LEGAL.match(p.name) and not p.name.endswith(CODE))


def go_modules() -> dict[tuple[str, str], tuple[pathlib.Path, set[str]]]:
    modules: dict[tuple[str, str], tuple[pathlib.Path, set[str]]] = {}
    fmt = "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}"
    for module in ("operator", "capabilities"):
        out = subprocess.run(["go", "list", "-deps", "-f", fmt, "./cmd/..."], cwd=ROOT / module,
                             check=True, capture_output=True, text=True).stdout
        for line in filter(None, out.splitlines()):
            path, version, directory = line.split("\t")
            modules.setdefault((path, version), (pathlib.Path(directory), set()))[1].add(module)
    return modules


def npm_runtime() -> list[tuple[str, str, pathlib.Path]]:
    ui = ROOT / "workspace-ui"
    packages = json.loads((ui / "package-lock.json").read_text())["packages"]
    return sorted((key.removeprefix("node_modules/"), meta["version"], ui / key)
                  for key, meta in packages.items()
                  if key and not meta.get("dev") and not meta.get("devOptional"))


# The Apache-2.0 terms are in the repository's LICENSE; each copy is replaced by a
# pointer, keeping whatever surrounds it (copyright lines, appendix).
APACHE_TERMS = re.compile(r"Apache License\s+Version 2\.0, January 2004.*?END OF TERMS AND CONDITIONS", re.S)


def text(path: pathlib.Path) -> str:
    return APACHE_TERMS.sub("[Apache License, Version 2.0: full terms in LICENSE at the repository root]",
                            path.read_text(errors="replace")).strip()


def section(title: str, files: list[pathlib.Path], notes: list[str] | None = None) -> str:
    if not files and not notes:
        sys.exit(f"no license file for {title}")
    body = "".join(f"{note}\n" for note in notes or [])
    body += "\n".join(f"--- {f.name} ---\n{text(f)}\n" for f in files)
    return f"{'=' * 79}\n{title}\n{'=' * 79}\n{body}\n"


def python_sections(
    runtime: list[tuple[str, str]], site: pathlib.Path,
    reviewed: dict | None = None, license_root: pathlib.Path = ROOT / "licenses/python",
) -> list[str]:
    def normalize(name: str) -> str:
        return re.sub(r"[-_.]+", "-", name).lower()

    root = site.resolve()
    installed = {(normalize(dist.name), dist.version): dist
                 for dist in importlib.metadata.distributions(path=[str(site)]) if dist.name}
    parts = []
    for name, version in sorted(runtime):
        title = f"{name} {version} (Python; AI image)"
        dist = installed.get((normalize(name), version))
        if dist is None:
            parts.append(section(title, [], ["UNVERIFIED: locked distribution metadata unavailable"]))
            continue

        declared = set(dist.metadata.get_all("License-File", []))
        found = set()
        files = []
        has_license = False
        for entry in sorted(dist.files or []):
            relative = ""
            if entry.parts[0].endswith(".dist-info"):
                relative_parts = entry.parts[1:]
                if relative_parts and relative_parts[0] == "licenses":
                    relative_parts = relative_parts[1:]
                relative = pathlib.PurePath(*relative_parts).as_posix()
            is_legal = PYTHON_LEGAL.match(entry.name) and not entry.name.lower().endswith(CODE)
            if relative not in declared and not is_legal:
                continue
            path = dist.locate_file(entry)
            if not path.is_file() or not path.resolve().is_relative_to(root):
                continue
            if relative in declared:
                found.add(relative)
            files.append(path)
            has_license |= not entry.name.lower().startswith("notice")

        expression = dist.metadata.get("License-Expression")
        evidence = (reviewed or {}).get(f"{name} {version}")
        notes = [f"Declared license expression: {expression}"] if expression else []
        if evidence:
            notes.extend([f"Reviewed upstream license: {evidence['license']}",
                          f"Upstream license evidence: {evidence['source']}"])
            supplemental = [license_root / filename for filename in evidence.get("files", [])]
            files.extend(supplemental)
            has_license |= bool(supplemental)
            notes.extend(evidence.get("notes", []))
        elif not expression:
            notes.append("UNVERIFIED: license expression unverified (no wheel License-Expression)")
        if not has_license:
            notes.append("UNVERIFIED: license text unavailable in installed wheel")
        notes.extend(f"UNVERIFIED: declared file unavailable: {name}" for name in sorted(declared - found))
        parts.append(section(title, files, notes))
    return parts


def python_runtime() -> list[str]:
    from packaging.markers import Marker

    lock = subprocess.run(
        ["uv", "export", "--locked", "--no-default-groups", "--no-emit-project",
         "--format", "pylock.toml"],
        cwd=ROOT / "capabilities/ai", check=True, capture_output=True, text=True,
    ).stdout
    runtime = [(pkg["name"], pkg["version"]) for pkg in tomllib.loads(lock)["packages"]
               if "marker" not in pkg or Marker(pkg["marker"]).evaluate()]
    reviewed = json.loads((ROOT / "licenses/python/reviewed.json").read_text())
    return python_sections(runtime, pathlib.Path(sysconfig.get_path("purelib")), reviewed)


def render() -> str:
    goroot = pathlib.Path(subprocess.run(["go", "env", "GOROOT"], cwd=ROOT / "operator", check=True,
                                         capture_output=True, text=True).stdout.strip())
    goversion = subprocess.run(["go", "env", "GOVERSION"], cwd=ROOT / "operator", check=True,
                               capture_output=True, text=True).stdout.strip()
    parts = [
        "Third-party license texts for OpenDX-OS builds.\n"
        "Generated by scripts/third_party_notices.py; do not edit by hand.\n"
        "Go modules are linked into the binaries of the named module directory;\n"
        "npm packages are bundled into the Workspace UI. Python distributions are\n"
        "installed in the AI image. See COPYRIGHT.txt.\n\n",
        section(f"Go standard library ({goversion}), linked into every Go binary",
                [goroot / "LICENSE"]),
    ]
    for (path, version), (directory, used_by) in sorted(go_modules().items()):
        parts.append(section(f"{path} {version} (Go; {', '.join(sorted(used_by))})", legal_files(directory)))
    for name, version, directory in npm_runtime():
        parts.append(section(f"{name} {version} (npm; workspace-ui bundle)", legal_files(directory)))
    parts.extend(python_runtime())
    return "".join(parts)


def main() -> int:
    venv = ROOT / "capabilities/ai/.venv"
    if pathlib.Path(sys.prefix).resolve() != venv.resolve():
        interpreter = venv / "bin/python"
        if not interpreter.is_file():
            sys.exit("Prepare capabilities/ai/.venv with a locked uv sync before running this script.")
        os.execv(interpreter, [str(interpreter), str(pathlib.Path(__file__).resolve()), *sys.argv[1:]])
    if (sys.implementation.name, sys.version_info[:2], sys.platform, platform.machine()) != (
        "cpython", (3, 12), "linux", "x86_64"
    ):
        sys.exit("The AI image inventory requires Linux x86-64 CPython 3.12.")
    text = render()
    title = ""
    for line in text.splitlines():
        if line.endswith("(Python; AI image)"):
            title = line
        elif line.startswith("UNVERIFIED:"):
            print(f"{title}: {line}", file=sys.stderr)
    if "--check" in sys.argv[1:]:
        if not OUT.exists() or OUT.read_bytes() != text.encode("utf-8"):
            print(f"{OUT.relative_to(ROOT)} is stale; rerun without --check", file=sys.stderr)
            return 1
        return 0
    OUT.write_bytes(text.encode("utf-8"))
    print(f"wrote {OUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
