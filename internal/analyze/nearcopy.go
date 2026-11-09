package analyze

import (
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/nearcopy"
)

// NearCopiesOn reports whether the config asks for the near-copy step: the
// content check on (content.near_duplicates.enabled, which also has the crawl
// store each page's signature) and the analysis not switched off
// (analysis.near_duplicates). It does not depend on analysis.auto — with the
// rest of the analysis off, finalize runs the step on its own.
func NearCopiesOn(cfg *config.Config) bool {
	return cfg.Analysis.NearDuplicates && cfg.Content.NearDuplicates.Enabled
}

// NearCopySettings is the step's settings under a config.
func NearCopySettings(cfg *config.Config) nearcopy.Settings {
	nd := cfg.Content.NearDuplicates
	return nearcopy.Settings{
		Threshold:       nd.Threshold,
		MinWords:        nd.MinWords,
		IndexableOnly:   nd.IndexableOnly,
		IgnorePaginated: cfg.Advanced.IgnorePaginatedForDuplicates,
	}
}
