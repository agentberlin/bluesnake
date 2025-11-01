package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools registers all MCP tools with the server
func (s *MCPServer) registerTools() {
	s.logger.Printf("Registering MCP tools...")

	// Core crawl management tools
	s.registerCrawlWebsiteTool()
	s.registerStopCrawlTool()
	s.registerGetCrawlStatusTool()

	// Result retrieval tools
	s.registerGetCrawlResultsTool()
	s.registerSearchCrawlResultsTool()
	s.registerGetCrawlStatisticsTool()

	// Link and content analysis tools
	s.registerGetPageLinksTool()
	s.registerGetPageContentTool()

	// Project management tools
	s.registerListProjectsTool()
	s.registerListProjectCrawlsTool()
	s.registerDeleteProjectTool()
	s.registerDeleteCrawlTool()

	// Configuration tools
	s.registerGetDomainConfigTool()
	s.registerUpdateDomainConfigTool()

	s.logger.Printf("All MCP tools registered successfully")
}

// CrawlWebsiteArgs defines the input schema for crawl_website tool
type CrawlWebsiteArgs struct {
	URL    string           `json:"url"`
	Config *CrawlConfigArgs `json:"config,omitempty"`
}

// CrawlConfigArgs defines the crawler configuration options
type CrawlConfigArgs struct {
	IncludeSubdomains      *bool    `json:"includeSubdomains,omitempty"`
	SinglePageMode         *bool    `json:"singlePageMode,omitempty"`
	JSRenderingEnabled     *bool    `json:"jsRenderingEnabled,omitempty"`
	Parallelism            *int     `json:"parallelism,omitempty"`
	DiscoveryMechanisms    []string `json:"discoveryMechanisms,omitempty"`
	RobotsTxtMode          *string  `json:"robotsTxtMode,omitempty"`
	CheckExternalResources *bool    `json:"checkExternalResources,omitempty"`
	InitialWaitMs          *int     `json:"initialWaitMs,omitempty"`
	ScrollWaitMs           *int     `json:"scrollWaitMs,omitempty"`
	UserAgent              *string  `json:"userAgent,omitempty"`
}

