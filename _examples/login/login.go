package main

import (
	"log"

	"github.com/agentberlin/bluesnake"
)

func main() {
	// create a new collector
	c := bluesnake.NewCollector()

	// authenticate
	err := c.Post("http://example.com/login", map[string]string{"username": "admin", "password": "admin"})
	if err != nil {
		log.Fatal(err)
	}

	// attach callbacks after login
	c.OnResponse(func(r *bluesnake.Response) {
		log.Println("response received", r.StatusCode)
	})

	// start scraping
	c.Visit("https://example.com/")
}
