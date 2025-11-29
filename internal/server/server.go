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

package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentberlin/bluesnake/internal/app"
)

// Server represents the HTTP server
type Server struct {
	app *app.App
	mux *http.ServeMux
}

// NewServer creates a new HTTP server
func NewServer(app *app.App) *Server {
	s := &Server{
		app: app,
		mux: http.NewServeMux(),
	}

	// Register routes
	s.registerRoutes()

	return s
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS middleware
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Logging middleware
	log.Printf("%s %s", r.Method, r.URL.Path)

	// Serve request
	s.mux.ServeHTTP(w, r)
}

// registerRoutes registers all HTTP routes
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/version", s.handleGetVersion)
	s.mux.HandleFunc("/api/v1/projects", s.handleProjects)
	s.mux.HandleFunc("/api/v1/projects/", s.handleProjectsWithID)
	s.mux.HandleFunc("/api/v1/crawls/", s.handleCrawls)
	s.mux.HandleFunc("/api/v1/crawl", s.handleStartCrawl)
	s.mux.HandleFunc("/api/v1/stop-crawl/", s.handleStopCrawl)
	s.mux.HandleFunc("/api/v1/active-crawls", s.handleActiveCrawls)
	s.mux.HandleFunc("/api/v1/config", s.handleConfig)
	s.mux.HandleFunc("/api/v1/search/", s.handleSearch)
}

// handleHealth returns server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// handleGetVersion returns the application version
func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version := s.app.GetVersion()
	json.NewEncoder(w).Encode(map[string]string{
		"version": version,
	})
}

// handleProjects handles GET /api/v1/projects
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projects, err := s.app.GetProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

