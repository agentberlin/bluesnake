package parse

import (
	"bytes"
	"slices"

	"golang.org/x/net/html"
)

// headValidElements are the only elements Google accepts inside <head>;
// anything else ends the head from a search-bot perspective.
var headValidElements = []string{
	"title", "meta", "link", "script", "style", "base", "noscript", "template",
}

// headChecks runs the Validation-tab checks that need the raw token stream:
// the tree parser repairs pathologies (merging duplicate heads, synthesizing
// missing ones), so these are detected before repair.
func headChecks(body []byte) HeadValidity {
	var hv HeadValidity
	z := html.NewTokenizer(bytes.NewReader(body))

	headCount, bodyCount := 0, 0
	sawHTML := false
	elementSeenInsideHTML := false // any element between <html> and the first <head>
	inHead := false
	headEnded := false

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken && tt != html.EndTagToken {
			continue
		}
		name, _ := z.TagName()
		tag := string(name)

		if tt == html.EndTagToken {
			if tag == "head" {
				inHead = false
				headEnded = true
			}
			continue
		}

		switch tag {
		case "html":
			sawHTML = true
		case "head":
			headCount++
			if headCount == 1 && elementSeenInsideHTML {
				hv.HeadNotFirst = true
			}
			inHead = true
		case "body":
			bodyCount++
			if !sawHTML {
				hv.BodyBeforeHTML = true
			}
			inHead = false
			headEnded = true
			if headCount == 0 {
				elementSeenInsideHTML = true
			}
		default:
			if sawHTML && headCount == 0 {
				elementSeenInsideHTML = true
			}
			if inHead && !headEnded && !slices.Contains(headValidElements, tag) {
				hv.InvalidElementsInHead = append(hv.InvalidElementsInHead, tag)
			}
		}
	}

	hv.MissingHead = headCount == 0
	hv.MultipleHead = headCount > 1
	hv.MissingBody = bodyCount == 0
	hv.MultipleBody = bodyCount > 1
	return hv
}
