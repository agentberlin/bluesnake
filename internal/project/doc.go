// Package project is a fully self-contained, REMOVABLE layer that groups a main
// domain with its competitors for competitor study. It sits on top of the crawl
// abstraction and never modifies it.
//
// # Zero-core-change contract
//
// This package and its data are the ENTIRE feature. To remove the project
// feature, delete this package, its CLI/MCP/desktop registration lines, and the
// projects.db file — the rest of the product is byte-for-byte unchanged.
//
//   - Persistence: its OWN database file, <store-dir>/projects.db (see Store).
//     It never edits the crawl registry, any per-crawl database, their schemas,
//     or store's methods/models.
//   - Reads: only through store's public, read-only API — store.ListCrawls,
//     store.OpenCrawl, Crawl.DB(), Crawl.Meta, Crawl.IssueCounts, Crawl.LoadPages.
//   - Comparison: reuses internal/compare unchanged (Mode A, per-competitor over
//     time) and computes its own read-only SQL scorecard (Mode B, cross-competitor).
//
// # Model
//
// A Project is a main site plus competitor sites. A "site" is identified by its
// EXACT lowercased host[:port] (SiteKey): example.com, www.example.com,
// a.example.com and example.com:8080 are all distinct sites — no folding.
// Membership is by domain, never by crawl: a site's crawl history is resolved
// live from the registry (see SiteHistory), so a standalone crawl of a member
// domain auto-joins and a deleted crawl simply drops out. Nothing links a crawl
// to a project on disk.
package project
