package sitecheck

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/serpwidth"
)

func TestSerpPureTitleOverPixels(t *testing.T) {
	long := strings.Repeat("Wide Widget Warehouse ", 5) + "End"
	rep, err := newChecker(t).Serp(context.Background(), SerpOptions{Title: long})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Title == nil || rep.Description != nil {
		t.Fatalf("report = %+v (pure single-field preview must not invent the other field)", rep)
	}
	if rep.Title.Pixels <= rep.Title.MaxPx {
		t.Fatalf("fixture too narrow: %dpx <= max %dpx", rep.Title.Pixels, rep.Title.MaxPx)
	}
	if !hasFinding(rep, "title_over_pixels") || !hasFinding(rep, "title_over_chars") {
		t.Errorf("findings = %v", findingIDs(rep))
	}
	if hasFinding(rep, "description_missing") {
		t.Errorf("description_missing must not fire without a fetched page: %v", findingIDs(rep))
	}
	// The truncated preview must itself fit and end with the ellipsis.
	if rep.Title.Truncated == "" || !strings.HasSuffix(rep.Title.Truncated, "…") {
		t.Fatalf("truncated = %q", rep.Title.Truncated)
	}
	if w := serpwidth.Width(rep.Title.Truncated, serpwidth.TitleFontPx); w > rep.Title.MaxPx {
		t.Errorf("truncated preview is %dpx, over max %dpx", w, rep.Title.MaxPx)
	}
}

func TestSerpPureShortDescription(t *testing.T) {
	rep, err := newChecker(t).Serp(context.Background(), SerpOptions{Description: "Too short."})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "description_below_chars") || !hasFinding(rep, "description_below_pixels") {
		t.Errorf("findings = %v", findingIDs(rep))
	}
	if rep.Description.Truncated != "" {
		t.Errorf("no truncation expected, got %q", rep.Description.Truncated)
	}
}

func TestSerpHealthyPair(t *testing.T) {
	rep, err := newChecker(t).Serp(context.Background(), SerpOptions{
		Title:       "Wide Widget Warehouse — Widgets Shipped Fast",
		Description: "Browse hundreds of widgets with free next-day delivery, easy returns and a lifetime warranty on every single widget in our catalogue.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Findings(); len(got) != 0 {
		t.Errorf("healthy pair findings = %v", got)
	}
}

func TestSerpFetchesPage(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Tiny</title></head><body></body></html>`)
	})
	rep, err := newChecker(t).Serp(context.Background(), SerpOptions{URL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Fetched || rep.Title == nil || rep.Title.Text != "Tiny" {
		t.Fatalf("report = %+v", rep)
	}
	// The page has no meta description: with a fetched page that IS a finding.
	if !hasFinding(rep, "description_missing") || !hasFinding(rep, "title_below_chars") {
		t.Errorf("findings = %v", findingIDs(rep))
	}
}

func TestSerpURLWithOverride(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Live title</title></head><body></body></html>`)
	})
	rep, err := newChecker(t).Serp(context.Background(), SerpOptions{URL: s.URL, Title: "Edited draft title"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Title.Text != "Edited draft title" {
		t.Errorf("explicit title must override the fetched one, got %q", rep.Title.Text)
	}
}

func TestSerpFetchFailure(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})
	rep, err := newChecker(t).Serp(context.Background(), SerpOptions{URL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fetched || rep.Findings() != nil {
		t.Errorf("report = %+v findings = %v", rep, rep.Findings())
	}
}

func TestSerpNoInput(t *testing.T) {
	if _, err := newChecker(t).Serp(context.Background(), SerpOptions{}); err == nil {
		t.Fatal("want an error when nothing is given")
	}
}
