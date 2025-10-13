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

package app

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/types"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/temoto/robotstxt"
	"golang.org/x/net/html"
)

//go:embed bots.json
var botsConfigJSON []byte

// Bot represents a bot configuration
type Bot struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	InfoURL         string   `json:"info_url"`
	UserAgentPrefix string   `json:"user_agent_prefix"`
	Type            []string `json:"type"`
	Brand           string   `json:"brand"`
	Domain          string   `json:"domain"`
}

// BotsConfig represents the bots configuration file
type BotsConfig struct {
	Bots []Bot `json:"bots"`
}

var (
	botsConfig     *BotsConfig
	botsConfigOnce sync.Once
)

// loadBotsConfig loads the bots configuration from embedded JSON
func loadBotsConfig() (*BotsConfig, error) {
	var err error
	botsConfigOnce.Do(func() {
		botsConfig = &BotsConfig{}
		err = json.Unmarshal(botsConfigJSON, botsConfig)
	})
	return botsConfig, err
}

// buildUserAgent constructs a user agent string for a bot
func buildUserAgent(bot Bot) string {
	ua := fmt.Sprintf("%s/%s (+%s)", bot.Name, bot.Version, bot.InfoURL)
	if bot.UserAgentPrefix != "" {
		return fmt.Sprintf("%s %s", bot.UserAgentPrefix, ua)
	}
	return ua
}

// validateURL performs basic URL validation and SSRF protection
func validateURL(rawURL string) error {
	// Parse URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Check scheme
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme: must be http or https")
	}

	// Check host
	if u.Host == "" {
		return fmt.Errorf("URL must have a host")
	}

	// Resolve hostname to IP addresses for SSRF check
	host := u.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	// Check for private IPs (SSRF protection)
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("access to private IP addresses is not allowed")
		}
	}

	return nil
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(ip net.IP) bool {
	// IPv4 private ranges
	privateIPv4Ranges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
	}

	for _, cidr := range privateIPv4Ranges {
		_, ipNet, _ := net.ParseCIDR(cidr)
		if ipNet.Contains(ip) {
			return true
		}
	}

	// IPv6 checks
	if ip.To4() == nil {
		// Check for loopback
		if ip.IsLoopback() {
			return true
		}
		// Check for link-local
		if ip.IsLinkLocalUnicast() {
			return true
		}
		// Check for unique local addresses (fc00::/7)
		if len(ip) == net.IPv6len && ip[0] == 0xfc || ip[0] == 0xfd {
			return true
		}
	}

	return false
}

// CheckRobotsTxt checks robots.txt for bot access
func (a *App) CheckRobotsTxt(targetURL string) (map[string]types.BotAccess, error) {
	// Validate URL
	if err := validateURL(targetURL); err != nil {
		return nil, err
	}

	// Parse URL to get domain
	u, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Fetch robots.txt
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", u.Scheme, u.Host)
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(robotsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch robots.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("robots.txt returned status %d", resp.StatusCode)
	}

	// Read and parse robots.txt
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read robots.txt: %w", err)
	}

	robotsData, err := robotstxt.FromBytes(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse robots.txt: %w", err)
	}

	// Load bot configuration
	config, err := loadBotsConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load bots config: %w", err)
	}

	// Check each bot
	results := make(map[string]types.BotAccess)
	for _, bot := range config.Bots {
		userAgent := buildUserAgent(bot)
		allowed := robotsData.TestAgent(targetURL, userAgent)

		results[bot.Name] = types.BotAccess{
			Allowed: allowed,
			Domain:  bot.Domain,
			Message: "",
		}
	}

	return results, nil
}

