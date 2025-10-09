package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake"
)

func main() {
	// Create a collector
	c := bluesnake.NewCollector()

	// Set HTML callback
	// Won't be called if error occurs
	c.OnHTML("*", func(e *bluesnake.HTMLElement) {
		fmt.Println(e)
	})

	// Set error handler
	c.OnError(func(r *bluesnake.Response, err error) {
		fmt.Println("Request URL:", r.Request.URL, "failed with response:", r, "\nError:", err)
	})

	// Start scraping
	c.Visit("https://definitely-not-a.website/")
}
