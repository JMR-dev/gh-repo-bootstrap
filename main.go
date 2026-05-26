// gh-repo-bootstrap applies a standard branch-protection ruleset, a set of
// deployment environments, and optional GitHub Actions secrets to an existing
// GitHub repository, using Pulumi as the configuration engine.
//
// Installed as a `gh` extension and invoked as:
//
//	gh repo-bootstrap <owner/repo> [options]
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/JMR-dev/gh-repo-bootstrap/internal/cli"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/runner"
)

func main() {
	opts, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gh-repo-bootstrap:", err)
		cli.PrintUsage()
		os.Exit(1)
	}
	if opts == nil {
		return // --help
	}
	if err := runner.Run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "gh-repo-bootstrap:", err)
		os.Exit(1)
	}
}
