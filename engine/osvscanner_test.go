package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestIgnoredVulnerabilities requires every advisory osv-scanner.toml ignores
// to have a reason that names a module and version go.mod requires. It reads
// both files and asks no database.
func TestIgnoredVulnerabilities(t *testing.T) {
	t.Parallel()
	for _, p := range ignoreProblems(readFile(t, "osv-scanner.toml"), readFile(t, "go.mod")) {
		t.Error(p)
	}
}

// ignoredVuln is one [[IgnoredVulns]] entry of an osv-scanner.toml.
type ignoredVuln struct {
	line       int // of its [[IgnoredVulns]] line, from 1
	id, reason string
	unread     bool // its id or reason could not be read, which is reported
}

var (
	tomlKey = regexp.MustCompile(`^(\w+)\s*=\s*(.+)$`)
	// checkedAt matches a reason that starts with the module and version it
	// was checked at, as in "example.com/module v1.2.3: why".
	checkedAt = regexp.MustCompile(`^(\S+) (v\S+): \S`)
)

// ignoreProblems returns one message for each problem in config, the text of
// an osv-scanner.toml, given the text of go.mod: an advisory ignored without an
// id, twice or without a reason; a reason that does not start with the module
// and version it was checked at, or names a module go.mod does not require or
// requires at another version; and a line that is not a comment, an
// [[IgnoredVulns]] entry or one of the keys every OSV-Scanner release so far
// reads there (id, reason, ignoreUntil), as nothing else has a rule here. An
// id and a reason are each one string in double quotes, alone on a line.
func ignoreProblems(config, gomod string) []string {
	const file = "osv-scanner.toml"
	var problems []string
	var entries []*ignoredVuln
	var entry *ignoredVuln
	set := map[string]bool{} // the keys of entry
	n := 0
	for line := range strings.Lines(config) {
		n++
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[IgnoredVulns]]" {
			entry = &ignoredVuln{line: n}
			entries = append(entries, entry)
			clear(set)
			continue
		}
		m := tomlKey.FindStringSubmatch(line)
		if m == nil || entry == nil {
			entry = nil
			problems = append(problems, fmt.Sprintf("%s:%d: %q: only [[IgnoredVulns]] entries and their keys have a rule here", file, n, line))
			continue
		}
		key, value := m[1], m[2]
		if set[key] {
			problems = append(problems, fmt.Sprintf("%s:%d: %s is set twice in one entry", file, n, key))
			continue
		}
		set[key] = true
		switch key {
		case "id", "reason":
			s, err := strconv.Unquote(value)
			if err != nil || !strings.HasPrefix(value, `"`) {
				problems = append(problems, fmt.Sprintf("%s:%d: %s is not one string in double quotes, alone on its line", file, n, key))
				entry.unread = true
				continue
			}
			if key == "id" {
				entry.id = s
			} else {
				entry.reason = s
			}
		case "ignoreUntil": // when the scanner reports the advisory again
		default:
			// OSV-Scanner refuses a file with a key it does not know, and with
			// it every ignore. effectiveUntil is such a key before 2.7.1, so
			// for the 2.5.1 of the OpenSSF Scorecard too.
			problems = append(problems, fmt.Sprintf("%s:%d: not every OSV-Scanner release knows the key %q of an ignored advisory, and one that does not refuses the whole file", file, n, key))
		}
	}
	required := requiredModules(gomod)
	seen := map[string]bool{}
	for _, e := range entries {
		if e.unread {
			continue
		}
		at := fmt.Sprintf("%s:%d: ", file, e.line)
		if e.id == "" {
			problems = append(problems, at+"the entry has no id")
			continue
		}
		if seen[e.id] {
			problems = append(problems, at+e.id+" is ignored twice")
			continue
		}
		seen[e.id] = true
		if strings.TrimSpace(e.reason) == "" {
			problems = append(problems, at+e.id+" is ignored without a reason")
			continue
		}
		m := checkedAt.FindStringSubmatch(e.reason)
		if m == nil {
			problems = append(problems, at+"the reason of "+e.id+` does not start with the module and version it was checked at, as in "example.com/module v1.2.3: why"`)
			continue
		}
		switch version, ok := required[m[1]]; {
		case !ok:
			problems = append(problems, at+e.id+" is ignored for "+m[1]+", which go.mod does not require: remove the entry")
		case version != m[2]:
			problems = append(problems, at+"the reason of "+e.id+" was checked at "+m[1]+" "+m[2]+", and go.mod requires "+version+": check it again, then name that version")
		}
	}
	return problems
}

