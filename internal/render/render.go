// Package render drives headless Chrome (via the DevTools protocol) for
// JavaScript rendering mode: it loads a page, waits the configured AJAX
// timeout for scripts to settle, and returns the rendered DOM, console
// errors and an optional screenshot. The crawler parses raw and rendered
// HTML separately and diffs them (the JavaScript tab data).
package render

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/hhsecond/acrawler/internal/config"
)

// Result is one rendered page.
type Result struct {
	HTML          string
	ConsoleErrors []string
	Screenshot    []byte
}

// Renderer owns a headless Chrome allocator; each Render call runs in its
// own tab. Safe for concurrent use (bounded internally).
type Renderer struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	cfg         *config.Config
	sem         chan struct{}
}

// ChromePath locates a Chrome/Chromium binary (config override first).
func ChromePath(cfg *config.Config) string {
	if cfg.Rendering.ChromePath != "" {
		return cfg.Rendering.ChromePath
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// New starts the Chrome allocator. Errors when no Chrome can be found.
func New(cfg *config.Config) (*Renderer, error) {
	path := ChromePath(cfg)
	if path == "" {
		return nil, fmt.Errorf("rendering.mode=javascript requires Chrome/Chromium (set rendering.chrome_path)")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(path),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.IgnoreCertErrors,
		chromedp.UserAgent(cfg.HTTP.UserAgent),
	)
	if w, h := cfg.Rendering.WindowWidth, cfg.Rendering.WindowHeight; w > 0 && h > 0 {
		opts = append(opts, chromedp.WindowSize(w, h))
	} else {
		opts = append(opts, chromedp.WindowSize(1024, 768)) // googlebot-desktop preset
	}
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	concurrency := min(cfg.Speed.MaxThreads, maxTabs())
	return &Renderer{
		allocCtx:    allocCtx,
		allocCancel: cancel,
		cfg:         cfg,
		sem:         make(chan struct{}, concurrency),
	}, nil
}

func maxTabs() int {
	if runtime.NumCPU() < 4 {
		return 2
	}
	return 4
}

func (r *Renderer) Close() { r.allocCancel() }

// Render loads the URL, waits the AJAX timeout, and snapshots the DOM.
func (r *Renderer) Render(ctx context.Context, url string) (*Result, error) {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	tabCtx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()
	timeout := time.Duration(r.cfg.Advanced.ResponseTimeoutSec+r.cfg.Rendering.AjaxTimeoutSec+10) * time.Second
	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, timeout)
	defer cancelTimeout()
	go func() { // propagate caller cancellation
		select {
		case <-ctx.Done():
			cancel()
		case <-tabCtx.Done():
		}
	}()

	res := &Result{}
	if r.cfg.Rendering.JSErrorReporting {
		var mu sync.Mutex
		chromedp.ListenTarget(tabCtx, func(ev any) {
			if e, ok := ev.(*cdpruntime.EventConsoleAPICalled); ok && e.Type == "error" {
				mu.Lock()
				for _, arg := range e.Args {
					res.ConsoleErrors = append(res.ConsoleErrors, string(arg.Value))
				}
				mu.Unlock()
			}
		})
	}

	actions := []chromedp.Action{
		chromedp.Navigate(url),
		chromedp.Sleep(time.Duration(r.cfg.Rendering.AjaxTimeoutSec) * time.Second),
		chromedp.OuterHTML("html", &res.HTML),
	}
	if r.cfg.Rendering.Screenshots {
		actions = append(actions, chromedp.FullScreenshot(&res.Screenshot, 80))
	}
	if err := chromedp.Run(tabCtx, actions...); err != nil {
		return nil, err
	}
	return res, nil
}
