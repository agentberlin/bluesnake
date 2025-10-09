package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake"
)

func main() {
	// Instantiate default collector
	c := bluesnake.NewCollector(
		// MaxDepth is 2, so only the links on the scraped page
		// and links on those pages are visited
		bluesnake.MaxDepth(2),
		bluesnake.Async(),
	)

	// Limit the maximum parallelism to 2
	// This is necessary if the goroutines are dynamically
	// created to control the limit of simultaneous requests.
	//
	// Parallelism can be controlled also by spawning fixed
	// number of go routines.
	c.Limit(&bluesnake.LimitRule{DomainGlob: "*", Parallelism: 2})

	// On every a element which has href attribute call callback
	c.OnHTML("a[href]", func(e *bluesnake.HTMLElement) {
		link := e.Attr("href")
		// Print link
		fmt.Println(link)
		// Visit link found on page on a new thread
		e.Request.Visit(link)
	})

	// Start scraping on https://en.wikipedia.org
	c.Visit("https://en.wikipedia.org/")
	// Wait until threads are finished
	c.Wait()
}
