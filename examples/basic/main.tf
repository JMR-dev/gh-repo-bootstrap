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
  owner = var.owner
}

variable "owner" {
  type    = string
  default = "JMR-dev"
}

variable "repo" {
  type = string
}

module "repo" {
  source = "../../modules/repo"

  repo_owner             = var.owner
  repo_name              = var.repo
  default_branch         = "main"
  required_reviews       = 1
  require_signed_commits = false
  environments           = ["production", "staging"]
}