// CheckHTTPAccess tests HTTP access for each bot
func (a *App) CheckHTTPAccess(targetURL string) (map[string]types.BotAccess, error) {
	// Validate URL
	if err := validateURL(targetURL); err != nil {
		return nil, err
	}

	// Load bot configuration
	config, err := loadBotsConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load bots config: %w", err)
	}

	// Create HTTP client
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow up to 10 redirects
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// Check each bot
	results := make(map[string]types.BotAccess)
	for _, bot := range config.Bots {
		userAgent := buildUserAgent(bot)

		// Try HEAD request first
		req, err := http.NewRequest("HEAD", targetURL, nil)
		if err != nil {
			results[bot.Name] = types.BotAccess{
				Allowed: false,
				Domain:  bot.Domain,
				Message: fmt.Sprintf("Failed to create request: %v", err),
			}
			continue
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := client.Do(req)
		if err != nil {
			results[bot.Name] = types.BotAccess{
				Allowed: false,
				Domain:  bot.Domain,
				Message: fmt.Sprintf("Request failed: %v", err),
			}
			continue
		}
		resp.Body.Close()

		// If HEAD not allowed, try GET
		if resp.StatusCode == http.StatusMethodNotAllowed {
			req, err = http.NewRequest("GET", targetURL, nil)
			if err != nil {
				results[bot.Name] = types.BotAccess{
					Allowed: false,
					Domain:  bot.Domain,
					Message: fmt.Sprintf("Failed to create request: %v", err),
				}
				continue
			}
			req.Header.Set("User-Agent", userAgent)

			resp, err = client.Do(req)
			if err != nil {
				results[bot.Name] = types.BotAccess{
					Allowed: false,
					Domain:  bot.Domain,
					Message: fmt.Sprintf("Request failed: %v", err),
				}
				continue
			}
			resp.Body.Close()
		}

		// Success if status code < 400
		success := resp.StatusCode < 400
		results[bot.Name] = types.BotAccess{
			Allowed: success,
			Domain:  bot.Domain,
			Message: fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}

	return results, nil
}

// extractVisibleText extracts visible text from HTML, excluding script/style/noscript tags
func extractVisibleText(htmlContent string) (string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	var text strings.Builder
	var traverse func(*html.Node)

	traverse = func(n *html.Node) {
		// Skip script, style, noscript tags
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}

		if n.Type == html.TextNode {
			text.WriteString(n.Data)
			text.WriteString(" ")
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return text.String(), nil
}

// tokenizeText tokenizes text into lowercase words
func tokenizeText(text string) map[string]bool {
	// Split by whitespace and punctuation
	re := regexp.MustCompile(`\s+`)
	words := re.Split(strings.TrimSpace(text), -1)

	tokens := make(map[string]bool)
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word != "" && len(word) > 1 { // Filter out single characters
			tokens[word] = true
		}
	}
	return tokens
}

// calculateSSRScore calculates the SSR score by comparing raw and rendered HTML
func calculateSSRScore(rawHTML, renderedHTML string) (float64, error) {
	// Extract visible text
	rawText, err := extractVisibleText(rawHTML)
	if err != nil {
		return 0, fmt.Errorf("failed to extract raw text: %w", err)
	}

	renderedText, err := extractVisibleText(renderedHTML)
	if err != nil {
		return 0, fmt.Errorf("failed to extract rendered text: %w", err)
	}

	// Tokenize
	rawTokens := tokenizeText(rawText)
	renderedTokens := tokenizeText(renderedText)

	if len(renderedTokens) == 0 {
		return 0, nil
	}

	// Calculate intersection
	intersection := 0
	for token := range rawTokens {
		if renderedTokens[token] {
			intersection++
		}
	}

	// Calculate percentage
	score := float64(intersection) / float64(len(renderedTokens)) * 100.0
	return score, nil
}

// CheckSSR performs SSR check and returns score with screenshot
func (a *App) CheckSSR(targetURL string) (*types.ContentVisibilityResult, string, error) {
	// Validate URL
	if err := validateURL(targetURL); err != nil {
		return nil, "", err
	}

	// Fetch raw HTML
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(targetURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	isError := statusCode >= 400

	rawHTML, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	// Create chromedp context
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Set timeout
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var renderedHTML string
	var screenshotData []byte

	// Navigate and capture
	err = chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond), // Initial wait for JavaScript
		// Scroll to top to ensure screenshot is from top of page
		chromedp.Evaluate(`window.scrollTo(0, 0)`, nil),
		chromedp.Sleep(1000*time.Millisecond), // Wait after scroll
		chromedp.OuterHTML("html", &renderedHTML, chromedp.ByQuery),
		chromedp.FullScreenshot(&screenshotData, 90),
	)

	if err != nil {
		return nil, "", fmt.Errorf("chromedp failed: %w", err)
	}

	// Calculate SSR score
	score, err := calculateSSRScore(string(rawHTML), renderedHTML)
	if err != nil {
		return nil, "", fmt.Errorf("failed to calculate SSR score: %w", err)
	}

	// Encode screenshot as base64
	screenshot := base64.StdEncoding.EncodeToString(screenshotData)

	result := &types.ContentVisibilityResult{
		Score:      score,
		StatusCode: statusCode,
		IsError:    isError,
	}

	return result, screenshot, nil
}

