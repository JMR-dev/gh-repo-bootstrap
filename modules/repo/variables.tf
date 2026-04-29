variable "repo_owner" {
  description = "GitHub user or organization that owns the repository."
  type        = string
}

variable "repo_name" {
  description = "Repository name (without owner prefix)."
  type        = string
}

variable "default_branch" {
  description = "Branch protected by the ruleset."
  type        = string
  default     = "main"
}

variable "required_reviews" {
  description = "Number of required approving reviews on PRs targeting the default branch."
  type        = number
  default     = 1
}

variable "require_signed_commits" {
  description = "Require signed commits on the protected branch."
  type        = bool
  default     = false
}

variable "environments" {
  description = "List of GitHub deployment environments to ensure exist."
  type        = list(string)
  default     = []
}

variable "ruleset_name" {
  description = "Name to give the branch protection ruleset."
  type        = string
  default     = "default-branch-protection"
}
