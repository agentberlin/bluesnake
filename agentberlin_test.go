package bluesnake

import (
	"os"
	"testing"
	"strings"
)

func TestAgentBerlinNewlines(t *testing.T) {
	html, err := os.ReadFile("/tmp/agentberlin.html")
	if err != nil {
		t.Skip("No test HTML file")
	}
	
	result := extractMainContentText(html)
	
	// Print first 2000 chars
	output := result
	if len(output) > 2000 {
		output = output[:2000]
	}
	t.Logf("Result (first 2000 chars):\n%s", output)
	
	// Check if newlines are present
	newlineCount := strings.Count(result, "\n")
	t.Logf("Newline count: %d", newlineCount)
	
	if newlineCount < 5 {
		t.Error("Expected more newlines in output")
	}
}
