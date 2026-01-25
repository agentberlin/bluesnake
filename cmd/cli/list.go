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

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
)

func runList(args []string) error {
	if len(args) < 1 {
		printListUsage()
		return fmt.Errorf("subcommand required: projects or crawls")
	}

	subcommand := args[0]

	switch subcommand {
	case "projects":
		return runListProjects(args[1:])
	case "crawls":
		return runListCrawls(args[1:])
	case "help", "-h", "--help":
		printListUsage()
		return nil
	default:
		printListUsage()
		return fmt.Errorf("unknown subcommand: %s", subcommand)
	}
}

func printListUsage() {
	fmt.Println(`Usage: bluesnake list <subcommand> [flags]

Subcommands:
  projects    List all projects
  crawls      List crawls for a project

Examples:
  # List all projects
  bluesnake list projects

  # List crawls for a project
  bluesnake list crawls --project-id 1`)
}

func runListProjects(args []string) error {
	fs := flag.NewFlagSet("list projects", flag.ExitOnError)

	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Println(`Usage: bluesnake list projects [flags]

List all crawled projects.

Flags:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Initialize store
	st, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}

	// Get all projects
	projects, err := st.GetAllProjects()
	if err != nil {
		return fmt.Errorf("failed to get projects: %v", err)
	}

	if len(projects) == 0 {
		fmt.Println("No projects found.")
		return nil
	}

	// Print header
	fmt.Printf("%-6s %-40s %-20s %-20s\n", "ID", "Domain", "Last Crawl", "Pages")
	fmt.Println("-------------------------------------------------------------------------------------------")

	for _, p := range projects {
		// Get latest crawl for this project
		emitter := &app.NoOpEmitter{}
		coreApp := app.NewApp(st, emitter)
		coreApp.Startup(context.Background())

		crawls, err := coreApp.GetCrawls(p.ID)
		lastCrawl := "Never"
		pagesCrawled := 0
		if err == nil && len(crawls) > 0 {
			lastCrawl = time.Unix(crawls[0].CrawlDateTime, 0).Format("2006-01-02 15:04")
			pagesCrawled = crawls[0].PagesCrawled
		}

		fmt.Printf("%-6d %-40s %-20s %-20d\n", p.ID, truncate(p.Domain, 40), lastCrawl, pagesCrawled)
	}

	return nil
}

func runListCrawls(args []string) error {
	fs := flag.NewFlagSet("list crawls", flag.ExitOnError)

	var projectID uint
	var jsonOutput bool
	fs.UintVar(&projectID, "project-id", 0, "Project ID (required)")
	fs.UintVar(&projectID, "p", 0, "Project ID (shorthand)")
	fs.BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Println(`Usage: bluesnake list crawls [flags]

List all crawls for a project.

Flags:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if projectID == 0 {
		fs.Usage()
		return fmt.Errorf("--project-id is required")
	}

	// Initialize store and app
	st, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}

	emitter := &app.NoOpEmitter{}
	coreApp := app.NewApp(st, emitter)
	coreApp.Startup(context.Background())

	// Get crawls for project
	crawls, err := coreApp.GetCrawls(projectID)
	if err != nil {
		return fmt.Errorf("failed to get crawls: %v", err)
	}

	if len(crawls) == 0 {
		fmt.Printf("No crawls found for project %d.\n", projectID)
		return nil
	}

	// Print header
	fmt.Printf("%-10s %-20s %-15s %-10s %-15s\n", "Crawl ID", "Date", "Duration", "Pages", "State")
	fmt.Println("-------------------------------------------------------------------------")

	for _, c := range crawls {
		crawlTime := time.Unix(c.CrawlDateTime, 0).Format("2006-01-02 15:04")
		duration := formatDuration(c.CrawlDuration)
		fmt.Printf("%-10d %-20s %-15s %-10d %-15s\n", c.ID, crawlTime, duration, c.PagesCrawled, c.State)
	}

	return nil
}

// truncate truncates a string to the specified length
func truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length-3] + "..."
}

// formatDuration formats a duration in milliseconds to a human-readable string
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	remainingSeconds := seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, remainingSeconds)
	}
	hours := minutes / 60
	remainingMinutes := minutes % 60
	return fmt.Sprintf("%dh %dm", hours, remainingMinutes)
}
