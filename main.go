// gh-repo-bootstrap: apply standard branch protection + environments to a
// GitHub repository, using the bundled OpenTofu `repo` module.
//
// Installed as a gh extension, invoked as: gh repo-bootstrap <owner/repo> [opts]
package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

//go:embed modules/repo/main.tf modules/repo/variables.tf modules/repo/outputs.tf modules/repo/versions.tf
var moduleFS embed.FS

const moduleSubpath = "modules/repo"

const usageText = `Usage:
  gh repo-bootstrap <owner/repo> [options]

Apply a standard branch-protection ruleset and a set of deployment
environments to an existing GitHub repository, via OpenTofu.

Options:
  --branch NAME        Default branch to protect (default: main)
  --reviews N          Required PR approving reviews (default: 1)
  --signed             Require signed commits on the protected branch
  --env NAME           Add a deployment environment (repeatable)
                       Default if none given: production
  --ruleset NAME       Ruleset name (default: default-branch-protection)
  --bypass SPEC        Add a bypass actor (repeatable). SPEC is
                       <actor_type>:<actor_id>[:<mode>]
                         actor_type: RepositoryRole | Team | Integration |
                                     OrganizationAdmin | DeployKey
                         actor_id:   numeric ID (built-in repo roles:
                                     1=read 2=triage 3=write 4=maintain 5=admin)
                         mode:       always | pull_request   (default: always)
                       Shortcut: --solo  is equivalent to
                         --bypass RepositoryRole:5:always
  --solo               Allow the Admin repo role to bypass the ruleset.
  --upload-repo-secrets FILE
                       Upload repository-level GitHub Actions secrets
                       sourced from a tfvars-style file. Each non-blank,
                       non-comment line must be: SECRET_NAME = "value"
                       Names must match GitHub's rules (alphanumerics +
                       underscore, no leading digit, no GITHUB_ prefix).
                       Comments (#, //) and blank lines are allowed.
  --upload-env-secrets DIR
                       Upload environment-level GitHub Actions secrets.
                       DIR must contain one <env>.tfvars per env, where
                       <env> matches one of the --env values.
  --plan               Run ` + "`tofu plan`" + ` instead of ` + "`tofu apply`" + `
  --destroy            Run ` + "`tofu destroy`" + `
  --state-dir DIR      Override working/state directory
                       (default: $XDG_STATE_HOME/gh-repo-bootstrap or
                                 ~/.local/state/gh-repo-bootstrap on Unix,
                                 %LOCALAPPDATA%\gh-repo-bootstrap on Windows)
  -h, --help           Show this help

Authentication:
  GITHUB_TOKEN is auto-populated from ` + "`gh auth token`" + ` if not already set.

Secrets & state:
  Uploaded secret values are sent to GitHub encrypted, but they are
  ALSO stored in plaintext in the OpenTofu state file under the
  per-repo state directory. Protect that directory accordingly.
`

func usage() { fmt.Fprint(os.Stderr, usageText) }

func errf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "gh-repo-bootstrap: "+format+"\n", a...)
}

type bypassActor struct {
	ActorType  string
	ActorID    int
	BypassMode string
}

type options struct {
	repo            string
	owner           string
	name            string
	branch          string
	reviews         int
	signed          bool
	ruleset         string
	envs            []string
	bypass          []bypassActor
	action          string // apply | plan | destroy
	stateDir        string
	repoSecretsFile string
	envSecretsDir   string
}

