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
  --upload-repo-secrets FILE
                       Upload repository-level GitHub Actions secrets
                       sourced from a tfvars-style file. The file must
                       contain one assignment per line in the form:
                           SECRET_NAME = "value"
                       Names must match GitHub's rules (alphanumerics +
                       underscore, no leading digit, no GITHUB_ prefix).
                       Comments (#, //) and blank lines are allowed.
  --upload-env-secrets DIR
                       Upload environment-level GitHub Actions secrets.
                       DIR must contain one <env>.tfvars file per env,
                       where <env> matches one of the --env values.
                       Each file uses the same KEY = "value" syntax.
  --plan               Run `tofu plan` instead of `tofu apply`
  --destroy            Run `tofu destroy` (removes managed protection/envs)
  --state-dir DIR      Override working/state directory
                       (default: $XDG_STATE_HOME/gh-repo-bootstrap or
                                 ~/.local/state/gh-repo-bootstrap)
  -h, --help           Show this help

Authentication:
  GITHUB_TOKEN is auto-populated from `gh auth token` if not already set.

Secrets & state:
  Uploaded secret values are sent to GitHub encrypted, but they are
  ALSO stored in plaintext in the OpenTofu state file under the
  per-repo state directory. Protect that directory accordingly.

Examples:
  gh repo-bootstrap JMR-dev/my-app
  gh repo-bootstrap JMR-dev/api --reviews 2 --env production --env staging --signed
  gh repo-bootstrap JMR-dev/solo-project --solo
  gh repo-bootstrap JMR-dev/my-app --plan
  gh repo-bootstrap JMR-dev/my-app --upload-repo-secrets ./repo.secrets.tfvars
  gh repo-bootstrap JMR-dev/my-app --env production --env staging \
      --upload-env-secrets ./env-secrets/
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
REPO_SECRETS_FILE=""
ENV_SECRETS_DIR=""

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
    --upload-repo-secrets) REPO_SECRETS_FILE="$2"; shift 2 ;;
    --upload-env-secrets)  ENV_SECRETS_DIR="$2"; shift 2 ;;
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

# ----------------------------------------------------------------------
# Secrets handling (approach: codegen one resource + one sensitive
# variable per parsed key; values supplied via a tmpfs tfvars file).
# ----------------------------------------------------------------------

# Tempdir under /dev/shm if available so plaintext values never touch
# disk; chmod 700 either way; cleaned on exit.
if [[ -d /dev/shm && -w /dev/shm ]]; then
  SECRETS_TMPDIR="$(mktemp -d /dev/shm/gh-repo-bootstrap.XXXXXX)"
else
  SECRETS_TMPDIR="$(mktemp -d)"
fi
chmod 700 "$SECRETS_TMPDIR"
cleanup_secrets() {
  if [[ -n "${SECRETS_TMPDIR:-}" && -d "$SECRETS_TMPDIR" ]]; then
    rm -rf -- "$SECRETS_TMPDIR"
  fi
}
trap cleanup_secrets EXIT INT TERM

SECRETS_TF="$STATE_DIR/secrets.tf"
SECRETS_TFVARS="$SECRETS_TMPDIR/secrets.auto.tfvars"
TOFU_VAR_FILES=()
: > "$SECRETS_TF"
: > "$SECRETS_TFVARS"
chmod 600 "$SECRETS_TFVARS"

