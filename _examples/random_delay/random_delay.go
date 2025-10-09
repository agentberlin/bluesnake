package main

import (
	"fmt"
	"time"

	"github.com/agentberlin/bluesnake"
	"github.com/agentberlin/bluesnake/debug"
)

func main() {
	url := "https://httpbin.org/delay/2"

	// Instantiate default collector
	c := bluesnake.NewCollector(
		// Attach a debugger to the collector
		bluesnake.Debugger(&debug.LogDebugger{}),
		bluesnake.Async(),
	)

	// Limit the number of threads started by colly to two
	// when visiting links which domains' matches "*httpbin.*" glob
	c.Limit(&bluesnake.LimitRule{
		DomainGlob:  "*httpbin.*",
		Parallelism: 2,
		RandomDelay: 5 * time.Second,
	})

	// Start scraping in four threads on https://httpbin.org/delay/2
	for i := 0; i < 4; i++ {
		c.Visit(fmt.Sprintf("%s?n=%d", url, i))
	}
	// Start scraping on https://httpbin.org/delay/2
	c.Visit(url)
	// Wait until threads are finished
	c.Wait()
}
