package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake"
)

func main() {
	// Instantiate default collector
	c := bluesnake.NewCollector()

	// Before making a request put the URL with
	// the key of "url" into the context of the request
	c.OnRequest(func(r *bluesnake.Request) {
		r.Ctx.Put("url", r.URL.String())
	})

	// After making a request get "url" from
	// the context of the request
	c.OnResponse(func(r *bluesnake.Response) {
		fmt.Println(r.Ctx.Get("url"))
	})

	// Start scraping on https://en.wikipedia.org
	c.Visit("https://en.wikipedia.org/")
}