// CrawlWebsiteResult defines the output schema for crawl_website tool
type CrawlWebsiteResult struct {
	Success   bool   `json:"success"`
	ProjectID uint   `json:"projectId,omitempty"`
	CrawlID   uint   `json:"crawlId,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Message   string `json:"message"`
}

// registerCrawlWebsiteTool registers the crawl_website tool
func (s *MCPServer) registerCrawlWebsiteTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "crawl_website",
		Description: "Initiates a new web crawl for the specified URL with optional configuration",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args CrawlWebsiteArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: crawl_website for URL: %s", args.URL)

		// Apply configuration if provided
		if args.Config != nil {
			if err := s.applyConfig(args.URL, args.Config); err != nil {
				return nil, CrawlWebsiteResult{
					Success: false,
					Message: fmt.Sprintf("Failed to apply configuration: %v", err),
				}, nil
			}
		}

		// Extract domain from URL for response
		parsedURL, err := url.Parse(args.URL)
		if err != nil {
			return nil, CrawlWebsiteResult{
				Success: false,
				Message: fmt.Sprintf("Invalid URL: %v", err),
			}, nil
		}
		domain := parsedURL.Hostname()

		// Start the crawl
		err = s.app.StartCrawl(args.URL)
		if err != nil {
			return nil, CrawlWebsiteResult{
				Success: false,
				Message: fmt.Sprintf("Failed to start crawl: %v", err),
			}, nil
		}

		// Get the active crawl to retrieve project and crawl IDs
		activeCrawls := s.app.GetActiveCrawls()
		var crawlID uint
		var projectID uint

		for _, crawl := range activeCrawls {
			if crawl.Domain == domain {
				crawlID = crawl.CrawlID
				projectID = crawl.ProjectID
				domain = crawl.Domain
				break
			}
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Crawl started successfully for %s (Project ID: %d, Crawl ID: %d)", domain, projectID, crawlID),
				},
			},
		}, CrawlWebsiteResult{
			Success:   true,
			ProjectID: projectID,
			CrawlID:   crawlID,
			Domain:    domain,
			Message:   "Crawl started successfully",
		}, nil
	})
}

// applyConfig applies the provided configuration to the domain
func (s *MCPServer) applyConfig(urlStr string, config *CrawlConfigArgs) error {
	// Get the current config for the domain
	configResp, err := s.app.GetConfigForDomain(urlStr)
	if err != nil {
		return err
	}

	// Apply overrides
	if config.IncludeSubdomains != nil {
		configResp.IncludeSubdomains = *config.IncludeSubdomains
	}
	if config.SinglePageMode != nil {
		configResp.SinglePageMode = *config.SinglePageMode
	}
	if config.JSRenderingEnabled != nil {
		configResp.JSRenderingEnabled = *config.JSRenderingEnabled
	}
	if config.Parallelism != nil {
		configResp.Parallelism = *config.Parallelism
	}
	if config.RobotsTxtMode != nil {
		configResp.RobotsTxtMode = *config.RobotsTxtMode
	}
	if config.CheckExternalResources != nil {
		configResp.CheckExternalResources = *config.CheckExternalResources
	}
	if config.InitialWaitMs != nil {
		configResp.InitialWaitMs = *config.InitialWaitMs
	}
	if config.ScrollWaitMs != nil {
		configResp.ScrollWaitMs = *config.ScrollWaitMs
	}
	if config.UserAgent != nil {
		configResp.UserAgent = *config.UserAgent
	}
	var mechanisms []string
	if len(config.DiscoveryMechanisms) > 0 {
		mechanisms = config.DiscoveryMechanisms
	} else {
		mechanisms = configResp.DiscoveryMechanisms
	}

	// Determine spider and sitemap flags
	spiderEnabled := false
	sitemapEnabled := false
	for _, m := range mechanisms {
		if m == "spider" {
			spiderEnabled = true
		}
		if m == "sitemap" {
			sitemapEnabled = true
		}
	}

	// Update the config with all required parameters
	return s.app.UpdateConfigForDomain(
		urlStr,
		configResp.JSRenderingEnabled,
		configResp.InitialWaitMs,
		configResp.ScrollWaitMs,
		configResp.FinalWaitMs,
		configResp.Parallelism,
		configResp.UserAgent,
		configResp.IncludeSubdomains,
		spiderEnabled,
		sitemapEnabled,
		configResp.SitemapURLs,
		configResp.CheckExternalResources,
		configResp.SinglePageMode,
		configResp.RobotsTxtMode,
		configResp.FollowInternalNofollow,
		configResp.FollowExternalNofollow,
		configResp.RespectMetaRobotsNoindex,
		configResp.RespectNoindex,
	)
}

// StopCrawlArgs defines the input schema for stop_crawl tool
type StopCrawlArgs struct {
	ProjectID uint `json:"projectId"`
}

// StopCrawlResult defines the output schema for stop_crawl tool
type StopCrawlResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// registerStopCrawlTool registers the stop_crawl tool
func (s *MCPServer) registerStopCrawlTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "stop_crawl",
		Description: "Stops an active crawl gracefully",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args StopCrawlArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: stop_crawl for project ID: %d", args.ProjectID)

		err := s.app.StopCrawl(args.ProjectID)
		if err != nil {
			return nil, StopCrawlResult{
				Success: false,
				Message: fmt.Sprintf("Failed to stop crawl: %v", err),
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Crawl stopped successfully for project ID: %d", args.ProjectID),
				},
			},
		}, StopCrawlResult{
			Success: true,
			Message: "Crawl stopped successfully",
		}, nil
	})
}

// GetCrawlStatusArgs defines the input schema for get_crawl_status tool
type GetCrawlStatusArgs struct {
	ProjectID uint `json:"projectId"`
}

// registerGetCrawlStatusTool registers the get_crawl_status tool
func (s *MCPServer) registerGetCrawlStatusTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "get_crawl_status",
		Description: "Gets real-time progress information for an active or completed crawl",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCrawlStatusArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: get_crawl_status for project ID: %d", args.ProjectID)

		// First check active crawls
		activeCrawls := s.app.GetActiveCrawls()
		for _, crawl := range activeCrawls {
			if crawl.ProjectID == args.ProjectID {
				// Get detailed stats
				stats, err := s.app.GetActiveCrawlStats(args.ProjectID)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to get crawl stats: %w", err)
				}

				result := map[string]interface{}{
					"projectId":        crawl.ProjectID,
					"crawlId":          crawl.CrawlID,
					"domain":           crawl.Domain,
					"url":              crawl.URL,
					"isCrawling":       true,
					"pagesCrawled":     stats.HTML,
					"totalUrlsCrawled": stats.Crawled,
					"totalDiscovered":  stats.Total,
					"discoveredUrls":   crawl.DiscoveredURLs,
				}

				resultJSON, _ := json.MarshalIndent(result, "", "  ")
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("Active crawl status:\n%s", string(resultJSON)),
						},
					},
				}, result, nil
			}
		}

		// If not active, get the latest completed crawl
		crawls, err := s.app.GetCrawls(args.ProjectID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get crawls: %w", err)
		}

		if len(crawls) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: fmt.Sprintf("No crawls found for project ID: %d", args.ProjectID),
					},
				},
			}, map[string]interface{}{
				"projectId":  args.ProjectID,
				"isCrawling": false,
				"message":    "No crawls found for this project",
			}, nil
		}

		// Get the latest crawl
		latestCrawl := crawls[0]

		result := map[string]interface{}{
			"projectId":    args.ProjectID,
			"crawlId":      latestCrawl.ID,
			"isCrawling":   false,
			"pagesCrawled": latestCrawl.PagesCrawled,
		}

		resultJSON, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Completed crawl status:\n%s", string(resultJSON)),
				},
			},
		}, result, nil
	})
}

// GetCrawlResultsArgs defines the input schema for get_crawl_results tool
type GetCrawlResultsArgs struct {
	CrawlID     uint   `json:"crawlId"`
	ContentType string `json:"contentType,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Cursor      uint   `json:"cursor,omitempty"`
}

