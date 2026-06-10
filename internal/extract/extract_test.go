package extract

import (
	"strings"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
)

const page = `<html><head><title>T</title></head><body>
<div class="price" data-sku="A1">$19.99</div>
<div class="price" data-sku="A2">$5.00</div>
<p>Call us on 555-0100 today. Or 555-0199.</p>
<span id="stock">In stock</span>
</body></html>`

func engine(t *testing.T, mutate func(*config.Config)) *Engine {
	t.Helper()
	cfg := config.Default()
	mutate(cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func value(results []Result, name string) string {
	for _, r := range results {
		if r.Name == name {
			return r.Value
		}
	}
	return "<absent>"
}

func TestNoConfigMeansNilEngine(t *testing.T) {
	e, err := New(config.Default())
	if err != nil || e != nil {
		t.Fatalf("e=%v err=%v", e, err)
	}
}

func TestCustomSearch(t *testing.T) {
	e := engine(t, func(c *config.Config) {
		c.CustomSearch = []config.CustomSearch{
			{Name: "phone", Mode: "contains", Pattern: `555-\d{4}`, Regex: true},
			{Name: "plain", Mode: "contains", Pattern: "price"},
			{Name: "no-gtm", Mode: "not_contains", Pattern: "gtm.js"},
			{Name: "in-text", Mode: "contains", Pattern: "Call us", Scope: "text"},
			{Name: "in-el", Mode: "contains", Pattern: "In stock", Scope: "element:#stock"},
		}
	})
	results := e.Run([]byte(page), "Call us on 555-0100 today. Or 555-0199. In stock")
	if got := value(results, "phone"); got != "2" {
		t.Errorf("phone = %s", got)
	}
	if got := value(results, "plain"); got != "2" {
		t.Errorf("plain = %s", got)
	}
	if got := value(results, "no-gtm"); got != "true" {
		t.Errorf("no-gtm = %s", got)
	}
	if got := value(results, "in-text"); got != "1" {
		t.Errorf("in-text = %s", got)
	}
	if got := value(results, "in-el"); got != "1" {
		t.Errorf("in-el = %s", got)
	}
}

func TestCustomExtraction(t *testing.T) {
	e := engine(t, func(c *config.Config) {
		c.CustomExtraction = []config.CustomExtraction{
			{Name: "prices-xpath", Type: "xpath", Expression: `//div[@class='price']`},
			{Name: "count-xpath", Type: "xpath", Expression: `count(//div[@class='price'])`, Return: "function"},
			{Name: "skus-css", Type: "css", Expression: "div.price", Attribute: "data-sku"},
			{Name: "stock-css", Type: "css", Expression: "#stock"},
			{Name: "phone-re", Type: "regex", Expression: `Call us on (555-\d{4})`},
		}
	})
	results := e.Run([]byte(page), "")
	if got := value(results, "prices-xpath"); got != "$19.99 | $5.00" {
		t.Errorf("prices-xpath = %q", got)
	}
	if got := value(results, "count-xpath"); !strings.HasPrefix(got, "2") {
		t.Errorf("count-xpath = %q", got)
	}
	if got := value(results, "skus-css"); got != "A1 | A2" {
		t.Errorf("skus-css = %q", got)
	}
	if got := value(results, "stock-css"); got != "In stock" {
		t.Errorf("stock-css = %q", got)
	}
	if got := value(results, "phone-re"); got != "555-0100" {
		t.Errorf("phone-re = %q", got)
	}
}

func TestBadExpressionsError(t *testing.T) {
	cfg := config.Default()
	cfg.CustomExtraction = []config.CustomExtraction{{Name: "bad", Type: "xpath", Expression: "//["}}
	if _, err := New(cfg); err == nil {
		t.Error("bad xpath must error")
	}
}
