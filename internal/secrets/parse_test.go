package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseFile_basics(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "s.tfvars", `
# a comment
// another
API_TOKEN = "ghp_xyz"
WEBHOOK   = 'raw\nvalue'
MULTI     = "line1\nline2\twith\\backslash and \"quote\""
`)
	got, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	want := []Pair{
		{"API_TOKEN", "ghp_xyz"},
		{"WEBHOOK", `raw\nvalue`}, // single-quoted is raw
		{"MULTI", "line1\nline2\twith\\backslash and \"quote\""},
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %#v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %#v want %#v", i, got[i], want[i])
		}
	}
}

func TestParseFile_errors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, body, want string
	}{
		{"bad-name", `1FOO = "x"`, "cannot parse"},
		{"github-prefix", `GITHUB_TOKEN = "x"`, "invalid GitHub secret name"},
		{"unquoted", `FOO = bar`, "value must be a single quoted string"},
		{"duplicate", "FOO = \"a\"\nFOO = \"b\"\n", "duplicate secret"},
		{"empty", "# nothing\n", "no secrets found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, dir, tc.name+".tfvars", tc.body)
			_, err := ParseFile(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want contains %q", err, tc.want)
			}
		})
	}
}

func TestLoadEnvDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "production.tfvars", `DB = "prodpw"`+"\n")
	writeFile(t, dir, "staging.tfvars", `DB = "stagepw"`+"\n")
	set := map[string]struct{}{"production": {}, "staging": {}}
	envs, err := LoadEnvDir(dir, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 || envs[0].Env != "production" || envs[1].Env != "staging" {
		t.Fatalf("envs=%#v", envs)
	}
}

func TestLoadEnvDir_unknownEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "qa.tfvars", `DB = "x"`+"\n")
	_, err := LoadEnvDir(dir, map[string]struct{}{"production": {}})
	if err == nil || !strings.Contains(err.Error(), "not in --env list") {
		t.Fatalf("err=%v", err)
	}
}

func TestIsValidGitHubSecretName(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"OK_NAME_1", true},
		{"_underscore", true},
		{"1bad", false},
		{"GITHUB_TOKEN", false},
		{"has space", false},
		{"", false},
	} {
		if got := IsValidGitHubSecretName(c.in); got != c.ok {
			t.Errorf("%q: got %v want %v", c.in, got, c.ok)
		}
	}
}