// registerGetCrawlResultsTool registers the get_crawl_results tool
func (s *MCPServer) registerGetCrawlResultsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "get_crawl_results",
		Description: "Retrieves paginated crawl results with optional filtering by content type",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCrawlResultsArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: get_crawl_results for crawl ID: %d", args.CrawlID)

		// Set defaults
		if args.ContentType == "" {
			args.ContentType = "all"
		}
		if args.Limit <= 0 || args.Limit > 1000 {
			args.Limit = 100
		}

		// Get results
		response, err := s.app.GetCrawlWithResultsPaginated(args.CrawlID, args.Limit, args.Cursor, args.ContentType)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get crawl results: %w", err)
		}

		resultJSON, _ := json.MarshalIndent(response, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Crawl results (showing %d results):\n%s", len(response.Results), string(resultJSON)),
				},
			},
		}, response, nil
	})
}

// SearchCrawlResultsArgs defines the input schema for search_crawl_results tool
type SearchCrawlResultsArgs struct {
	CrawlID     uint   `json:"crawlId"`
	Query       string `json:"query"`
	ContentType string `json:"contentType,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Cursor      uint   `json:"cursor,omitempty"`
}

// registerSearchCrawlResultsTool registers the search_crawl_results tool
func (s *MCPServer) registerSearchCrawlResultsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "search_crawl_results",
		Description: "Searches crawl results with pattern matching and filters",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SearchCrawlResultsArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: search_crawl_results for crawl ID: %d, query: %s", args.CrawlID, args.Query)

		// Set defaults
		if args.ContentType == "" {
			args.ContentType = "all"
		}
		if args.Limit <= 0 || args.Limit > 1000 {
			args.Limit = 100
		}

		// Search results
		response, err := s.app.SearchCrawlResultsPaginated(args.CrawlID, args.Query, args.ContentType, args.Limit, args.Cursor)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to search crawl results: %w", err)
		}

		resultJSON, _ := json.MarshalIndent(response, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Search results for '%s' (showing %d results):\n%s", args.Query, len(response.Results), string(resultJSON)),
				},
			},
		}, response, nil
	})
}

// GetCrawlStatisticsArgs defines the input schema for get_crawl_statistics tool
type GetCrawlStatisticsArgs struct {
	CrawlID uint `json:"crawlId"`
}

// registerGetCrawlStatisticsTool registers the get_crawl_statistics tool
func (s *MCPServer) registerGetCrawlStatisticsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "get_crawl_statistics",
		Description: "Gets detailed statistics for a crawl (content type breakdown indexability etc.)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCrawlStatisticsArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: get_crawl_statistics for crawl ID: %d", args.CrawlID)

		// Get statistics
		stats, err := s.app.GetCrawlStats(args.CrawlID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get crawl statistics: %w", err)
		}

		statsJSON, _ := json.MarshalIndent(stats, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Crawl statistics:\n%s", string(statsJSON)),
				},
			},
		}, stats, nil
	})
}
// GetPageLinksArgs defines the input schema for get_page_links tool
type GetPageLinksArgs struct {
	CrawlID uint   `json:"crawlId"`
	PageURL string `json:"pageUrl"`
}

// registerGetPageLinksTool registers the get_page_links tool
func (s *MCPServer) registerGetPageLinksTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "get_page_links",
		Description: "Retrieves the link graph for a specific page (inbound and outbound links)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetPageLinksArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: get_page_links for crawl ID: %d, URL: %s", args.CrawlID, args.PageURL)

		// Get page links
		links, err := s.app.GetPageLinksForURL(args.CrawlID, args.PageURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get page links: %w", err)
		}

		linksJSON, _ := json.MarshalIndent(links, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Links for %s:\n%s", args.PageURL, string(linksJSON)),
				},
			},
		}, links, nil
	})
}

// GetPageContentArgs defines the input schema for get_page_content tool
type GetPageContentArgs struct{
	CrawlID uint   `json:"crawlId"`
	PageURL string `json:"pageUrl"`
}

// registerGetPageContentTool registers the get_page_content tool
func (s *MCPServer) registerGetPageContentTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "get_page_content",
		Description: "Retrieves the extracted text content of a crawled page",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetPageContentArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: get_page_content for crawl ID: %d, URL: %s", args.CrawlID, args.PageURL)

		// Get page content
		content, err := s.app.GetPageContent(args.CrawlID, args.PageURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get page content: %w", err)
		}

		result := map[string]interface{}{
			"url":           args.PageURL,
			"content":       content,
			"contentLength": len(content),
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Content for %s (%d characters):\n\n%s", args.PageURL, len(content), content),
				},
			},
		}, result, nil
	})
}
// registerListProjectsTool registers the list_projects tool
func (s *MCPServer) registerListProjectsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "list_projects",
		Description: "Lists all projects (domains) with their latest crawl information",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: list_projects")

		// Get all projects
		projects, err := s.app.GetProjects()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list projects: %w", err)
		}

		result := map[string]interface{}{
			"projects": projects,
		}

		projectsJSON, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Found %d projects:\n%s", len(projects), string(projectsJSON)),
				},
			},
		}, result, nil
	})
}

// ListProjectCrawlsArgs defines the input schema for list_project_crawls tool
type ListProjectCrawlsArgs struct {
	ProjectID uint `json:"projectId"`
}

// registerListProjectCrawlsTool registers the list_project_crawls tool
func (s *MCPServer) registerListProjectCrawlsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "list_project_crawls",
		Description: "Lists all crawls for a specific project",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListProjectCrawlsArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: list_project_crawls for project ID: %d", args.ProjectID)

		// Get all crawls for the project
		crawls, err := s.app.GetCrawls(args.ProjectID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list project crawls: %w", err)
		}

		result := map[string]interface{}{
			"projectId": args.ProjectID,
			"crawls":    crawls,
		}

		crawlsJSON, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Found %d crawls for project %d:\n%s", len(crawls), args.ProjectID, string(crawlsJSON)),
				},
			},
		}, result, nil
	})
}

// DeleteProjectArgs defines the input schema for delete_project tool
type DeleteProjectArgs struct {
	ProjectID uint `json:"projectId"`
}

// registerDeleteProjectTool registers the delete_project tool
func (s *MCPServer) registerDeleteProjectTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "delete_project",
		Description: "Deletes a project and all associated crawls (CASCADE DELETE)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DeleteProjectArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: delete_project for project ID: %d", args.ProjectID)

		// Delete the project
		err := s.app.DeleteProjectByID(args.ProjectID)
		if err != nil {
			return nil, map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Failed to delete project: %v", err),
			}, nil
		}

		result := map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Project %d deleted successfully", args.ProjectID),
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: result["message"].(string),
				},
			},
		}, result, nil
	})
}

// DeleteCrawlArgs defines the input schema for delete_crawl tool
type DeleteCrawlArgs struct {
	CrawlID uint `json:"crawlId"`
}

// registerDeleteCrawlTool registers the delete_crawl tool
func (s *MCPServer) registerDeleteCrawlTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "delete_crawl",
		Description: "Deletes a specific crawl and all its results",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DeleteCrawlArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: delete_crawl for crawl ID: %d", args.CrawlID)

		// Delete the crawl
		err := s.app.DeleteCrawlByID(args.CrawlID)
		if err != nil {
			return nil, map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Failed to delete crawl: %v", err),
			}, nil
		}

		result := map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Crawl %d deleted successfully", args.CrawlID),
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: result["message"].(string),
				},
			},
		}, result, nil
	})
}
// GetDomainConfigArgs defines the input schema for get_domain_config tool
type GetDomainConfigArgs struct {
	Domain string `json:"domain"`
}

// registerGetDomainConfigTool registers the get_domain_config tool
func (s *MCPServer) registerGetDomainConfigTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "get_domain_config",
		Description: "Retrieves the crawler configuration for a specific domain",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetDomainConfigArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: get_domain_config for domain: %s", args.Domain)

		// Build URL from domain (add https:// if missing)
		urlStr := args.Domain
		if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
			urlStr = "https://" + urlStr
		}

		// Get config
		config, err := s.app.GetConfigForDomain(urlStr)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get domain config: %w", err)
		}

		configJSON, _ := json.MarshalIndent(config, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Configuration for %s:\n%s", args.Domain, string(configJSON)),
				},
			},
		}, config, nil
	})
}

// UpdateDomainConfigArgs defines the input schema for update_domain_config tool
type UpdateDomainConfigArgs struct {
	Domain string            `json:"domain"`
	Config *CrawlConfigArgs  `json:"config"`
}

// registerUpdateDomainConfigTool registers the update_domain_config tool
func (s *MCPServer) registerUpdateDomainConfigTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "update_domain_config",
		Description: "Updates the crawler configuration for a domain (applies to future crawls)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args UpdateDomainConfigArgs) (*mcp.CallToolResult, any, error) {
		s.logger.Printf("Tool called: update_domain_config for domain: %s", args.Domain)

		// Build URL from domain
		urlStr := args.Domain
		if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
			urlStr = "https://" + urlStr
		}

		// Apply the configuration
		if err := s.applyConfig(urlStr, args.Config); err != nil {
			return nil, map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Failed to update config: %v", err),
			}, nil
		}

		result := map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Configuration updated successfully for %s", args.Domain),
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: result["message"].(string),
				},
			},
		}, result, nil
	})
}
