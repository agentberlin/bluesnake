package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake"
)

func main() {
	// Array containing all the known URLs in a sitemap
	knownUrls := []string{}

	// Create a Collector specifically for Shopify
	c := bluesnake.NewCollector(bluesnake.AllowedDomains("www.shopify.com"))

	// Create a callback on the XPath query searching for the URLs
	c.OnXML("//urlset/url/loc", func(e *bluesnake.XMLElement) {
		knownUrls = append(knownUrls, e.Text)
	})

	// Start the collector
	c.Visit("https://www.shopify.com/sitemap.xml")

	fmt.Println("All known URLs:")
	for _, url := range knownUrls {
		fmt.Println("\t", url)
	}
	fmt.Println("Collected", len(knownUrls), "URLs")
}