var (
	validActorTypes  = map[string]bool{"RepositoryRole": true, "Team": true, "Integration": true, "OrganizationAdmin": true, "DeployKey": true}
	validBypassModes = map[string]bool{"always": true, "pull_request": true}
	secretNameRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envNameRe        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	tfvarsLineRe     = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)[\t ]*=[\t ]*(.*)$`)
)

var errHelp = errors.New("help requested")

func parseArgs(argv []string) (*options, error) {
	opts := &options{
		branch:  "main",
		reviews: 1,
		ruleset: "default-branch-protection",
		action:  "apply",
	}

	needValue := func(i int, flag string) (string, error) {
		if i+1 >= len(argv) {
			return "", fmt.Errorf("flag %s requires a value", flag)
		}
		v := argv[i+1]
		if v == "" {
			return "", fmt.Errorf("flag %s requires a non-empty value", flag)
		}
		return v, nil
	}

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch a {
		case "-h", "--help":
			return nil, errHelp
		case "--branch":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			opts.branch = v
			i++
		case "--reviews":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("--reviews must be a non-negative integer (got: %s)", v)
			}
			opts.reviews = n
			i++
		case "--signed":
			opts.signed = true
		case "--ruleset":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			opts.ruleset = v
			i++
		case "--env":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			opts.envs = append(opts.envs, v)
			i++
		case "--bypass":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			b, err := parseBypassSpec(v)
			if err != nil {
				return nil, err
			}
			opts.bypass = append(opts.bypass, b)
			i++
		case "--solo":
			opts.bypass = append(opts.bypass, bypassActor{ActorType: "RepositoryRole", ActorID: 5, BypassMode: "always"})
		case "--upload-repo-secrets":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			opts.repoSecretsFile = v
			i++
		case "--upload-env-secrets":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			opts.envSecretsDir = v
			i++
		case "--plan":
			opts.action = "plan"
		case "--destroy":
			opts.action = "destroy"
		case "--state-dir":
			v, err := needValue(i, a)
			if err != nil {
				return nil, err
			}
			opts.stateDir = v
			i++
		case "--":
			// remaining args are positional; we only accept one (repo)
			for _, rest := range argv[i+1:] {
				if opts.repo != "" {
					return nil, fmt.Errorf("unexpected positional argument: %s", rest)
				}
				opts.repo = rest
			}
			i = len(argv)
		default:
			if strings.HasPrefix(a, "-") {
				return nil, fmt.Errorf("unknown option: %s", a)
			}
			if opts.repo != "" {
				return nil, fmt.Errorf("unexpected positional argument: %s", a)
			}
			opts.repo = a
		}
	}

	if opts.repo == "" || !strings.Contains(opts.repo, "/") {
		return nil, errors.New("first argument must be <owner>/<repo>")
	}
	parts := strings.SplitN(opts.repo, "/", 2)
	opts.owner, opts.name = parts[0], parts[1]
	if opts.owner == "" || opts.name == "" {
		return nil, fmt.Errorf("invalid <owner>/<repo>: %s", opts.repo)
	}

	if len(opts.envs) == 0 {
		opts.envs = []string{"production"}
	}

	return opts, nil
}

func parseBypassSpec(spec string) (bypassActor, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return bypassActor{}, fmt.Errorf("invalid --bypass SPEC %q (expected <actor_type>:<actor_id>[:<mode>])", spec)
	}
	bType := parts[0]
	bIDStr := parts[1]
	bMode := "always"
	if len(parts) == 3 && parts[2] != "" {
		bMode = parts[2]
	}
	if bType == "" || bIDStr == "" {
		return bypassActor{}, fmt.Errorf("invalid --bypass SPEC %q (expected <actor_type>:<actor_id>[:<mode>])", spec)
	}
	bID, err := strconv.Atoi(bIDStr)
	if err != nil || bID < 0 {
		return bypassActor{}, fmt.Errorf("invalid --bypass SPEC %q (actor_id must be numeric)", spec)
	}
	if !validActorTypes[bType] {
		return bypassActor{}, fmt.Errorf("invalid --bypass actor_type %q", bType)
	}
	if !validBypassModes[bMode] {
		return bypassActor{}, fmt.Errorf("invalid --bypass mode %q (must be 'always' or 'pull_request')", bMode)
	}
	return bypassActor{ActorType: bType, ActorID: bID, BypassMode: bMode}, nil
}

// hclString returns an HCL/JSON-compatible quoted string literal for s.
// HCL accepts JSON-style escaped strings for double-quoted literals, so this
// is safe for any user-supplied input.
func hclString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string never fails; fall back defensively.
		return strconv.Quote(s)
	}
	return string(b)
}

func hclStringList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = hclString(it)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func hclBypassList(items []bypassActor) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, len(items))
	for i, b := range items {
		parts[i] = fmt.Sprintf(
			"{ actor_id = %d, actor_type = %s, bypass_mode = %s }",
			b.ActorID, hclString(b.ActorType), hclString(b.BypassMode),
		)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// defaultStateDir mirrors $XDG_STATE_HOME on Unix and %LOCALAPPDATA% on Windows.
func defaultStateDir() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "gh-repo-bootstrap"), nil
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "gh-repo-bootstrap"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "gh-repo-bootstrap"), nil
}

// extractModule writes the embedded module .tf files into destDir, replacing
// any prior contents (so stale files from a previous version don't linger).
func extractModule(destDir string) error {
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("clean module dir: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create module dir: %w", err)
	}
	entries, err := fs.ReadDir(moduleFS, moduleSubpath)
	if err != nil {
		return fmt.Errorf("read embedded module: %w", err)
	}
	if len(entries) == 0 {
		return errors.New("no embedded module files (build error?)")
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(moduleFS, moduleSubpath+"/"+e.Name())
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(destDir, e.Name()), data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}
	return nil
}

func writeMainTF(stateDir, modulePath string, opts *options) error {
	var sb strings.Builder
	sb.WriteString("# Generated by gh repo-bootstrap. Edits will be overwritten.\n")
	sb.WriteString(`terraform {
  required_version = ">= 1.8.0"
  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.2"
    }
  }
}

