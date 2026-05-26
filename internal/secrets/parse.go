// Package secrets parses tfvars-style files of `NAME = "value"` lines into
// GitHub Actions secret name/value pairs.
package secrets

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Pair is a single parsed secret.
type Pair struct {
	Name  string
	Value string
}

// EnvFile groups secrets parsed from one <env>.tfvars file.
type EnvFile struct {
	Env     string
	Source  string
	Secrets []Pair
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	lineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*(.*)$`)
	envRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

// IsValidGitHubSecretName reports whether n is a valid GitHub Actions secret
// name (alphanumerics + underscore, no leading digit, no GITHUB_ prefix).
func IsValidGitHubSecretName(n string) bool {
	if !nameRe.MatchString(n) {
		return false
	}
	if strings.HasPrefix(n, "GITHUB_") {
		return false
	}
	return true
}

// ParseFile reads a tfvars-style secrets file and returns the parsed pairs in
// declaration order. Comments (# or //) and blank lines are ignored. Values
// may be double-quoted (with `\\ \" \n \r \t` escapes) or single-quoted (raw).
// Duplicate names within a single file are an error.
func ParseFile(path string) ([]Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("secrets file not found: %s", path)
		}
		return nil, fmt.Errorf("secrets file not readable: %s: %w", path, err)
	}
	defer f.Close()

	var pairs []Pair
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineno := 0
	for scanner.Scan() {
		lineno++
		line := strings.TrimRight(scanner.Text(), "\r")
		line = strings.TrimLeft(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf(`%s:%d: cannot parse line (expected NAME = "value"): %s`, path, lineno, line)
		}
		name := m[1]
		rest := strings.TrimRight(m[2], " \t")
		val, err := decodeValue(rest)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", path, lineno, err)
		}
		if !IsValidGitHubSecretName(name) {
			return nil, fmt.Errorf("%s:%d: invalid GitHub secret name %q (alphanumerics + underscore, no leading digit, no GITHUB_ prefix)", path, lineno, name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("duplicate secret %q in %s", name, path)
		}
		seen[name] = struct{}{}
		pairs = append(pairs, Pair{Name: name, Value: val})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("%s: no secrets found", path)
	}
	return pairs, nil
}

func decodeValue(rest string) (string, error) {
	if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
		inner := rest[1 : len(rest)-1]
		return decodeDoubleQuoted(inner), nil
	}
	if len(rest) >= 2 && rest[0] == '\'' && rest[len(rest)-1] == '\'' {
		return rest[1 : len(rest)-1], nil
	}
	return "", fmt.Errorf("value must be a single quoted string")
}

func decodeDoubleQuoted(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(c)
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// LoadEnvDir loads every *.tfvars file in dir. Each basename (without
// extension) must be one of envSet. The list is sorted by env name so output
// is deterministic.
func LoadEnvDir(dir string, envSet map[string]struct{}) ([]EnvFile, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("env-secrets dir not found: %s", dir)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.tfvars"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no *.tfvars files in %s", dir)
	}
	sort.Strings(matches)

	declared := make([]string, 0, len(envSet))
	for e := range envSet {
		declared = append(declared, e)
	}
	sort.Strings(declared)

	out := make([]EnvFile, 0, len(matches))
	for _, ef := range matches {
		base := strings.TrimSuffix(filepath.Base(ef), ".tfvars")
		if !envRe.MatchString(base) {
			return nil, fmt.Errorf("invalid env name derived from filename: %s (basename must match [A-Za-z][A-Za-z0-9_-]*)", ef)
		}
		if _, ok := envSet[base]; !ok {
			return nil, fmt.Errorf("env-secrets file %q targets env %q which is not in --env list (%s)", ef, base, strings.Join(declared, " "))
		}
		pairs, err := ParseFile(ef)
		if err != nil {
			return nil, err
		}
		out = append(out, EnvFile{Env: base, Source: ef, Secrets: pairs})
	}
	return out, nil
}
