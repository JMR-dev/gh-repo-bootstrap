package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHCLString(t *testing.T) {
	cases := map[string]string{
		`hello`:           `"hello"`,
		`with "quote"`:    `"with \"quote\""`,
		"with\nnewline":   `"with\nnewline"`,
		`back\slash`:      `"back\\slash"`,
		`tab	here`:        `"tab\there"`,
		`${interp}`:       `"${interp}"`,
		`C:\Users\me\dir`: `"C:\\Users\\me\\dir"`,
	}
	for in, want := range cases {
		if got := hclString(in); got != want {
			t.Errorf("hclString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHCLStringList(t *testing.T) {
	if got := hclStringList(nil); got != "[]" {
		t.Errorf("nil list: %s", got)
	}
	got := hclStringList([]string{"a", "weird\"name", "ok"})
	want := `["a", "weird\"name", "ok"]`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestHCLBypassList(t *testing.T) {
	got := hclBypassList([]bypassActor{
		{ActorType: "RepositoryRole", ActorID: 5, BypassMode: "always"},
		{ActorType: "Team", ActorID: 42, BypassMode: "pull_request"},
	})
	want := `[{ actor_id = 5, actor_type = "RepositoryRole", bypass_mode = "always" }, { actor_id = 42, actor_type = "Team", bypass_mode = "pull_request" }]`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestParseBypassSpec(t *testing.T) {
	good := []struct {
		in   string
		want bypassActor
	}{
		{"RepositoryRole:5", bypassActor{"RepositoryRole", 5, "always"}},
		{"Team:42:pull_request", bypassActor{"Team", 42, "pull_request"}},
		{"Integration:1:always", bypassActor{"Integration", 1, "always"}},
	}
	for _, c := range good {
		got, err := parseBypassSpec(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseBypassSpec(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	bad := []string{"", "Team", "Team:abc", "Bogus:1", "Team:1:weird", "Team:-1"}
	for _, in := range bad {
		if _, err := parseBypassSpec(in); err == nil {
			t.Errorf("parseBypassSpec(%q) expected error", in)
		}
	}
}

func TestIsValidGHSecretName(t *testing.T) {
	good := []string{"FOO", "foo_bar", "_X", "A1"}
	bad := []string{"", "1FOO", "GITHUB_TOKEN", "with space", "dash-name", "GITHUB_X"}
	for _, n := range good {
		if !isValidGHSecretName(n) {
			t.Errorf("expected %q valid", n)
		}
	}
	for _, n := range bad {
		if isValidGHSecretName(n) {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestDecodeDoubleQuoted(t *testing.T) {
	cases := map[string]string{
		``:                ``,
		`hello`:           `hello`,
		`a\nb`:            "a\nb",
		`a\rb\tc`:         "a\rb\tc",
		`back\\slash`:     `back\slash`,
		`q\"x`:            `q"x`,
		`unknown\zescape`: `unknown\zescape`,
		`trailing\`:       `trailing\`,
	}
	for in, want := range cases {
		got, err := decodeDoubleQuoted(in)
		if err != nil || got != want {
			t.Errorf("decode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestParseSecretsFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.tfvars")
	os.WriteFile(good, []byte(`# header comment
// other style

API_TOKEN     = "abc123"
WEBHOOK = "with \"quotes\" and \n newline"
SINGLE  = 'no escapes here \n'
`), 0o600)
	entries, err := parseSecretsFile(good)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	if entries[0] != (secretEntry{"API_TOKEN", "abc123"}) {
		t.Errorf("entry0 = %+v", entries[0])
	}
	if entries[1].Value != "with \"quotes\" and \n newline" {
		t.Errorf("entry1 value = %q", entries[1].Value)
	}
	if entries[2].Value != `no escapes here \n` {
		t.Errorf("entry2 value = %q (single quotes don't decode escapes)", entries[2].Value)
	}

	// Duplicate
	dup := filepath.Join(dir, "dup.tfvars")
	os.WriteFile(dup, []byte("X=\"a\"\nX=\"b\"\n"), 0o600)
	if _, err := parseSecretsFile(dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("dup err: %v", err)
	}

	// Bad name
	badname := filepath.Join(dir, "bad.tfvars")
	os.WriteFile(badname, []byte("GITHUB_TOKEN = \"x\"\n"), 0o600)
	if _, err := parseSecretsFile(badname); err == nil {
		t.Errorf("expected reserved-name error")
	}

	// Empty
	empty := filepath.Join(dir, "empty.tfvars")
	os.WriteFile(empty, []byte("# only comments\n\n"), 0o600)
	if _, err := parseSecretsFile(empty); err == nil {
		t.Errorf("expected empty-file error")
	}

	// Unparseable line
	junk := filepath.Join(dir, "junk.tfvars")
	os.WriteFile(junk, []byte("not a valid line\n"), 0o600)
	if _, err := parseSecretsFile(junk); err == nil {
		t.Errorf("expected parse error")
	}

	// Missing file
	if _, err := parseSecretsFile(filepath.Join(dir, "nope.tfvars")); err == nil {
		t.Errorf("expected not-found error")
	}
}

func TestParseArgsBasics(t *testing.T) {
	opts, err := parseArgs([]string{"owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.owner != "owner" || opts.name != "repo" || opts.action != "apply" || len(opts.envs) != 1 || opts.envs[0] != "production" {
		t.Errorf("defaults wrong: %+v", opts)
	}

	opts, err = parseArgs([]string{
		"o/r", "--branch", "trunk", "--reviews", "3", "--signed",
		"--env", "production", "--env", "staging", "--solo", "--plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.branch != "trunk" || opts.reviews != 3 || !opts.signed || opts.action != "plan" {
		t.Errorf("opts wrong: %+v", opts)
	}
	if len(opts.envs) != 2 || opts.envs[1] != "staging" {
		t.Errorf("envs wrong: %v", opts.envs)
	}
	if len(opts.bypass) != 1 || opts.bypass[0].ActorType != "RepositoryRole" || opts.bypass[0].ActorID != 5 {
		t.Errorf("solo bypass wrong: %+v", opts.bypass)
	}

	bad := [][]string{
		{},
		{"no-slash"},
		{"o/r", "--reviews", "abc"},
		{"o/r", "--branch"}, // missing value
		{"o/r", "--unknown"},
		{"o/r", "extra"},
		{"o/r", "--bypass", "Bogus:1"},
	}
	for _, args := range bad {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}

func TestExtractModuleAndWriteMainTF(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, ".module")
	if err := extractModule(mod); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, want := range []string{"main.tf", "variables.tf", "outputs.tf", "versions.tf"} {
		if _, err := os.Stat(filepath.Join(mod, want)); err != nil {
			t.Errorf("missing embedded file %s: %v", want, err)
		}
	}

	// Stale-file removal: drop a junk file then re-extract.
	junk := filepath.Join(mod, "stale.tf")
	os.WriteFile(junk, []byte("bogus"), 0o600)
	if err := extractModule(mod); err != nil {
		t.Fatalf("re-extract: %v", err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Errorf("stale file not removed: %v", err)
	}

	opts := &options{
		owner: "o", name: "r", branch: `weird"branch`, reviews: 2,
		signed: true, ruleset: "rs", envs: []string{"production", "staging"},
		bypass: []bypassActor{{"RepositoryRole", 5, "always"}},
	}
	if err := writeMainTF(dir, mod, opts); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `default_branch         = "weird\"branch"`) {
		t.Errorf("branch not escaped:\n%s", s)
	}
	if !strings.Contains(s, `environments           = ["production", "staging"]`) {
		t.Errorf("envs missing:\n%s", s)
	}
	if !strings.Contains(s, `require_signed_commits = true`) {
		t.Errorf("signed missing")
	}
	if !strings.Contains(s, `actor_id = 5`) {
		t.Errorf("bypass missing")
	}
}

func TestGenerateSecrets(t *testing.T) {
	tmp := t.TempDir()
	state := filepath.Join(tmp, "state")
	os.MkdirAll(state, 0o700)
	tmpd := filepath.Join(tmp, "tmp")
	os.MkdirAll(tmpd, 0o700)

	repoSec := filepath.Join(tmp, "repo.tfvars")
	os.WriteFile(repoSec, []byte("API = \"abc\"\nTOKEN=\"xyz\"\n"), 0o600)

	envDir := filepath.Join(tmp, "envs")
	os.MkdirAll(envDir, 0o700)
	os.WriteFile(filepath.Join(envDir, "production.tfvars"), []byte("DB = \"prod\"\n"), 0o600)
	os.WriteFile(filepath.Join(envDir, "staging.tfvars"), []byte("DB = \"stage\"\n"), 0o600)

	opts := &options{
		repoSecretsFile: repoSec,
		envSecretsDir:   envDir,
		envs:            []string{"production", "staging"},
	}
	vf, err := generateSecrets(state, tmpd, opts)
	if err != nil {
		t.Fatal(err)
	}
	if vf == "" {
		t.Fatal("expected var-file path")
	}
	tfb, _ := os.ReadFile(filepath.Join(state, "secrets.tf"))
	tf := string(tfb)
	if !strings.Contains(tf, `secret_name     = "API"`) || !strings.Contains(tf, `secret_name     = "TOKEN"`) {
		t.Errorf("repo secrets missing: %s", tf)
	}
	if !strings.Contains(tf, `module.repo.environments_by_name["production"]`) || !strings.Contains(tf, `module.repo.environments_by_name["staging"]`) {
		t.Errorf("env secrets missing: %s", tf)
	}
	vfb, _ := os.ReadFile(vf)
	v := string(vfb)
	if !strings.Contains(v, `rs_0 = "abc"`) || !strings.Contains(v, `rs_1 = "xyz"`) {
		t.Errorf("repo tfvars missing: %s", v)
	}

	// Env not in --env list -> error
	os.WriteFile(filepath.Join(envDir, "rogue.tfvars"), []byte("X = \"y\"\n"), 0o600)
	if _, err := generateSecrets(state, tmpd, opts); err == nil {
		t.Errorf("expected rogue-env error")
	}
}