is_valid_gh_secret_name() {
  # GitHub Actions secret names: alphanumerics + underscore, must not
  # start with a digit, must not start with GITHUB_ (reserved).
  local n="$1"
  [[ "$n" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || return 1
  [[ "$n" != GITHUB_* ]] || return 1
  return 0
}

# Append an HCL string literal of $1 to file $2, properly escaping
# backslashes, double quotes, newlines, carriage returns, and tabs.
emit_hcl_string() {
  local s="$1" out="$2"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/\\r}"
  s="${s//$'\t'/\\t}"
  printf '"%s"' "$s" >>"$out"
}

# Parse a tfvars-style file of `KEY = "value"` lines into two parallel
# arrays in the caller's scope: __names and __values. Comments (#, //)
# and blank lines are ignored. Values may be double- or single-quoted;
# escape sequences \\ \" \n \r \t are recognized inside double quotes.
parse_secrets_file() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    err "secrets file not found: $file"; exit 1
  fi
  if [[ ! -r "$file" ]]; then
    err "secrets file not readable: $file"; exit 1
  fi
  __names=()
  __values=()
  local line lineno=0 name rest val
  while IFS= read -r line || [[ -n "$line" ]]; do
    lineno=$((lineno + 1))
    # Strip CR (Windows line endings).
    line="${line%$'\r'}"
    # Trim leading whitespace.
    line="${line#"${line%%[![:space:]]*}"}"
    # Skip blanks and comments.
    [[ -z "$line" ]] && continue
    [[ "$line" == \#* || "$line" == //* ]] && continue
    if [[ ! "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*(.*)$ ]]; then
      err "$file:$lineno: cannot parse line (expected NAME = \"value\"): $line"
      exit 1
    fi
    name="${BASH_REMATCH[1]}"
    rest="${BASH_REMATCH[2]}"
    # Strip trailing inline comment outside of quotes is hard; instead,
    # only strip whole-line trailing whitespace and require the value
    # itself to be a single quoted string.
    rest="${rest%"${rest##*[![:space:]]}"}"
    if [[ "$rest" =~ ^\"(.*)\"$ ]]; then
      val="${BASH_REMATCH[1]}"
      # Decode common escapes inside double-quoted form.
      val="${val//\\\\/$'\x01'}"   # placeholder for literal backslash
      val="${val//\\\"/\"}"
      val="${val//\\n/$'\n'}"
      val="${val//\\r/$'\r'}"
      val="${val//\\t/$'\t'}"
      val="${val//$'\x01'/\\}"
    elif [[ "$rest" =~ ^\'(.*)\'$ ]]; then
      val="${BASH_REMATCH[1]}"
    else
      err "$file:$lineno: value must be a single quoted string"
      exit 1
    fi
    if ! is_valid_gh_secret_name "$name"; then
      err "$file:$lineno: invalid GitHub secret name '$name' (alphanumerics + underscore, no leading digit, no GITHUB_ prefix)"
      exit 1
    fi
    __names+=("$name")
    __values+=("$val")
  done <"$file"
  if [[ ${#__names[@]} -eq 0 ]]; then
    err "$file: no secrets found"
    exit 1
  fi
}

# --- Repo-level secrets ---------------------------------------------------
if [[ -n "$REPO_SECRETS_FILE" ]]; then
  __names=(); __values=()
  parse_secrets_file "$REPO_SECRETS_FILE"
  # Detect duplicate names within this file.
  declare -A __seen_repo=()
  for n in "${__names[@]}"; do
    if [[ -n "${__seen_repo[$n]:-}" ]]; then
      err "duplicate repo secret '$n' in $REPO_SECRETS_FILE"; exit 1
    fi
    __seen_repo[$n]=1
  done
  {
    printf '# Generated by gh repo-bootstrap. Repo-level Actions secrets.\n'
    for i in "${!__names[@]}"; do
      printf '\nvariable "rs_%d" {\n  type      = string\n  sensitive = true\n}\n' "$i"
      printf 'resource "github_actions_secret" "rs_%d" {\n' "$i"
      printf '  repository      = module.repo.repository_name\n'
      printf '  secret_name     = "%s"\n' "${__names[$i]}"
      printf '  plaintext_value = var.rs_%d\n' "$i"
      printf '}\n'
    done
  } >>"$SECRETS_TF"
  for i in "${!__values[@]}"; do
    printf 'rs_%d = ' "$i" >>"$SECRETS_TFVARS"
    emit_hcl_string "${__values[$i]}" "$SECRETS_TFVARS"
    printf '\n' >>"$SECRETS_TFVARS"
  done
  unset __names __values __seen_repo
fi

# --- Environment-level secrets -------------------------------------------
if [[ -n "$ENV_SECRETS_DIR" ]]; then
  if [[ ! -d "$ENV_SECRETS_DIR" ]]; then
    err "env-secrets dir not found: $ENV_SECRETS_DIR"; exit 1
  fi
  # Build a lookup of declared envs.
  declare -A __env_set=()
  for e in "${ENVS[@]}"; do __env_set[$e]=1; done

  shopt -s nullglob
  env_files=("$ENV_SECRETS_DIR"/*.tfvars)
  shopt -u nullglob
  if [[ ${#env_files[@]} -eq 0 ]]; then
    err "no *.tfvars files in $ENV_SECRETS_DIR"; exit 1
  fi

  printf '\n# Generated by gh repo-bootstrap. Env-level Actions secrets.\n' >>"$SECRETS_TF"

  env_idx=0
  for ef in "${env_files[@]}"; do
    base="$(basename "$ef" .tfvars)"
    if [[ ! "$base" =~ ^[A-Za-z][A-Za-z0-9_-]*$ ]]; then
      err "invalid env name derived from filename: $ef (basename must match [A-Za-z][A-Za-z0-9_-]*)"
      exit 1
    fi
    if [[ -z "${__env_set[$base]:-}" ]]; then
      err "env-secrets file '$ef' targets env '$base' which is not in --env list (${ENVS[*]})"
      exit 1
    fi
    __names=(); __values=()
    parse_secrets_file "$ef"
    declare -A __seen_env=()
    for n in "${__names[@]}"; do
      if [[ -n "${__seen_env[$n]:-}" ]]; then
        err "duplicate secret '$n' in $ef"; exit 1
      fi
      __seen_env[$n]=1
    done
    {
      printf '\n# env: %s   (source: %s)\n' "$base" "$ef"
      for i in "${!__names[@]}"; do
        printf 'variable "es_%d_%d" {\n  type      = string\n  sensitive = true\n}\n' "$env_idx" "$i"
        printf 'resource "github_actions_environment_secret" "es_%d_%d" {\n' "$env_idx" "$i"
        printf '  repository      = module.repo.repository_name\n'
        printf '  environment     = module.repo.environments_by_name["%s"].environment\n' "$base"
        printf '  secret_name     = "%s"\n' "${__names[$i]}"
        printf '  plaintext_value = var.es_%d_%d\n' "$env_idx" "$i"
        printf '}\n'
      done
    } >>"$SECRETS_TF"
    for i in "${!__values[@]}"; do
      printf 'es_%d_%d = ' "$env_idx" "$i" >>"$SECRETS_TFVARS"
      emit_hcl_string "${__values[$i]}" "$SECRETS_TFVARS"
      printf '\n' >>"$SECRETS_TFVARS"
    done
    env_idx=$((env_idx + 1))
    unset __names __values __seen_env
  done
fi

# Only pass -var-file if we actually wrote any secret assignments.
if [[ -s "$SECRETS_TFVARS" ]]; then
  TOFU_VAR_FILES+=(-var-file="$SECRETS_TFVARS")
fi
# Remove an empty secrets.tf so tofu doesn't see stale resources from a
# previous run that included secrets.
if [[ ! -s "$SECRETS_TF" ]]; then
  rm -f "$SECRETS_TF"
fi


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
  apply)   tofu apply   -input=false -auto-approve "${TOFU_VAR_FILES[@]}" ;;
  plan)    tofu plan    -input=false              "${TOFU_VAR_FILES[@]}" ;;
  destroy) tofu destroy -input=false -auto-approve "${TOFU_VAR_FILES[@]}" ;;
esac