// requiredModules returns the version of each module the go.mod text requires.
func requiredModules(gomod string) map[string]string {
	modules := map[string]string{}
	block := false // inside require ( ... )
	for line := range strings.Lines(gomod) {
		line, _, _ = strings.Cut(line, "//")
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "require" && f[1] == "(":
			block = true
		case len(f) == 1 && f[0] == ")":
			block = false
		case block && len(f) == 2:
			modules[f[0]] = f[1]
		case len(f) == 3 && f[0] == "require":
			modules[f[1]] = f[2]
		}
	}
	return modules
}

func TestIgnoreProblems(t *testing.T) {
	t.Parallel()
	const gomod = "module example.com/engine\n\ngo 1.26.10\n\nrequire example.com/direct v1.2.3\n\n" +
		"require (\n\texample.com/other v0.4.0 // indirect\n\texample.com/policy v1.19.1 // indirect\n)\n\n" +
		"replace (\n\texample.com/gone v1.0.0 => ../gone\n)\n"
	const good = "# Why each advisory is ignored.\n\n[[IgnoredVulns]]\nid = \"GO-2026-0001\"\n" +
		"reason = \"example.com/policy v1.19.1: the engine builds none of the affected packages\"\n"
	entry := func(lines ...string) string {
		return "[[IgnoredVulns]]\n" + strings.Join(lines, "\n") + "\n"
	}
	tests := []struct {
		name   string
		config string
		want   string // part of the one problem; none when empty
	}{
		{"one entry with a reason", good, ""},
		{"no entries", "# Nothing is ignored.\n", ""},
		{"two entries, one with a date", good + "\n" + entry(`id = "GO-2026-0002"`, `ignoreUntil = 2027-05-09`, `reason = "example.com/direct v1.2.3: a reason"`), ""},
		{"no reason", entry(`id = "GO-2026-0001"`), "GO-2026-0001 is ignored without a reason"},
		{"blank reason", entry(`id = "GO-2026-0001"`, `reason = "  "`), "GO-2026-0001 is ignored without a reason"},
		{"reason names no module", entry(`id = "GO-2026-0001"`, `reason = "does not apply"`), "does not start with the module and version"},
		{"reason is only the module", entry(`id = "GO-2026-0001"`, `reason = "example.com/policy v1.19.1: "`), "does not start with the module and version"},
		{"module no longer required", entry(`id = "GO-2026-0001"`, `reason = "example.com/removed v1.0.0: not built"`), "example.com/removed, which go.mod does not require"},
		{"module only replaced", entry(`id = "GO-2026-0001"`, `reason = "example.com/gone v1.0.0: not built"`), "example.com/gone, which go.mod does not require"},
		{"another version required", entry(`id = "GO-2026-0001"`, `reason = "example.com/policy v1.19.0: not built"`), "go.mod requires v1.19.1"},
		{"no id", entry(`reason = "example.com/policy v1.19.1: not built"`), "the entry has no id"},
		{"ignored twice", good + good, "GO-2026-0001 is ignored twice"},
		{"reason set twice", good + `reason = "example.com/policy v1.19.1: again"` + "\n", "reason is set twice"},
		{"reason in single quotes", entry(`id = "GO-2026-0001"`, `reason = 'example.com/policy v1.19.1: not built'`), "reason is not one string in double quotes"},
		{"reason over several lines", entry(`id = "GO-2026-0001"`, `reason = """`), "reason is not one string in double quotes"},
		{"unknown key", good + `module = "example.com/policy"` + "\n", `knows the key "module"`},
		{"a key only OSV-Scanner 2.7.1 and newer know", good + "effectiveUntil = 2027-05-09\n", `knows the key "effectiveUntil"`},
		{"reason with a comment after it", entry(`id = "GO-2026-0001"`, `reason = "example.com/policy v1.19.1: not built" # checked`), "reason is not one string in double quotes"},
		{"key before an entry", `GoVersionOverride = "1.27.0"` + "\n" + good, "only [[IgnoredVulns]] entries and their keys have a rule"},
		{"another table", good + "\n[[PackageOverrides]]\n", "only [[IgnoredVulns]] entries and their keys have a rule"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ignoreProblems(tt.config, gomod)
			if tt.want == "" && len(got) != 0 {
				t.Errorf("ignoreProblems(%q) = %q, want no problems", tt.config, got)
			}
			if tt.want != "" && (len(got) != 1 || !strings.Contains(got[0], tt.want)) {
				t.Errorf("ignoreProblems(%q) = %q, want one problem with %q", tt.config, got, tt.want)
			}
		})
	}
}
