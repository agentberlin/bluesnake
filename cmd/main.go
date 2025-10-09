package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/agentberlin/bluesnake"
)

func main() {
	urlFlag := flag.String("url", "", "URL to crawl")
	flag.Parse()

	if *urlFlag == "" {
		log.Fatal("Please provide a URL to crawl with the --url flag")
	}

	parsedURL, err := url.Parse(*urlFlag)
	if err != nil {
		log.Fatalf("Failed to parse URL: %v", err)
	}

	c := bluesnake.NewCollector(
		bluesnake.AllowedDomains(parsedURL.Hostname()),
	)

	c.OnRequest(func(r *bluesnake.Request) {
		fmt.Println("Crawling:", r.URL.String())
	})

	c.OnHTML("a[href]", func(e *bluesnake.HTMLElement) {
		link := e.Request.AbsoluteURL(e.Attr("href"))
		if link != "" {
			c.Visit(link)
		}
	})

	c.OnResponse(func(r *bluesnake.Response) {
		contentType := r.Headers.Get("Content-Type")
		xRobotsTag := r.Headers.Get("X-Robots-Tag")
		isIndexable := "Yes"
		if strings.Contains(strings.ToLower(xRobotsTag), "noindex") {
			isIndexable = "No"
		}

		if strings.Contains(contentType, "text/html") {
			r.Request.Ctx.Put("isIndexable", isIndexable)
		}

		fmt.Printf("  > Status: %d\n", r.StatusCode)
		fmt.Printf("  > Content-Type: %s\n", contentType)

	})

	c.OnHTML("title", func(e *bluesnake.HTMLElement) {
		isIndexable := e.Request.Ctx.Get("isIndexable")
		if isIndexable == "" {
			isIndexable = "Yes"
		}
		if strings.Contains(strings.ToLower(e.Text), "noindex") {
			isIndexable = "No"
		}
		fmt.Printf("  > Title: %s\n", e.Text)
		fmt.Printf("  > Indexable: %s\n", isIndexable)
	})

	c.OnError(func(r *bluesnake.Response, err error) {
		fmt.Printf("  > Error: %v\n", err)
	})

	c.Visit(*urlFlag)
	c.Wait()
}
