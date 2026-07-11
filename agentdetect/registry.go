package agentdetect

import (
	"path/filepath"
	"strings"
)

// registry holds all known agent specs, rules pre-sorted by priority.
var registry = func() []*AgentSpec {
	specs := []*AgentSpec{
		copilotSpec(),
	}
	for _, spec := range specs {
		spec.sortRules()
	}
	return specs
}()

// genericRuntimes are interpreter/launcher process names that may wrap an
// agent; for these the argv is inspected for a known agent name.
var genericRuntimes = map[string]bool{
	"node": true, "bun": true, "deno": true,
	"python": true, "python3": true,
	"sh": true, "bash": true, "zsh": true,
}

// Identify maps a foreground process (comm name plus argv) to a registered
// agent spec, or nil when the process is not a recognized agent.
func Identify(procName string, argv []string) *AgentSpec {
	name := normalizeName(procName)
	if spec := specByBinary(name); spec != nil {
		return spec
	}
	// Unwrap generic runtimes: "node /path/to/copilot ..." etc.
	if genericRuntimes[name] && len(argv) > 1 {
		for _, arg := range argv[1:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if spec := specByBinary(normalizeName(arg)); spec != nil {
				return spec
			}
		}
	}
	return nil
}

func specByBinary(name string) *AgentSpec {
	if name == "" {
		return nil
	}
	for _, spec := range registry {
		for _, bin := range spec.BinaryNames {
			if name == bin {
				return spec
			}
		}
	}
	return nil
}

// normalizeName reduces a process name or argv entry to a comparable
// lowercase basename without extension or script suffix.
func normalizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = filepath.Base(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".exe")
	s = strings.TrimSuffix(s, ".js")
	s = strings.TrimSuffix(s, ".mjs")
	s = strings.TrimSuffix(s, ".cjs")
	return s
}
