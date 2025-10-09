package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake"
	"github.com/agentberlin/bluesnake/debug"
)

func main() {
	url := "https://httpbin.org/delay/2"

	// Instantiate default collector
	c := bluesnake.NewCollector(
		// Turn on asynchronous requests
		bluesnake.Async(),
		// Attach a debugger to the collector
		bluesnake.Debugger(&debug.LogDebugger{}),
	)

	// Limit the number of threads started by colly to two
	// when visiting links which domains' matches "*httpbin.*" glob
	c.Limit(&bluesnake.LimitRule{
		DomainGlob:  "*httpbin.*",
		Parallelism: 2,
		//Delay:      5 * time.Second,
	})

	// Start scraping in five threads on https://httpbin.org/delay/2
	for i := 0; i < 5; i++ {
		c.Visit(fmt.Sprintf("%s?n=%d", url, i))
	}
	// Wait until threads are finished
	c.Wait()
}
