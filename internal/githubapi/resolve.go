// Package githubapi resolves human-friendly GitHub identifiers (logins,
// org/team slugs) into numeric IDs by shelling out to `gh api`. Numeric
// inputs short-circuit.
package githubapi

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Resolver caches user/team lookups for the lifetime of a single run.
type Resolver struct {
	mu    sync.Mutex
	users map[string]int
	teams map[string]int
}

// New returns a fresh Resolver.
func New() *Resolver {
	return &Resolver{users: map[string]int{}, teams: map[string]int{}}
}

// ResolveUser turns a numeric string or a GitHub login (with optional leading
// `@`) into a numeric user ID. Numeric inputs are passed through unchanged.
func (r *Resolver) ResolveUser(s string) (int, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	if id, err := strconv.Atoi(s); err == nil {
		return id, nil
	}
	r.mu.Lock()
	if id, ok := r.users[s]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()
	id, err := ghAPIID("users/" + s)
	if err != nil {
		return 0, fmt.Errorf("resolving user %q: %w", s, err)
	}
	r.mu.Lock()
	r.users[s] = id
	r.mu.Unlock()
	return id, nil
}

// ResolveTeam turns a numeric string or an `org/team-slug` pair into a
// numeric team ID. Bare slugs without an org prefix are rejected because the
// org cannot be inferred unambiguously.
func (r *Resolver) ResolveTeam(s string) (int, error) {
	s = strings.TrimSpace(s)
	if id, err := strconv.Atoi(s); err == nil {
		return id, nil
	}
	if !strings.Contains(s, "/") {
		return 0, fmt.Errorf("team reviewer %q must be in the form org/team-slug", s)
	}
	r.mu.Lock()
	if id, ok := r.teams[s]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, fmt.Errorf("team reviewer %q must be in the form org/team-slug", s)
	}
	id, err := ghAPIID(fmt.Sprintf("orgs/%s/teams/%s", parts[0], parts[1]))
	if err != nil {
		return 0, fmt.Errorf("resolving team %q: %w", s, err)
	}
	r.mu.Lock()
	r.teams[s] = id
	r.mu.Unlock()
	return id, nil
}

var ExecCommand = exec.Command

// ghAPIID runs `gh api <path>` and returns the `.id` field of the response.
func ghAPIID(path string) (int, error) {
	cmd := ExecCommand("gh", "api", path)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return 0, fmt.Errorf("gh api %s: %s", path, strings.TrimSpace(string(ee.Stderr)))
		}
		return 0, fmt.Errorf("gh api %s: %w", path, err)
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return 0, fmt.Errorf("decoding gh api %s response: %w", path, err)
	}
	if body.ID == 0 {
		return 0, fmt.Errorf("gh api %s returned no id", path)
	}
	return body.ID, nil
}
