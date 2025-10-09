package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake"
)

func main() {
	// Instantiate default collector
	c := bluesnake.NewCollector(
		// MaxDepth is 1, so only the links on the scraped page
		// is visited, and no further links are followed
		bluesnake.MaxDepth(1),
	)

	// On every a element which has href attribute call callback
	c.OnHTML("a[href]", func(e *bluesnake.HTMLElement) {
		link := e.Attr("href")
		// Print link
		fmt.Println(link)
		// Visit link found on page
		e.Request.Visit(link)
	})

	// Start scraping on https://en.wikipedia.org
	c.Visit("https://en.wikipedia.org/")
}
