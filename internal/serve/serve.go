// Package serve exposes stored crawls over a read-only localhost JSON API
// (the `bluesnake serve` subcommand — DESIGN.md §8). It reuses the export
// layer wholesale: every dataset, report and issue listing the CLI can
// produce is reachable over HTTP, which makes scripting and UIs trivial.
package serve

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/agentberlin/bluesnake/internal/export"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// apiDataset is the wire form of an export.Dataset.
type apiDataset struct {
	Name   string     `json:"name"`
	Header []string   `json:"header"`
	Rows   [][]string `json:"rows"`
}

func wireDataset(d *export.Dataset) apiDataset {
	return apiDataset{Name: d.Name, Header: d.Header, Rows: d.Rows}
}

type apiCrawl struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Seed    string `json:"seed"`
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	Crawled int    `json:"crawled"`
}

type apiIssue struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Priority string `json:"priority"`
	Tab      string `json:"tab"`
	Count    int    `json:"count"`
}

type handler struct {
	storeDir string
}

// Handler serves the read-only API over every crawl in storeDir.
func Handler(storeDir string) http.Handler {
	h := &handler{storeDir: storeDir}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/crawls", h.crawls)
	mux.HandleFunc("GET /api/crawls/{id}/overview", h.overview)
	mux.HandleFunc("GET /api/crawls/{id}/datasets", h.datasets)
	mux.HandleFunc("GET /api/crawls/{id}/datasets/{name}", h.dataset)
	mux.HandleFunc("GET /api/crawls/{id}/reports", h.reports)
	mux.HandleFunc("GET /api/crawls/{id}/reports/{name}", h.report)
	mux.HandleFunc("GET /api/crawls/{id}/issues", h.issues)
	mux.HandleFunc("GET /api/crawls/{id}/page", h.page)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// open resolves the crawl id from the path; a missing crawl is a 404. The
// error sent to the client is generic on purpose — store errors carry
// filesystem paths and SQLite internals that this localhost-but-bindable API
// should not disclose (store.OpenCrawl also rejects traversing ids).
func (h *handler) open(w http.ResponseWriter, r *http.Request) *store.Crawl {
	st, err := store.OpenCrawl(h.storeDir, r.PathValue("id"))
	if err != nil {
		// generic + no echo of the (user-controlled) id: avoid both path
		// disclosure and reflecting arbitrary input back in the response
		writeError(w, http.StatusNotFound, "crawl not found")
		return nil
	}
	return st
}

func (h *handler) crawls(w http.ResponseWriter, r *http.Request) {
	infos, err := store.ListCrawls(h.storeDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	entries := make([]apiCrawl, 0, len(infos))
	for _, in := range infos {
		entries = append(entries, apiCrawl{
			ID: in.ID, Project: in.Project, Seed: in.Seed,
			Mode: in.Mode, Status: in.Status, Crawled: in.Crawled,
		})
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *handler) datasets(w http.ResponseWriter, r *http.Request) {
	if st := h.open(w, r); st != nil {
		st.Close()
		writeJSON(w, http.StatusOK, export.List())
	}
}

func (h *handler) dataset(w http.ResponseWriter, r *http.Request) {
	st := h.open(w, r)
	if st == nil {
		return
	}
	defer st.Close()
	d, err := export.BuildAny(st, r.PathValue("name"), r.URL.Query().Get("issue"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wireDataset(d))
}

func (h *handler) reports(w http.ResponseWriter, r *http.Request) {
	if st := h.open(w, r); st != nil {
		st.Close()
		writeJSON(w, http.StatusOK, export.Reports())
	}
}

func (h *handler) report(w http.ResponseWriter, r *http.Request) {
	st := h.open(w, r)
	if st == nil {
		return
	}
	defer st.Close()
	h.writeReport(w, st, r.PathValue("name"))
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	st := h.open(w, r)
	if st == nil {
		return
	}
	defer st.Close()
	h.writeReport(w, st, "crawl_overview")
}

func (h *handler) writeReport(w http.ResponseWriter, st *store.Crawl, name string) {
	d, err := export.BuildReport(st, name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wireDataset(d))
}

func (h *handler) issues(w http.ResponseWriter, r *http.Request) {
	st := h.open(w, r)
	if st == nil {
		return
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	entries := make([]apiIssue, 0, len(counts))
	for id, n := range counts {
		if n == 0 {
			continue
		}
		def, _ := issues.Lookup(id)
		entries = append(entries, apiIssue{
			ID: id, Name: def.Name, Severity: string(def.Severity),
			Priority: string(def.Priority), Tab: def.Tab, Count: n,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	writeJSON(w, http.StatusOK, entries)
}

func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing url query parameter")
		return
	}
	st := h.open(w, r)
	if st == nil {
		return
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rec, ok := pages[url]
	if !ok {
		writeError(w, http.StatusNotFound, "URL not found in crawl: "+url)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
