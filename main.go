// ctk — Context Token Killer. A standalone, brew-installable CLI that compresses
// Claude Code tool output via a PostToolUse hook. Broader scope than rtk: covers
// Bash, Grep, Read, and all mcp__* tools, not just shell commands.
package main

import (
	"fmt"
	"os"
)

var version = "0.1.0"

func usage() {
	fmt.Fprint(os.Stderr, `ctk — Context Token Killer

usage:
  ctk hook                 run the PostToolUse compressor (reads event JSON on stdin)
  ctk init [path]          wire the hook into a repo's .claude/settings.json
  ctk init --global        wire it into ~/.claude/settings.json (all projects)
  ctk uninstall [path]     remove the ctk hook entry
  ctk gain                 token savings for the current project
  ctk gain --global        savings across ALL projects (works from any folder)
  ctk gain --history       recent per-call savings (+ --global for all projects)
  ctk gain --reset         clear stats (+ --global clears every project)
  ctk version

init flags:
  --global       install to ~/.claude (uses an absolute path to this binary's name on PATH)
  --with-rtk     drop Bash from the matcher so rtk owns shell commands
  --matcher S    override the tool matcher (default: Bash|Grep|Read|mcp__.*)
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "hook":
		cmdHook()
	case "init":
		cmdInit(os.Args[2:])
	case "uninstall":
		cmdUninstall(os.Args[2:])
	case "gain":
		cmdGain(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("ctk", version)
	default:
		usage()
		os.Exit(1)
	}
}
