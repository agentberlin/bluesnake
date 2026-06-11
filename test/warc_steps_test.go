package acceptance

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

func (w *world) registerWARCSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the stored crawl archive contains a response record for "([^"]*)"$`, w.storedArchiveHasResponse)
}

// storedArchiveHasResponse gunzips the stored crawl's archive.warc.gz member
// by member (each WARC record is its own gzip member) and looks for a
// response record whose WARC-Target-URI is the test server URL plus path.
func (w *world) storedArchiveHasResponse(path string) error {
	archive := filepath.Join(w.storeDirPath(), "crawls", w.storedCrawlID+".assets", "archive.warc.gz")
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	target := w.server.URL + path
	br := bufio.NewReader(bytes.NewReader(data))
	gz, err := gzip.NewReader(br)
	if err != nil {
		return err
	}
	defer gz.Close()
	for {
		gz.Multistream(false)
		member, err := io.ReadAll(gz)
		if err != nil {
			return err
		}
		if warcMemberIsResponseFor(member, target) {
			return nil
		}
		if err := gz.Reset(br); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}
	return fmt.Errorf("no response record for %s in %s", target, archive)
}

func warcMemberIsResponseFor(member []byte, target string) bool {
	head, _, ok := bytes.Cut(member, []byte("\r\n\r\n"))
	if !ok {
		return false
	}
	var isResponse, hasTarget bool
	for _, line := range strings.Split(string(head), "\r\n") {
		name, value, _ := strings.Cut(line, ":")
		switch name {
		case "WARC-Type":
			isResponse = strings.TrimSpace(value) == "response"
		case "WARC-Target-URI":
			hasTarget = strings.TrimSpace(value) == target
		}
	}
	return isResponse && hasTarget
}
