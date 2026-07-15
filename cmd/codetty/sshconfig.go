package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// sshConfigHosts returns the concrete Host aliases from the user's OpenSSH
// client config (~/.ssh/config), in file order, following Include
// directives. Wildcard patterns and negations are skipped: only aliases a
// user could type after "ssh" verbatim are useful as remote targets.
func sshConfigHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var hosts []string
	seen := make(map[string]bool)
	parseSSHConfigFile(filepath.Join(home, ".ssh", "config"), filepath.Join(home, ".ssh"), 0, seen, &hosts)
	return hosts
}

func parseSSHConfigFile(path, baseDir string, depth int, seen map[string]bool, hosts *[]string) {
	if depth > 4 || seen[path] {
		return
	}
	seen[path] = true
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		switch key {
		case "host":
			for _, pattern := range fields[1:] {
				if strings.ContainsAny(pattern, "*?!") {
					continue
				}
				if !seen["host:"+pattern] {
					seen["host:"+pattern] = true
					*hosts = append(*hosts, pattern)
				}
			}
		case "include":
			for _, pattern := range fields[1:] {
				if strings.HasPrefix(pattern, "~/") {
					if home, err := os.UserHomeDir(); err == nil {
						pattern = filepath.Join(home, pattern[2:])
					}
				} else if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(baseDir, pattern)
				}
				matches, err := filepath.Glob(pattern)
				if err != nil {
					continue
				}
				for _, m := range matches {
					parseSSHConfigFile(m, baseDir, depth+1, seen, hosts)
				}
			}
		}
	}
}