// GetSSRCSRScreenshots captures screenshots with and without JavaScript
func (a *App) GetSSRCSRScreenshots(targetURL string) (jsScreenshot, noJSScreenshot string, err error) {
	// Validate URL
	if err := validateURL(targetURL); err != nil {
		return "", "", err
	}

	// Create chromedp context
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	// Capture with JS enabled
	{
		ctx, cancel := chromedp.NewContext(allocCtx)
		defer cancel()

		ctx, cancel = context.WithTimeout(ctx, 75*time.Second)
		defer cancel()

		var jsData []byte
		err := chromedp.Run(ctx,
			chromedp.Navigate(targetURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Sleep(2*time.Second),
			// Scroll to top before taking screenshot
			chromedp.Evaluate(`window.scrollTo({top: 0, behavior: 'instant'})`, nil),
			chromedp.Sleep(500*time.Millisecond),
			chromedp.FullScreenshot(&jsData, 90),
		)

		if err != nil {
			return "", "", fmt.Errorf("JS-enabled screenshot failed: %w", err)
		}

		jsScreenshot = base64.StdEncoding.EncodeToString(jsData)
	}

	// Capture with JS disabled
	{
		ctx, cancel := chromedp.NewContext(allocCtx)
		defer cancel()

		ctx, cancel = context.WithTimeout(ctx, 75*time.Second)
		defer cancel()

		var noJSData []byte
		err := chromedp.Run(ctx,
			emulation.SetScriptExecutionDisabled(true),
			chromedp.Navigate(targetURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			// Scroll to top before taking screenshot
			chromedp.Evaluate(`window.scrollTo({top: 0, behavior: 'instant'})`, nil),
			chromedp.Sleep(500*time.Millisecond),
			chromedp.FullScreenshot(&noJSData, 90),
		)

		if err != nil {
			return "", "", fmt.Errorf("JS-disabled screenshot failed: %w", err)
		}

		noJSScreenshot = base64.StdEncoding.EncodeToString(noJSData)
	}

	return jsScreenshot, noJSScreenshot, nil
}

// RunAICrawlerChecks runs all AI crawler checks and saves the results
func (a *App) RunAICrawlerChecks(projectURL string) error {
	// Format URL
	formattedURL := projectURL
	if !strings.HasPrefix(formattedURL, "http://") && !strings.HasPrefix(formattedURL, "https://") {
		formattedURL = "https://" + formattedURL
	}

	// Run checks
	robotsData, err := a.CheckRobotsTxt(formattedURL)
	if err != nil {
		return fmt.Errorf("robots.txt check failed: %w", err)
	}

	httpData, err := a.CheckHTTPAccess(formattedURL)
	if err != nil {
		return fmt.Errorf("HTTP access check failed: %w", err)
	}

	ssrResult, ssrScreenshot, err := a.CheckSSR(formattedURL)
	if err != nil {
		return fmt.Errorf("SSR check failed: %w", err)
	}

	jsScreenshot, noJSScreenshot, err := a.GetSSRCSRScreenshots(formattedURL)
	if err != nil {
		return fmt.Errorf("screenshots capture failed: %w", err)
	}

	// Save results
	aiCrawlerData := &types.AICrawlerData{
		ContentVisibility: ssrResult,
		RobotsTxt:         robotsData,
		HTTPCheck:         httpData,
	}

	return a.SaveAICrawlerData(projectURL, aiCrawlerData, ssrScreenshot, jsScreenshot, noJSScreenshot)
}
