package project

import (
	"database/sql"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// SiteScore is one site's row in the project scorecard.
type SiteScore struct {
	Domain  string `json:"domain"`
	Role    Role   `json:"role"`
	CrawlID string `json:"crawl_id,omitempty"`
	Status  string `json:"status"` // "ok" | "no-crawl"
	Started int64  `json:"started,omitempty"`

	// config badges (from the resolved crawl's frozen config)
	Rendering  string `json:"rendering,omitempty"`
	MaxDepth   int    `json:"max_depth"`
	MaxURLs    int    `json:"max_urls"`
	RobotsMode string `json:"robots_mode,omitempty"`
	Scoped     bool   `json:"scoped"`

	// core metrics
	URLs          int         `json:"urls"`
	Crawled       int         `json:"crawled"`
	IndexableRate float64     `json:"indexable_rate"`
	StatusBuckets map[int]int `json:"status_buckets,omitempty"`
	Errors        int         `json:"errors"`
	Warnings      int         `json:"warnings"`
	Opportunities int         `json:"opportunities"`
	AvgLinkScore  float64     `json:"avg_link_score"`
	NearDup       int         `json:"near_dup"`

	// optional (JSON-derived) tier
	HasOptional    bool    `json:"has_optional"`
	AvgWordCount   float64 `json:"avg_word_count,omitempty"`
	AvgFlesch      float64 `json:"avg_flesch,omitempty"`
	SchemaCoverage float64 `json:"schema_coverage,omitempty"`
}

// Scorecard is the cross-competitor metric comparison (Mode B): one row per
// project site, the main site first.
type Scorecard struct {
	ProjectID      string      `json:"project_id"`
	ProjectName    string      `json:"project_name"`
	Sites          []SiteScore `json:"sites"`
	ConfigDiverges bool        `json:"config_diverges"`
	DivergingDims  []string    `json:"diverging_dims,omitempty"`
}

// BuildScorecard computes the project scorecard from each site's LATEST
// comparable crawl. It runs read-only SQL aggregates over each crawl DB and
// never materializes pages (no LoadPages), so memory stays ~constant.
func (s *Store) BuildScorecard(projectID string, includeOptional bool) (*Scorecard, error) {
	p, err := s.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	members, err := s.Members(projectID)
	if err != nil {
		return nil, err
	}
	sc := &Scorecard{ProjectID: p.ID, ProjectName: p.Name}
	for _, m := range members {
		row := SiteScore{Domain: m.Domain, Role: m.Role, Status: "no-crawl"}
		latest, ok, err := s.LatestComparable(m.Domain)
		if err != nil {
			return nil, err
		}
		if ok {
			if err := s.scoreCrawl(&row, latest, includeOptional); err != nil {
				return nil, err
			}
		}
		sc.Sites = append(sc.Sites, row)
	}
	sc.ConfigDiverges, sc.DivergingDims = divergence(sc.Sites)
	return sc, nil
}

func (s *Store) scoreCrawl(row *SiteScore, sc SiteCrawl, includeOptional bool) error {
	st, err := store.OpenCrawl(s.dir, sc.ID)
	if err != nil {
		return err
	}
	defer st.Close()
	db := st.DB()
	row.Status = "ok"
	row.CrawlID = sc.ID
	row.Started = sc.Started.Unix()

	// config badges
	if yamlStr, err := st.Meta("config"); err == nil && yamlStr != "" {
		if cfg, err := config.Load([]byte(yamlStr)); err == nil {
			row.Rendering = cfg.Rendering.Mode
			row.MaxDepth = cfg.Limits.MaxDepth
			row.MaxURLs = cfg.Limits.MaxURLs
			row.RobotsMode = cfg.Robots.Mode
			row.Scoped = len(cfg.Scope.Exclude) > 0
		}
	}

	// core metrics: one single-pass aggregate over pages
	var urls, crawled, indexable, nearDup int
	var avgLink sql.NullFloat64
	err = db.QueryRow(`
		SELECT
		  COUNT(*),
		  COUNT(*) FILTER (WHERE state='crawled'),
		  COUNT(*) FILTER (WHERE indexable=1),
		  COUNT(*) FILTER (WHERE near_dup_count>0),
		  AVG(link_score)
		FROM pages`).Scan(&urls, &crawled, &indexable, &nearDup, &avgLink)
	if err != nil {
		return err
	}
	row.URLs = urls
	row.Crawled = crawled
	row.NearDup = nearDup
	if avgLink.Valid {
		row.AvgLinkScore = avgLink.Float64
	}
	if crawled > 0 {
		row.IndexableRate = float64(indexable) / float64(crawled)
	}

	// status-code buckets (2xx/3xx/4xx/5xx) over pages that got a response
	row.StatusBuckets = map[int]int{}
	rows, err := db.Query(`SELECT status_code/100 AS b, COUNT(*) FROM pages WHERE status_code > 0 GROUP BY b`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b, n int
		if err := rows.Scan(&b, &n); err != nil {
			return err
		}
		row.StatusBuckets[b] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// issues by severity: reuse IssueCounts (distinct affected URLs per issue)
	// and the catalogue's severity (kept in code, not the DB).
	counts, err := st.IssueCounts()
	if err != nil {
		return err
	}
	for id, n := range counts {
		def, ok := issues.Lookup(id)
		if !ok {
			continue
		}
		switch def.Severity {
		case issues.Issue:
			row.Errors += n
		case issues.Warning:
			row.Warnings += n
		case issues.Opportunity:
			row.Opportunities += n
		}
	}

	if includeOptional {
		row.HasOptional = true
		var wc, fl sql.NullFloat64
		_ = db.QueryRow(`SELECT AVG(json_extract(facts,'$.WordCount')), AVG(json_extract(facts,'$.Flesch'))
			FROM pages WHERE facts IS NOT NULL`).Scan(&wc, &fl)
		if wc.Valid {
			row.AvgWordCount = wc.Float64
		}
		if fl.Valid {
			row.AvgFlesch = fl.Float64
		}
		var cov sql.NullFloat64
		_ = db.QueryRow(`SELECT COUNT(*) FILTER (WHERE json_array_length(structured,'$.types') > 0) * 1.0
			/ NULLIF(COUNT(*) FILTER (WHERE state='crawled'), 0) FROM pages`).Scan(&cov)
		if cov.Valid {
			row.SchemaCoverage = cov.Float64
		}
	}
	return nil
}

// divergence flags config differences across scored sites on the fairness-
// critical dimensions (rendering mode, depth/URL caps, robots handling).
func divergence(sites []SiteScore) (bool, []string) {
	var scored []SiteScore
	for _, s := range sites {
		if s.Status == "ok" {
			scored = append(scored, s)
		}
	}
	if len(scored) < 2 {
		return false, nil
	}
	var dims []string
	differs := func(get func(SiteScore) string) bool {
		first := get(scored[0])
		for _, s := range scored[1:] {
			if get(s) != first {
				return true
			}
		}
		return false
	}
	if differs(func(s SiteScore) string { return s.Rendering }) {
		dims = append(dims, "rendering")
	}
	if differs(func(s SiteScore) string { return fmt.Sprint(s.MaxDepth) }) {
		dims = append(dims, "max_depth")
	}
	if differs(func(s SiteScore) string { return fmt.Sprint(s.MaxURLs) }) {
		dims = append(dims, "max_urls")
	}
	if differs(func(s SiteScore) string { return s.RobotsMode }) {
		dims = append(dims, "robots")
	}
	return len(dims) > 0, dims
}
