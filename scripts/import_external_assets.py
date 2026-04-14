#!/usr/bin/env python3
"""Import external skills/tools content into Heros embedded defaults.

Usage:
  python scripts/import_external_assets.py --source tmp/source-agent
"""

from __future__ import annotations

import argparse
import pathlib
import re
import shutil
import sys
from dataclasses import dataclass


@dataclass
class ImportStats:
    skills_markdown_files: int = 0
    tool_specs_written: int = 0
    tool_go_files_written: int = 0


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Import skills/tools from an external agent repository")
    p.add_argument(
        "--source",
        default="tmp/source-agent",
        help="Path to local source repository clone",
    )
    p.add_argument(
        "--dest-skills",
        default="internal/promptlayer/embedded_defaults/skills/_global/custom",
        help="Destination root for custom skill markdown",
    )
    p.add_argument(
        "--dest-tools",
        default="internal/promptlayer/embedded_defaults/tools/_global",
        help="Destination root for generated artifacts (reserved)",
    )
    p.add_argument(
        "--tool-prefix",
        default="",
        help="Reserved for compatibility (Go-native tools are implemented in code, not yaml).",
    )
    p.add_argument(
        "--emit-tool-yaml",
        action="store_true",
        default=True,
        help="Emit tool.yaml files (default: true).",
    )
    p.add_argument(
        "--clean",
        action="store_true",
        help="Delete existing custom content before importing",
    )
    return p.parse_args()


def slugify(rel_no_ext: str) -> str:
    slug = rel_no_ext.replace("\\", "/")
    slug = re.sub(r"[^a-zA-Z0-9/_-]+", "-", slug)
    slug = slug.strip("/").replace("/", "-").replace("_", "-").lower()
    slug = re.sub(r"-{2,}", "-", slug)
    return slug


def ensure_dir(path: pathlib.Path) -> None:
    path.mkdir(parents=True, exist_ok=True)


def copy_skill_markdown(source_repo: pathlib.Path, dest_root: pathlib.Path) -> int:
    source_skills = source_repo / "skills"
    if not source_skills.is_dir():
        raise FileNotFoundError(f"skills directory not found: {source_skills}")

    count = 0
    for src in source_skills.rglob("*.md"):
        rel = src.relative_to(source_skills)
        dst = dest_root / rel
        ensure_dir(dst.parent)
        shutil.copy2(src, dst)
        count += 1
    return count


def normalize_prefix(prefix: str) -> str:
    p = prefix.strip()
    if not p:
        return ""
    return p if p.endswith("-") else f"{p}-"


def generated_description(tool_id: str, rel_path: str) -> str:
    if tool_id in {"terminal-tool", "code-execution-tool"}:
        return "Execute local workspace shell commands with policy-aware output JSON."
    if tool_id in {"file-tools", "file-operations"}:
        return "Structured file actions (list/read/write/delete/mkdir) over workspace paths."
    if tool_id in {"memory-tool", "session-search-tool"}:
        return "Search and save episodic/session memory through agentd APIs."
    if tool_id.startswith("browser-providers-"):
        return "Provide browser provider endpoint metadata and invocation payload shaping."
    if tool_id.startswith("browser-"):
        return "Handle browser workflow payloads and URL-driven actions."
    if tool_id.startswith("environments-"):
        return "Execute commands in named environment mode (local/docker/ssh/modal/etc.)."
    if tool_id in {"web-tools", "url-safety", "website-policy"}:
        return "Web/url handling with HTTP fetch and URL policy checks."
    return f"Go-native extension tool handler for source module {rel_path}."


def generate_tool_stubs(source_repo: pathlib.Path, dest_tools_root: pathlib.Path, tool_prefix: str) -> int:
    source_tools = source_repo / "tools"
    if not source_tools.is_dir():
        raise FileNotFoundError(f"tools directory not found: {source_tools}")

    prefix = normalize_prefix(tool_prefix)
    count = 0
    for src in source_tools.rglob("*.py"):
        if src.name == "__init__.py":
            continue
        rel = src.relative_to(source_tools)
        rel_no_ext = str(rel.with_suffix(""))
        tool_id = f"{prefix}{slugify(rel_no_ext)}"
        tool_dir = dest_tools_root / tool_id
        ensure_dir(tool_dir)
        tool_yaml = tool_dir / "tool.yaml"
        desc = generated_description(tool_id, rel.as_posix())
        content = (
            f"id: {tool_id}\n"
            "risk_tier: medium\n"
            f"description: {desc}\n"
            "skills:\n"
            "  - core-reasoning\n"
            "implementation_language: go\n"
            "implementation_entrypoint: internal/cliagent/tools_runtime.go\n"
        )
        tool_yaml.write_text(content, encoding="utf-8")
        count += 1
    return count


def write_tool_go_markers(dest_tools_root: pathlib.Path) -> int:
    count = 0
    for d in dest_tools_root.iterdir():
        if not d.is_dir():
            continue
        y = d / "tool.yaml"
        if not y.is_file():
            continue
        g = d / "tool.go"
        g.write_text(
            "package tooldef\n\n"
            "// Tool metadata marker: runtime behavior is implemented in Go.\n"
            "const (\n"
            f"\tID = \"{d.name}\"\n"
            "\tImplementationLanguage = \"go\"\n"
            "\tRuntimeEntrypoint = \"internal/cliagent/tools_runtime.go:runImportedCatalogTool\"\n"
            ")\n",
            encoding="utf-8",
        )
        count += 1
    return count


def clean_existing(dest_skills: pathlib.Path, dest_tools_root: pathlib.Path) -> None:
    if dest_skills.exists():
        shutil.rmtree(dest_skills)
    for d in dest_tools_root.iterdir():
        if not d.is_dir():
            continue
        y = d / "tool.yaml"
        if y.is_file():
            text = y.read_text(encoding="utf-8", errors="ignore")
            if (
                "heros_extension_tool" in text
                or "Imported external tool stub" in text
                or "Imported from external source" in text
            ):
                shutil.rmtree(d)


def main() -> int:
    args = parse_args()
    root = pathlib.Path.cwd()
    source_repo = (root / args.source).resolve()
    dest_skills = (root / args.dest_skills).resolve()
    dest_tools_root = (root / args.dest_tools).resolve()

    if not source_repo.exists():
        print(f"[error] source path does not exist: {source_repo}", file=sys.stderr)
        return 1

    if args.clean:
        clean_existing(dest_skills, dest_tools_root)

    ensure_dir(dest_skills)
    ensure_dir(dest_tools_root)

    stats = ImportStats()
    stats.skills_markdown_files = copy_skill_markdown(source_repo, dest_skills)
    if args.emit_tool_yaml:
        stats.tool_specs_written = generate_tool_stubs(source_repo, dest_tools_root, args.tool_prefix)
        stats.tool_go_files_written = write_tool_go_markers(dest_tools_root)
    else:
        stats.tool_specs_written = 0
        stats.tool_go_files_written = 0

    print(f"Imported skill markdown files: {stats.skills_markdown_files}")
    if args.emit_tool_yaml:
        print(f"Generated tool stubs: {stats.tool_specs_written}")
        print(f"Generated tool Go markers: {stats.tool_go_files_written}")
    else:
        print("Generated tool stubs: 0 (Go-native mode; tool handlers live in internal/cliagent/tools_runtime.go)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
