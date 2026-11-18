#!/usr/bin/env python3
"""
Dynamic workflow for reading and synchronizing the Memory Bank (.agents/memory-bank).
Compatible with Antigravity `run_workflow` and standalone execution.
"""

import sys
import os
import subprocess
from pathlib import Path
from datetime import datetime

# Provide fallback stubs when executed outside Antigravity's run_workflow runner
if "phase" not in globals():
    def phase(name):
        print(f"\n[PHASE] {name}")

if "log" not in globals():
    def log(*args):
        print("[LOG]", *args)

if "args" not in globals():
    args = {}

async def main():
    root = Path.cwd()
    mb_dir = root / ".agents" / "memory-bank"

    phase("1. Read Memory Bank Context")
    if not mb_dir.exists():
        log(f"Memory Bank directory not found at {mb_dir}. Please create it first.")
        return

    active_file = mb_dir / "activeContext.md"
    brief_file = mb_dir / "projectbrief.md"

    if active_file.is_file():
        log("Current Active Context:")
        log(active_file.read_text(encoding="utf-8").strip())
    else:
        log("activeContext.md is missing!")

    phase("2. Inspect Workspace Status")
    try:
        git_status = subprocess.check_output(
            ["git", "status", "--short"],
            text=True,
            stderr=subprocess.DEVNULL
        ).strip()
        if git_status:
            log("Uncommitted changes detected in workspace:")
            log(git_status)
        else:
            log("Workspace working tree is clean.")
    except Exception as e:
        log(f"Git check skipped: {e}")

    phase("3. Sync Active Context")
    note = args.get("note") if isinstance(args, dict) else None
    if note and active_file.is_file():
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        entry = f"\n### Update [{timestamp}]\n{note}\n"
        with open(active_file, "a", encoding="utf-8") as f:
            f.write(entry)
        log(f"Recorded new update into activeContext.md: {note}")
    else:
        log("No new note specified in args. Context state confirmed.")

    phase("4. Validate Memory Bank Structure")
    required = [
        "projectbrief.md",
        "productContext.md",
        "systemPatterns.md",
        "techContext.md",
        "activeContext.md",
        "progress.md",
    ]
    missing = [f for f in required if not (mb_dir / f).is_file()]
    if missing:
        log(f"Warning: Missing Memory Bank files: {', '.join(missing)}")
    else:
        log("All Memory Bank files are present and intact.")

await main()

