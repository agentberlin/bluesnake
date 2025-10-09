package extensions

import (
	"github.com/agentberlin/bluesnake"
)

// Referer sets valid Referer HTTP header to requests.
// Warning: this extension works only if you use Request.Visit
// from callbacks instead of Collector.Visit.
func Referer(c *bluesnake.Collector) {
	c.OnResponse(func(r *bluesnake.Response) {
		r.Ctx.Put("_referer", r.Request.URL.String())
	})
	c.OnRequest(func(r *bluesnake.Request) {
		if ref := r.Ctx.Get("_referer"); ref != "" {
			r.Headers.Set("Referer", ref)
		}
	})
}
