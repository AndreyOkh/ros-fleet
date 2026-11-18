#!/usr/bin/env python3
"""
Helper script for managing the Memory Bank in ./.agents/memory-bank.
Supports summarizing, validating, initializing, and updating context.
"""

import sys
import os
import argparse
from datetime import datetime
from pathlib import Path

REQUIRED_FILES = [
    "projectbrief.md",
    "productContext.md",
    "systemPatterns.md",
    "techContext.md",
    "activeContext.md",
    "progress.md",
]

def find_memory_bank_dir() -> Path:
    cwd = Path.cwd()
    for parent in [cwd] + list(cwd.parents):
        mb = parent / ".agents" / "memory-bank"
        if mb.is_dir():
            return mb
    # Default fallback relative to current directory
    return cwd / ".agents" / "memory-bank"

def cmd_validate(mb_dir: Path) -> int:
    """Check that all required files exist and are not empty."""
    print(f"Checking Memory Bank at: {mb_dir}")
    if not mb_dir.exists():
        print(f"[FAIL] Directory does not exist: {mb_dir}")
        return 1

    missing = []
    empty = []
    for filename in REQUIRED_FILES:
        filepath = mb_dir / filename
        if not filepath.is_file():
            missing.append(filename)
        elif filepath.stat().st_size == 0:
            empty.append(filename)

    if missing:
        print(f"[FAIL] Missing files: {', '.join(missing)}")
    if empty:
        print(f"[WARN] Empty files: {', '.join(empty)}")

    if not missing and not empty:
        print("[OK] All required Memory Bank files exist and are populated.")
        return 0
    return 1 if missing else 0

def cmd_summary(mb_dir: Path) -> int:
    """Print a concise summary of active context and project status."""
    if not mb_dir.exists():
        print(f"Memory Bank not found at {mb_dir}")
        return 1

    active_file = mb_dir / "activeContext.md"
    progress_file = mb_dir / "progress.md"
    brief_file = mb_dir / "projectbrief.md"

    print("==================================================")
    print("           MEMORY BANK CONTEXT SUMMARY            ")
    print("==================================================")

    if brief_file.is_file():
        print("\n--- Project Brief (Header) ---")
        lines = brief_file.read_text(encoding="utf-8").splitlines()
        for line in lines[:8]:
            print(line)

    if active_file.is_file():
        print("\n--- Active Context ---")
        print(active_file.read_text(encoding="utf-8").strip())

    if progress_file.is_file():
        print("\n--- Progress Highlights ---")
        lines = progress_file.read_text(encoding="utf-8").splitlines()
        # Print lines that look like checkboxes or headers
        for line in lines:
            if line.startswith("#") or line.strip().startswith("- [ ]") or "In Progress" in line:
                print(line)

    print("\n==================================================")
    return 0

def cmd_update_active(mb_dir: Path, note: str) -> int:
    """Prepend or append a timestamped note to activeContext.md."""
    active_file = mb_dir / "activeContext.md"
    if not active_file.is_file():
        print(f"[ERROR] {active_file} not found.")
        return 1

    timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    entry = f"\n### Update [{timestamp}]\n{note}\n"
    
    content = active_file.read_text(encoding="utf-8")
    new_content = content + entry
    active_file.write_text(new_content, encoding="utf-8")
    print(f"[OK] Appended note to {active_file}")
    return 0

def main():
    parser = argparse.ArgumentParser(description="Memory Bank Management Utility")
    parser.add_argument("--dir", type=str, help="Custom path to .agents/memory-bank")
    subparsers = parser.add_subparsers(dest="action")

    subparsers.add_parser("validate", help="Validate presence and non-emptiness of memory bank files")
    subparsers.add_parser("summary", help="Print concise summary of active context and progress")
    
    update_parser = subparsers.add_parser("update-active", help="Append a note to activeContext.md")
    update_parser.add_argument("note", type=str, help="Text of the note/update")

    args = parser.parse_args()

    mb_dir = Path(args.dir) if args.dir else find_memory_bank_dir()

    if args.action == "validate":
        sys.exit(cmd_validate(mb_dir))
    elif args.action == "summary":
        sys.exit(cmd_summary(mb_dir))
    elif args.action == "update-active":
        sys.exit(cmd_update_active(mb_dir, args.note))
    else:
        # Default behavior: if no subcommand, output summary
        sys.exit(cmd_summary(mb_dir))

if __name__ == "__main__":
    main()
