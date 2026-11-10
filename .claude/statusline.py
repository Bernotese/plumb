#!/usr/bin/env python3
"""Claude Code status line for the Plump team: role · model · context · branch · codex usage."""
import json, os, subprocess, sys

ROLES = {"pl-lead": ("◆", "LEAD", "38;5;213"), "pl-architect": ("▲", "ARCH", "38;5;141"),
         "pl-frontend": ("◐", "FRONT", "38;5;117"), "pl-backend": ("■", "BACK", "38;5;114"),
         "pl-reviewer": ("⛨", "REVIEW", "38;5;215")}
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
name = os.environ.get("PLUMP_AGENT", "")
icon, role, col = ROLES.get(name, ("●", name.upper() or "CLAUDE", "38;5;250"))
model = ((d.get("model") or {}).get("display_name")) or "Claude"
cwd = ((d.get("workspace") or {}).get("current_dir")) or d.get("cwd") or os.getcwd()
ctx = ""
cw = d.get("context_window") or {}
pct = cw.get("used_percentage") if isinstance(cw, dict) else None
if pct is None and isinstance(cw, dict) and cw.get("total_input_tokens") and cw.get("context_window_size"):
    pct = 100 * cw["total_input_tokens"] / cw["context_window_size"]
if pct is not None:
    c = "38;5;114" if pct < 50 else "38;5;221" if pct < 80 else "38;5;203"
    ctx = f" \033[{c}mctx {pct:.0f}%\033[0m"
try:
    br = subprocess.run(["git", "-C", cwd, "branch", "--show-current"], capture_output=True, text=True, timeout=1).stdout.strip()
except Exception:
    br = ""
try:
    cu = subprocess.run([os.path.expanduser("~/.local/bin/codex-usage")], capture_output=True, text=True, timeout=2).stdout.strip()
except Exception:
    cu = ""
parts = [f"\033[1;{col}m{icon} {role}\033[0m", f"\033[38;5;245m{model}\033[0m"]
line = "  ".join(parts) + ctx
if br:
    line += f"  \033[38;5;109m⎇ {br}\033[0m"
if cu:
    line += f"  \033[38;5;240m{cu}\033[0m"
print(line)
