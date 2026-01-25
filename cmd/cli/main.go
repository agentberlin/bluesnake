// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// BlueSnake CLI
//
// Command-line interface for BlueSnake web crawler. Provides programmatic access
// to crawling, exporting, and managing crawl data.
//
// Usage:
//
//	bluesnake <command> [flags]
//
// Commands:
//
//	crawl     Start a new crawl (or resume with --resume flag)
//	export    Export crawl results
//	list      List projects or crawls
//	version   Show version information
package main

import (
	"fmt"
	"os"

	"github.com/agentberlin/bluesnake/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "crawl":
		if err := runCrawl(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "export":
		if err := runExport(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "list":
		if err := runList(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Printf("BlueSnake CLI %s\n", version.CurrentVersion)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`BlueSnake CLI - Web crawler for SEO analysis

Usage:
  bluesnake <command> [flags]

Commands:
  crawl     Start a new crawl (or resume with --resume)
  export    Export crawl results to JSON or CSV
  list      List projects or crawls
  version   Show version information
  help      Show this help message

Examples:
  # Crawl a website
  bluesnake crawl https://example.com

  # Crawl with a URL limit (pauses when reached)
  bluesnake crawl https://example.com --max-urls 100

  # Resume a paused crawl
  bluesnake crawl --resume --project-id 1

  # Resume with additional URL budget
  bluesnake crawl --resume --project-id 1 --max-urls 50

  # Export a completed crawl
  bluesnake export --crawl-id 123 --format csv -o ./export

  # List all projects
  bluesnake list projects

  # List crawls for a project
  bluesnake list crawls --project-id 1

Use "bluesnake <command> --help" for more information about a command.`)
}
