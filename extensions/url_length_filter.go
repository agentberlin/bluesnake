package extensions

import (
	"github.com/agentberlin/bluesnake"
)

// URLLengthFilter filters out requests with URLs longer than URLLengthLimit
func URLLengthFilter(c *bluesnake.Collector, URLLengthLimit int) {
	c.OnRequest(func(r *bluesnake.Request) {
		if len(r.URL.String()) > URLLengthLimit {
			r.Abort()
		}
	})
}