`)
	fmt.Fprintf(&sb, "provider \"github\" {\n  owner = %s\n}\n\n", hclString(opts.owner))
	fmt.Fprintf(&sb, "module \"repo\" {\n  source = %s\n\n", hclString(filepath.ToSlash(modulePath)))
	fmt.Fprintf(&sb, "  repo_owner             = %s\n", hclString(opts.owner))
	fmt.Fprintf(&sb, "  repo_name              = %s\n", hclString(opts.name))
	fmt.Fprintf(&sb, "  default_branch         = %s\n", hclString(opts.branch))
	fmt.Fprintf(&sb, "  required_reviews       = %d\n", opts.reviews)
	fmt.Fprintf(&sb, "  require_signed_commits = %t\n", opts.signed)
	fmt.Fprintf(&sb, "  ruleset_name           = %s\n", hclString(opts.ruleset))
	fmt.Fprintf(&sb, "  environments           = %s\n", hclStringList(opts.envs))
	fmt.Fprintf(&sb, "  bypass_actors          = %s\n", hclBypassList(opts.bypass))
	sb.WriteString("}\n\n")
	sb.WriteString(`output "repository_full_name" { value = module.repo.repository_full_name }
output "ruleset_id"           { value = module.repo.ruleset_id }
output "environments"         { value = module.repo.environments }
`)
	return os.WriteFile(filepath.Join(stateDir, "main.tf"), []byte(sb.String()), 0o600)
}

// ----------------------------------------------------------------------
// Secrets handling
// ----------------------------------------------------------------------

func isValidGHSecretName(n string) bool {
	if !secretNameRe.MatchString(n) {
		return false
	}
	if strings.HasPrefix(n, "GITHUB_") {
		return false
	}
	return true
}

type secretEntry struct {
	Name  string
	Value string
}

// parseSecretsFile reads a tfvars-style file of `KEY = "value"` lines.
// Comments (# and //) and blank lines are ignored. Values may be double- or
// single-quoted; escape sequences \\ \" \n \r \t are recognized inside
// double-quoted values only.
func parseSecretsFile(path string) ([]secretEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("secrets file not found: %s", path)
		}
		return nil, fmt.Errorf("secrets file not readable: %s: %w", path, err)
	}
	defer f.Close()

	var out []secretEntry
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineno := 0
	for sc.Scan() {
		lineno++
		line := strings.TrimRight(sc.Text(), "\r")
		line = strings.TrimLeft(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		m := tfvarsLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s:%d: cannot parse line (expected NAME = \"value\"): %s", path, lineno, line)
		}
		name := m[1]
		rest := strings.TrimRight(m[2], " \t")

		var val string
		switch {
		case len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"':
			inner := rest[1 : len(rest)-1]
			decoded, err := decodeDoubleQuoted(inner)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, lineno, err)
			}
			val = decoded
		case len(rest) >= 2 && rest[0] == '\'' && rest[len(rest)-1] == '\'':
			val = rest[1 : len(rest)-1]
		default:
			return nil, fmt.Errorf("%s:%d: value must be a single quoted string", path, lineno)
		}

		if !isValidGHSecretName(name) {
			return nil, fmt.Errorf("%s:%d: invalid GitHub secret name %q (alphanumerics + underscore, no leading digit, no GITHUB_ prefix)", path, lineno, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate secret %q in %s", name, path)
		}
		seen[name] = true
		out = append(out, secretEntry{Name: name, Value: val})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no secrets found", path)
	}
	return out, nil
}

// decodeDoubleQuoted decodes the limited set of escapes recognized by the
// original bash parser: \\ \" \n \r \t. Other backslash sequences are left
// as-is (matching bash behavior, which only substituted those five forms).
func decodeDoubleQuoted(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			b.WriteByte('\\')
			continue
		}
		nxt := s[i+1]
		switch nxt {
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
			b.WriteByte('\\')
			b.WriteByte(nxt)
		}
		i++
	}
	return b.String(), nil
}

// makeSecretsTmpDir creates a chmod-700 temp dir, preferring /dev/shm on Linux
// so plaintext values never touch persistent disk.
func makeSecretsTmpDir() (string, error) {
	if runtime.GOOS == "linux" {
		if st, err := os.Stat("/dev/shm"); err == nil && st.IsDir() {
			if dir, err := os.MkdirTemp("/dev/shm", "gh-repo-bootstrap.*"); err == nil {
				_ = os.Chmod(dir, 0o700)
				return dir, nil
			}
		}
	}
	dir, err := os.MkdirTemp("", "gh-repo-bootstrap.*")
	if err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	return dir, nil
}

// generateSecrets writes secrets.tf into stateDir and secrets.auto.tfvars into
// secretsTmpDir. Returns the path to the var-file if any secrets were emitted,
// or "" otherwise.
func generateSecrets(stateDir, secretsTmpDir string, opts *options) (string, error) {
	secretsTF := filepath.Join(stateDir, "secrets.tf")
	secretsTFVars := filepath.Join(secretsTmpDir, "secrets.auto.tfvars")

	// Always remove any prior secrets.tf so stale resources don't persist.
	_ = os.Remove(secretsTF)

	var tfBuf strings.Builder
	var varsBuf strings.Builder
	any := false

	if opts.repoSecretsFile != "" {
		entries, err := parseSecretsFile(opts.repoSecretsFile)
		if err != nil {
			return "", err
		}
		tfBuf.WriteString("# Generated by gh repo-bootstrap. Repo-level Actions secrets.\n")
		for i, s := range entries {
			fmt.Fprintf(&tfBuf, "\nvariable \"rs_%d\" {\n  type      = string\n  sensitive = true\n}\n", i)
			fmt.Fprintf(&tfBuf, "resource \"github_actions_secret\" \"rs_%d\" {\n", i)
			tfBuf.WriteString("  repository      = module.repo.repository_name\n")
			fmt.Fprintf(&tfBuf, "  secret_name     = %s\n", hclString(s.Name))
			fmt.Fprintf(&tfBuf, "  plaintext_value = var.rs_%d\n}\n", i)
			fmt.Fprintf(&varsBuf, "rs_%d = %s\n", i, hclString(s.Value))
		}
		any = true
	}

	if opts.envSecretsDir != "" {
		st, err := os.Stat(opts.envSecretsDir)
		if err != nil || !st.IsDir() {
			return "", fmt.Errorf("env-secrets dir not found: %s", opts.envSecretsDir)
		}
		envSet := map[string]bool{}
		for _, e := range opts.envs {
			envSet[e] = true
		}
		matches, err := filepath.Glob(filepath.Join(opts.envSecretsDir, "*.tfvars"))
		if err != nil {
			return "", fmt.Errorf("scan env-secrets dir: %w", err)
		}
		if len(matches) == 0 {
			return "", fmt.Errorf("no *.tfvars files in %s", opts.envSecretsDir)
		}
		// filepath.Glob returns lexically sorted results; preserve that order
		// so resource indices stay stable across runs.
		tfBuf.WriteString("\n# Generated by gh repo-bootstrap. Env-level Actions secrets.\n")
		for envIdx, ef := range matches {
			base := strings.TrimSuffix(filepath.Base(ef), ".tfvars")
			if !envNameRe.MatchString(base) {
				return "", fmt.Errorf("invalid env name derived from filename: %s (basename must match [A-Za-z][A-Za-z0-9_-]*)", ef)
			}
			if !envSet[base] {
				return "", fmt.Errorf("env-secrets file %q targets env %q which is not in --env list (%s)", ef, base, strings.Join(opts.envs, " "))
			}
			entries, err := parseSecretsFile(ef)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&tfBuf, "\n# env: %s   (source: %s)\n", base, ef)
			for i, s := range entries {
				fmt.Fprintf(&tfBuf, "variable \"es_%d_%d\" {\n  type      = string\n  sensitive = true\n}\n", envIdx, i)
				fmt.Fprintf(&tfBuf, "resource \"github_actions_environment_secret\" \"es_%d_%d\" {\n", envIdx, i)
				tfBuf.WriteString("  repository      = module.repo.repository_name\n")
				fmt.Fprintf(&tfBuf, "  environment     = module.repo.environments_by_name[%s].environment\n", hclString(base))
				fmt.Fprintf(&tfBuf, "  secret_name     = %s\n", hclString(s.Name))
				fmt.Fprintf(&tfBuf, "  plaintext_value = var.es_%d_%d\n}\n", envIdx, i)
				fmt.Fprintf(&varsBuf, "es_%d_%d = %s\n", envIdx, i, hclString(s.Value))
			}
		}
		any = true
	}

	if !any {
		return "", nil
	}

	if err := os.WriteFile(secretsTF, []byte(tfBuf.String()), 0o600); err != nil {
		return "", fmt.Errorf("write secrets.tf: %w", err)
	}
	if err := os.WriteFile(secretsTFVars, []byte(varsBuf.String()), 0o600); err != nil {
		return "", fmt.Errorf("write secrets.auto.tfvars: %w", err)
	}
	return secretsTFVars, nil
}

// ----------------------------------------------------------------------
// Auth + tofu invocation
// ----------------------------------------------------------------------

func ensureGitHubToken() (string, error) {
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		return v, nil
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", errors.New("no GITHUB_TOKEN set and `gh auth token` failed; run `gh auth login` first")
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", errors.New("`gh auth token` returned an empty token; run `gh auth login` first")
	}
	return tok, nil
}

// runTofu executes tofu in workDir with stdio attached. It forwards SIGINT to
// the child and waits for the child to exit before returning, so callers can
// safely defer cleanup of any temporary files used as -var-file inputs.
func runTofu(ctx context.Context, workDir, token string, args ...string) error {
	cmd := exec.Command("tofu", args...)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Inherit env, ensure GITHUB_TOKEN is set.
	env := os.Environ()
	env = append(env, "GITHUB_TOKEN="+token)
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tofu: %w", err)
	}

	// Forward signals to the child; do not exit the parent until child exits.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	for {
		select {
		case sig := <-sigCh:
			// Best-effort: forward to child. tofu handles SIGINT cleanly.
			_ = cmd.Process.Signal(sig)
		case <-ctx.Done():
			_ = cmd.Process.Signal(os.Interrupt)
		case err := <-done:
			return err
		}
	}
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, errHelp) {
			usage()
			return
		}
		errf("%v", err)
		os.Exit(1)
	}
}

func run() error {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		if !errors.Is(err, errHelp) {
			usage()
		}
		return err
	}

	// Tooling check.
	if _, err := exec.LookPath("tofu"); err != nil {
		return errors.New("OpenTofu (`tofu`) is required but not on PATH. Install: https://opentofu.org/docs/intro/install/")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("the GitHub CLI (`gh`) is required but not on PATH")
	}

	if opts.stateDir == "" {
		base, err := defaultStateDir()
		if err != nil {
			return fmt.Errorf("compute default state dir: %w", err)
		}
		opts.stateDir = filepath.Join(base, opts.owner+"__"+opts.name)
	}
	if err := os.MkdirAll(opts.stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", opts.stateDir, err)
	}
	// Best-effort tightening on already-existing dirs (no-op on Windows).
	_ = os.Chmod(opts.stateDir, 0o700)

	// Extract bundled module fresh each run so updates propagate and stale
	// files from prior versions are removed.
	moduleDir := filepath.Join(opts.stateDir, ".module")
	if err := extractModule(moduleDir); err != nil {
		return err
	}

	if err := writeMainTF(opts.stateDir, moduleDir, opts); err != nil {
		return fmt.Errorf("write main.tf: %w", err)
	}

	// Secrets: temp dir first (so we can defer cleanup before any failure
	// path that might have written values).
	secretsTmpDir, err := makeSecretsTmpDir()
	if err != nil {
		return fmt.Errorf("create secrets temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(secretsTmpDir) }()

	varFile, err := generateSecrets(opts.stateDir, secretsTmpDir, opts)
	if err != nil {
		return err
	}

	token, err := ensureGitHubToken()
	if err != nil {
		return err
	}

	fmt.Printf(">>> Working directory: %s\n", opts.stateDir)

	ctx, cancel := signalContext()
	defer cancel()

	if err := runTofu(ctx, opts.stateDir, token, "init", "-input=false", "-upgrade"); err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}

	var tofuArgs []string
	switch opts.action {
	case "apply":
		tofuArgs = []string{"apply", "-input=false", "-auto-approve"}
	case "plan":
		tofuArgs = []string{"plan", "-input=false"}
	case "destroy":
		tofuArgs = []string{"destroy", "-input=false", "-auto-approve"}
	default:
		return fmt.Errorf("internal error: unknown action %q", opts.action)
	}
	if varFile != "" {
		tofuArgs = append(tofuArgs, "-var-file="+varFile)
	}
	if err := runTofu(ctx, opts.stateDir, token, tofuArgs...); err != nil {
		return fmt.Errorf("tofu %s: %w", opts.action, err)
	}
	return nil
}

// signalContext returns a context cancelled on the first os.Interrupt. The
// child-process forwarding in runTofu handles the actual signal propagation;
// this context is mainly here for future use and clean cancellation of any
// non-tofu work.
func signalContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(ch)
	}()
	return ctx, cancel
}