// handleProjectsWithID handles /api/v1/projects/{id}/*
func (s *Server) handleProjectsWithID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Project ID required", http.StatusBadRequest)
		return
	}

	projectID, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	// DELETE /api/v1/projects/{id}
	if len(parts) == 1 && r.Method == "DELETE" {
		if err := s.app.DeleteProjectByID(uint(projectID)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /api/v1/projects/{id}/crawls
	if len(parts) == 2 && parts[1] == "crawls" && r.Method == "GET" {
		crawls, err := s.app.GetCrawls(uint(projectID))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(crawls)
		return
	}

	// GET /api/v1/projects/{id}/active-stats
	if len(parts) == 2 && parts[1] == "active-stats" && r.Method == "GET" {
		stats, err := s.app.GetActiveCrawlStats(uint(projectID))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
		return
	}

	http.Error(w, "Not found", http.StatusNotFound)
}

// handleCrawls handles /api/v1/crawls/{id}/*
func (s *Server) handleCrawls(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/crawls/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Crawl ID required", http.StatusBadRequest)
		return
	}

	crawlID, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		http.Error(w, "Invalid crawl ID", http.StatusBadRequest)
		return
	}

	// GET /api/v1/crawls/{id}?limit=100&cursor=0&type=html
	if len(parts) == 1 && r.Method == "GET" {
		// Parse pagination parameters
		limitStr := r.URL.Query().Get("limit")
		cursorStr := r.URL.Query().Get("cursor")
		contentTypeFilter := r.URL.Query().Get("type")

		// Default values
		limit := 100
		if limitStr != "" {
			parsedLimit, err := strconv.Atoi(limitStr)
			if err == nil && parsedLimit > 0 {
				limit = parsedLimit
			}
		}

		var cursor uint
		if cursorStr != "" {
			parsedCursor, err := strconv.ParseUint(cursorStr, 10, 32)
			if err == nil {
				cursor = uint(parsedCursor)
			}
		}

		if contentTypeFilter == "" {
			contentTypeFilter = "all"
		}

		// Use paginated endpoint only (non-paginated endpoint removed for scalability)
		result, err := s.app.GetCrawlWithResultsPaginated(uint(crawlID), limit, cursor, contentTypeFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	// DELETE /api/v1/crawls/{id}
	if len(parts) == 1 && r.Method == "DELETE" {
		if err := s.app.DeleteCrawlByID(uint(crawlID)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /api/v1/crawls/{id}/pages/{url}/links
	if len(parts) >= 3 && parts[1] == "pages" && strings.HasSuffix(path, "/links") && r.Method == "GET" {
		// Extract URL from path (everything between "pages/" and "/links")
		pageURL := strings.TrimSuffix(strings.Join(parts[2:], "/"), "/links")
		links, err := s.app.GetPageLinksForURL(uint(crawlID), pageURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(links)
		return
	}

	// GET /api/v1/crawls/{id}/pages/{url}/content
	if len(parts) >= 3 && parts[1] == "pages" && strings.HasSuffix(path, "/content") && r.Method == "GET" {
		// Extract URL from path (everything between "pages/" and "/content")
		pageURL := strings.TrimSuffix(strings.Join(parts[2:], "/"), "/content")
		content, err := s.app.GetPageContent(uint(crawlID), pageURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Return as JSON with content field
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"content": content,
		})
		return
	}

	http.Error(w, "Not found", http.StatusNotFound)
}

// handleStartCrawl handles POST /api/v1/crawl
func (s *Server) handleStartCrawl(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	projectInfo, err := s.app.StartCrawl(req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Crawl started",
		"project": projectInfo,
	})
}

// handleStopCrawl handles POST /api/v1/stop-crawl/{projectID}
func (s *Server) handleStopCrawl(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/stop-crawl/")
	projectID, err := strconv.ParseUint(path, 10, 32)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err := s.app.StopCrawl(uint(projectID)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Crawl stopped",
	})
}

// handleActiveCrawls handles GET /api/v1/active-crawls
func (s *Server) handleActiveCrawls(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	crawls := s.app.GetActiveCrawls()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(crawls)
}

// handleConfig handles GET and PUT /api/v1/config
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		url := r.URL.Query().Get("url")
		if url == "" {
			http.Error(w, "URL parameter required", http.StatusBadRequest)
			return
		}

		config, err := s.app.GetConfigForDomain(url)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)

	case "PUT":
		var req struct {
			URL                      string   `json:"url"`
			JSRendering              bool     `json:"jsRendering"`
			InitialWaitMs            int      `json:"initialWaitMs"`
			ScrollWaitMs             int      `json:"scrollWaitMs"`
			FinalWaitMs              int      `json:"finalWaitMs"`
			Parallelism              int      `json:"parallelism"`
			UserAgent                string   `json:"userAgent"`
			IncludeSubdomains        bool     `json:"includeSubdomains"`
			SpiderEnabled            bool     `json:"spiderEnabled"`
			SitemapEnabled           bool     `json:"sitemapEnabled"`
			SitemapURLs              []string `json:"sitemapURLs"`
			CheckExternalResources   *bool    `json:"checkExternalResources,omitempty"` // Pointer to distinguish between false and not-provided
			RobotsTxtMode            *string  `json:"robotsTxtMode,omitempty"`            // Pointer to distinguish between empty and not-provided
			FollowInternalNofollow   *bool    `json:"followInternalNofollow,omitempty"`   // Pointer to distinguish between false and not-provided
			FollowExternalNofollow   *bool    `json:"followExternalNofollow,omitempty"`   // Pointer to distinguish between false and not-provided
			RespectMetaRobotsNoindex *bool    `json:"respectMetaRobotsNoindex,omitempty"` // Pointer to distinguish between false and not-provided
			RespectNoindex           *bool    `json:"respectNoindex,omitempty"`           // Pointer to distinguish between false and not-provided
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Default to true if not provided
		checkExternal := true
		if req.CheckExternalResources != nil {
			checkExternal = *req.CheckExternalResources
		}

		// Crawler directive defaults (matching ScreamingFrog defaults)
		robotsTxtMode := "respect"
		if req.RobotsTxtMode != nil {
			robotsTxtMode = *req.RobotsTxtMode
		}

		followInternalNofollow := false
		if req.FollowInternalNofollow != nil {
			followInternalNofollow = *req.FollowInternalNofollow
		}

		followExternalNofollow := false
		if req.FollowExternalNofollow != nil {
			followExternalNofollow = *req.FollowExternalNofollow
		}

		respectMetaRobotsNoindex := true
		if req.RespectMetaRobotsNoindex != nil {
			respectMetaRobotsNoindex = *req.RespectMetaRobotsNoindex
		}

		respectNoindex := true
		if req.RespectNoindex != nil {
			respectNoindex = *req.RespectNoindex
		}

		if err := s.app.UpdateConfigForDomain(
			req.URL,
			req.JSRendering,
			req.InitialWaitMs,
			req.ScrollWaitMs,
			req.FinalWaitMs,
			req.Parallelism,
			req.UserAgent,
			req.IncludeSubdomains,
			req.SpiderEnabled,
			req.SitemapEnabled,
			req.SitemapURLs,
			checkExternal,
			robotsTxtMode,
			followInternalNofollow,
			followExternalNofollow,
			respectMetaRobotsNoindex,
			respectNoindex,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Config updated",
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSearch handles GET /api/v1/search/{crawlID}?q={query}&type={contentType}&limit=100&cursor=0
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract crawl ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/search/")
	crawlID, err := strconv.ParseUint(path, 10, 32)
	if err != nil {
		http.Error(w, "Invalid crawl ID", http.StatusBadRequest)
		return
	}

	// Get query parameters
	query := r.URL.Query().Get("q")
	contentTypeFilter := r.URL.Query().Get("type")
	if contentTypeFilter == "" {
		contentTypeFilter = "all"
	}

	limitStr := r.URL.Query().Get("limit")
	cursorStr := r.URL.Query().Get("cursor")

	// Parse pagination parameters with defaults
	limit := 100
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	var cursor uint
	if cursorStr != "" {
		parsedCursor, err := strconv.ParseUint(cursorStr, 10, 32)
		if err == nil {
			cursor = uint(parsedCursor)
		}
	}

	// Use paginated search endpoint only (non-paginated endpoint removed for scalability)
	result, err := s.app.SearchCrawlResultsPaginated(uint(crawlID), query, contentTypeFilter, limit, cursor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
