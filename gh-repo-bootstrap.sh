#!/usr/bin/env bash
# gh-repo-bootstrap: apply standard branch protection + environments to a
# GitHub repository, using the bundled OpenTofu `repo` module.
#
# Installed as a gh extension, invoked as:  gh repo-bootstrap <owner/repo> [opts]
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
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
                       Use this when you're the only maintainer and need
                       to merge your own PRs without a second approver.
  --plan               Run `tofu plan` instead of `tofu apply`
  --destroy            Run `tofu destroy` (removes managed protection/envs)
  --state-dir DIR      Override working/state directory
                       (default: $XDG_STATE_HOME/gh-repo-bootstrap or
                                 ~/.local/state/gh-repo-bootstrap)
  -h, --help           Show this help

Authentication:
  GITHUB_TOKEN is auto-populated from `gh auth token` if not already set.

Examples:
  gh repo-bootstrap JMR-dev/my-app
  gh repo-bootstrap JMR-dev/api --reviews 2 --env production --env staging --signed
  gh repo-bootstrap JMR-dev/solo-project --solo
  gh repo-bootstrap JMR-dev/my-app --plan
EOF
}

err() { echo "gh-repo-bootstrap: $*" >&2; }

REPO=""
BRANCH="main"
REVIEWS=1
SIGNED=false
RULESET="default-branch-protection"
ENVS=()
BYPASS=()
ACTION="apply"
STATE_DIR=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)    usage; exit 0 ;;
    --branch)     BRANCH="$2"; shift 2 ;;
    --reviews)    REVIEWS="$2"; shift 2 ;;
    --signed)     SIGNED=true; shift ;;
    --ruleset)    RULESET="$2"; shift 2 ;;
    --env)        ENVS+=("$2"); shift 2 ;;
    --bypass)     BYPASS+=("$2"); shift 2 ;;
    --solo)       BYPASS+=("RepositoryRole:5:always"); shift ;;
    --plan)       ACTION="plan"; shift ;;
    --destroy)    ACTION="destroy"; shift ;;
    --state-dir)  STATE_DIR="$2"; shift 2 ;;
    --)           shift; break ;;
    -*)           err "unknown option: $1"; usage; exit 1 ;;
    *)
      if [[ -z "$REPO" ]]; then
        REPO="$1"; shift
      else
        err "unexpected positional argument: $1"; exit 1
      fi
      ;;
  esac
done

if [[ -z "$REPO" || "$REPO" != */* ]]; then
  err "first argument must be <owner>/<repo>"
  usage; exit 1
fi
OWNER="${REPO%/*}"
NAME="${REPO#*/}"

if ! [[ "$REVIEWS" =~ ^[0-9]+$ ]]; then
  err "--reviews must be a non-negative integer (got: $REVIEWS)"
  exit 1
fi

if [[ ${#ENVS[@]} -eq 0 ]]; then
  ENVS=(production)
fi

# Locate the bundled module (repo root containing this script).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_DIR="$SCRIPT_DIR/modules/repo"
if [[ ! -f "$MODULE_DIR/main.tf" ]]; then
  err "could not locate bundled OpenTofu module at $MODULE_DIR"
  exit 1
fi

# Tooling check.
if ! command -v tofu >/dev/null 2>&1; then
  err "OpenTofu (\`tofu\`) is required but not on PATH. Install: https://opentofu.org/docs/intro/install/"
  exit 1
fi
if ! command -v gh >/dev/null 2>&1; then
  err "the GitHub CLI (\`gh\`) is required but not on PATH"
  exit 1
fi

# Per-repo state directory keeps each repo's tofu state isolated.
if [[ -z "$STATE_DIR" ]]; then
  STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}/gh-repo-bootstrap"
  STATE_DIR="$STATE_BASE/${OWNER}__${NAME}"
fi
mkdir -p "$STATE_DIR"

# Build HCL list literal for environments, with proper quoting.
envs_hcl="["
for e in "${ENVS[@]}"; do
  # Escape any embedded quotes/backslashes.
  esc=${e//\\/\\\\}
  esc=${esc//\"/\\\"}
  envs_hcl+="\"$esc\", "
done
envs_hcl="${envs_hcl%, }]"

# Build HCL list literal for bypass actors.
# Each spec: <actor_type>:<actor_id>[:<bypass_mode>]   (mode default: always)
bypass_hcl="["
for spec in "${BYPASS[@]}"; do
  IFS=':' read -r b_type b_id b_mode <<<"$spec"
  b_mode="${b_mode:-always}"
  if [[ -z "$b_type" || -z "$b_id" ]]; then
    err "invalid --bypass SPEC '$spec' (expected <actor_type>:<actor_id>[:<mode>])"
    exit 1
  fi
  if ! [[ "$b_id" =~ ^[0-9]+$ ]]; then
    err "invalid --bypass SPEC '$spec' (actor_id must be numeric)"
    exit 1
  fi
  case "$b_mode" in
    always|pull_request) ;;
    *) err "invalid --bypass mode '$b_mode' (must be 'always' or 'pull_request')"; exit 1 ;;
  esac
  case "$b_type" in
    RepositoryRole|Team|Integration|OrganizationAdmin|DeployKey) ;;
    *) err "invalid --bypass actor_type '$b_type'"; exit 1 ;;
  esac
  bypass_hcl+="{ actor_id = $b_id, actor_type = \"$b_type\", bypass_mode = \"$b_mode\" }, "
done
bypass_hcl="${bypass_hcl%, }]"

cat > "$STATE_DIR/main.tf" <<EOF
# Generated by gh repo-bootstrap. Edits will be overwritten.
terraform {
  required_version = ">= 1.8.0"
  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.2"
    }
  }
}

provider "github" {
  owner = "${OWNER}"
}

module "repo" {
  source = "${MODULE_DIR}"

  repo_owner             = "${OWNER}"
  repo_name              = "${NAME}"
  default_branch         = "${BRANCH}"
  required_reviews       = ${REVIEWS}
  require_signed_commits = ${SIGNED}
  ruleset_name           = "${RULESET}"
  environments           = ${envs_hcl}
  bypass_actors          = ${bypass_hcl}
}

output "repository_full_name" { value = module.repo.repository_full_name }
output "ruleset_id"           { value = module.repo.ruleset_id }
output "environments"         { value = module.repo.environments }
EOF

# Auth: prefer caller-supplied GITHUB_TOKEN, else borrow from gh.
if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  if ! GITHUB_TOKEN="$(gh auth token 2>/dev/null)"; then
    err "no GITHUB_TOKEN set and \`gh auth token\` failed; run \`gh auth login\` first"
    exit 1
  fi
  export GITHUB_TOKEN
fi

cd "$STATE_DIR"
echo ">>> Working directory: $STATE_DIR"
tofu init -input=false -upgrade

case "$ACTION" in
  apply)   tofu apply   -input=false -auto-approve ;;
  plan)    tofu plan    -input=false ;;
  destroy) tofu destroy -input=false -auto-approve ;;
esac
