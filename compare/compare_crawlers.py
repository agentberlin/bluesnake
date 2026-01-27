#!/usr/bin/env python3
"""
Crawler Comparison Script
Compares ScreamingFrog and BlueSnake crawler results
"""

import argparse
import csv
import json
import os
import re
import subprocess
import sys
import time
import unicodedata
import urllib.parse
from collections import defaultdict
from datetime import datetime
from difflib import SequenceMatcher
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple


class CrawlerComparison:
    def __init__(self, domain: str, bluesnake_only: bool = False, js_rendering: bool = False, skip_outlinks: bool = False):
        self.domain = domain
        self.bluesnake_only = bluesnake_only
        self.js_rendering = js_rendering
        self.skip_outlinks = skip_outlinks
        self.sf_output_dir = Path("/tmp/crawlertest/sf")
        self.bs_output_dir = Path("/tmp/crawlertest/bs")
        self.scream_executable = (
            "/Applications/Screaming Frog SEO Spider.app/Contents/MacOS/ScreamingFrogSEOSpiderLauncher"
        )
        self.config_path = "/Users/hhsecond/rendering.seospiderconfig"

        # For log and diff output
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        domain_safe = domain.replace("https://", "").replace("http://", "").replace("/", "_")
        self.sf_log_file = f"/tmp/screamingfrog_{domain_safe}_{timestamp}.log"
        self.bs_log_file = f"/tmp/bluesnake_{domain_safe}_{timestamp}.log"

        # For detailed diff output
        self.detailed_diff: Dict[str, Any] = {
            "metadata": {
                "domain": domain,
                "timestamp": datetime.now().isoformat(),
                "screamingfrog_log": self.sf_log_file,
                "bluesnake_log": self.bs_log_file,
            },
            "url_diffs": {},
            "status_diffs": {},
            "outlink_diffs": {},
            "page_attribute_diffs": {},
            "link_attribute_diffs": {},
            "content_diffs": {},
        }

    def should_filter_url(self, url: str) -> bool:
        """
        Check if a URL should be filtered from comparison.
        Returns True for URLs that should be excluded (RSC prefetch URLs, etc.)
        """
        url_lower = url.lower()

        # Filter Next.js RSC prefetch URLs (these are duplicates with cache-busting tokens)
        if "_rsc=" in url_lower:
            return True

        return False

    def categorize_resource_type(self, content_type: str) -> str:
        """Categorize content type into resource types"""
        ct = content_type.lower()

        if "text/html" in ct:
            return "html"
        elif any(img in ct for img in ["image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp", "image/svg"]):
            return "image"
        elif "text/css" in ct or "stylesheet" in ct:
            return "css"
        elif "javascript" in ct or "application/js" in ct or "text/js" in ct:
            return "javascript"
        elif "application/pdf" in ct:
            return "pdf"
        elif "font" in ct or "woff" in ct:
            return "font"
        elif "video" in ct:
            return "video"
        elif "audio" in ct:
            return "audio"
        elif any(doc in ct for doc in ["application/xml", "text/xml", "application/json"]):
            return "data"
        else:
            return "other"

    def run_screamingfrog(self) -> bool:
        """Run ScreamingFrog crawl"""
        # Create output directory
        self.sf_output_dir.mkdir(parents=True, exist_ok=True)

        # Ensure domain has protocol
        crawl_url = self.domain
        if not crawl_url.startswith(("http://", "https://")):
            crawl_url = f"https://{crawl_url}"

        # Build command
        args = [
            self.scream_executable,
            "--headless",
            "--overwrite",
            "--crawl",
            crawl_url,
            "--export-tabs",
            "Internal:All,Response Codes:All",
            "--bulk-export",
            "Web:All Page Text,All Page Source,Links:All Outlinks",
            "--output-folder",
            str(self.sf_output_dir),
            "--config",
            self.config_path,
        ]

        try:
            # Capture output to log file
            with open(self.sf_log_file, "w") as log_file:
                result = subprocess.run(args, stdout=log_file, stderr=subprocess.STDOUT, timeout=3600)

            if result.returncode != 0:
                print(f"ERROR: ScreamingFrog failed with return code {result.returncode}")
                print(f"Log file: {self.sf_log_file}")
                return False

            return True
        except subprocess.TimeoutExpired:
            print("ERROR: ScreamingFrog timed out after 1 hour")
            print(f"Log file: {self.sf_log_file}")
            return False
        except Exception as e:
            print(f"ERROR: Running ScreamingFrog: {e}")
            return False

    def check_prerequisites(self) -> Tuple[bool, List[str]]:
        """
        Check if all prerequisites are met before running comparison.
        Returns: (success, list of error messages)
        """
        errors = []

        # Check ScreamingFrog executable
        if not os.path.exists(self.scream_executable):
            errors.append(f"ScreamingFrog executable not found at: {self.scream_executable}")

        # Check ScreamingFrog config file
        if not os.path.exists(self.config_path):
            errors.append(f"ScreamingFrog config file not found at: {self.config_path}")

        # Check Go is available for CLI
        try:
            result = subprocess.run(["go", "version"], capture_output=True, timeout=5)
            if result.returncode != 0:
                errors.append("Go is not properly installed or not in PATH")
        except FileNotFoundError:
            errors.append("Go is not installed or not in PATH")
        except Exception as e:
            errors.append(f"Error checking Go installation: {e}")

        return len(errors) == 0, errors

    def run_bluesnake_cli(self) -> Tuple[bool, Optional[int], Optional[int]]:
        """
        Run BlueSnake crawl via CLI.
        Returns: (success, project_id, crawl_id)
        """
        # Create output directory
        self.bs_output_dir.mkdir(parents=True, exist_ok=True)

        # Ensure domain has protocol
        crawl_url = self.domain
        if not crawl_url.startswith(("http://", "https://")):
            crawl_url = f"https://{crawl_url}"

        # Build CLI command
        args = [
            "go", "run", "./cmd/cli",
            "crawl", crawl_url,
            "--parallelism", "10",
            "--user-agent", "Mozilla/5.0 (compatible; BlueSnake/1.0)",
            "--include-subdomains",
            "--spider",
            "--sitemap",
            "--check-external",
            "--format", "json",
            "--export-links",
            "--export-content",
            "--output", str(self.bs_output_dir),
        ]

        if self.js_rendering:
            args.append("--js-rendering")

        print(f"Running BlueSnake CLI: {' '.join(args)}")

        try:
            # Run CLI and capture output
            with open(self.bs_log_file, "w") as log_file:
                result = subprocess.run(
                    args,
                    stdout=log_file,
                    stderr=subprocess.STDOUT,
                    timeout=3600,
                    cwd=Path(__file__).parent.parent,  # Run from project root
                )

            if result.returncode != 0:
                print(f"ERROR: BlueSnake CLI failed with return code {result.returncode}")
                print(f"Log file: {self.bs_log_file}")
                # Print last 20 lines of log
                with open(self.bs_log_file, "r") as f:
                    lines = f.readlines()
                    print("Last 20 lines of log:")
                    for line in lines[-20:]:
                        print(f"  {line.rstrip()}")
                return False, None, None

            # Parse crawl summary for IDs
            summary_file = self.bs_output_dir / "crawl_summary.json"
            if summary_file.exists():
                with open(summary_file, "r") as f:
                    summary = json.load(f)
                    project_id = summary.get("projectId", 0)
                    crawl_id = summary.get("crawlId", 0)
                    return True, project_id, crawl_id

            # If no summary file, try to extract from log
            print("WARNING: crawl_summary.json not found, crawl IDs unavailable")
            return True, 0, 0

        except subprocess.TimeoutExpired:
            print("ERROR: BlueSnake CLI timed out after 1 hour")
            print(f"Log file: {self.bs_log_file}")
            return False, None, None
        except Exception as e:
            print(f"ERROR: Running BlueSnake CLI: {e}")
            return False, None, None

    def load_bluesnake_data(self) -> Optional[Dict[str, Any]]:
        """Load crawl results from BlueSnake CLI output files"""
        internal_file = self.bs_output_dir / "internal_all.json"
        if not internal_file.exists():
            print(f"ERROR: {internal_file} not found")
            return None

        try:
            with open(internal_file, "r") as f:
                data = json.load(f)

            # The CLI outputs {"crawlId": ..., "results": [...]}
            # Extract the results array for comparison
            if isinstance(data, dict) and "results" in data:
                return {"results": data["results"]}
            # Fallback for raw list format
            elif isinstance(data, list):
                return {"results": data}
            else:
                print(f"ERROR: Unexpected BlueSnake data format")
                return None
        except Exception as e:
            print(f"ERROR: Loading BlueSnake data: {e}")
            return None

    def load_bluesnake_links(self) -> Dict[str, List[Dict[str, Any]]]:
        """Load all outlinks from BlueSnake CLI output"""
        outlinks_file = self.bs_output_dir / "all_outlinks.json"
        if not outlinks_file.exists():
            print(f"WARNING: {outlinks_file} not found (run with --export-links)")
            return {}

        try:
            with open(outlinks_file, "r") as f:
                data = json.load(f)

            # Handle CLI format: {"crawlId": ..., "links": [...]}
            if isinstance(data, dict) and "links" in data:
                links = data["links"]
            elif isinstance(data, list):
                links = data
            else:
                print(f"WARNING: Unexpected outlinks format")
                return {}

            # Group by source URL
            # CLI uses sourceUrl/targetUrl, older format uses from/to
            outlinks_by_source: Dict[str, List[Dict[str, Any]]] = defaultdict(list)
            for link in links:
                # Support both field naming conventions
                source = link.get("sourceUrl") or link.get("from", "")
                if source:
                    outlinks_by_source[source].append({
                        "url": link.get("targetUrl") or link.get("to", ""),
                        "anchor": link.get("linkText") or link.get("anchor", ""),
                        "linkType": link.get("linkType", ""),
                        "follow": link.get("follow", True),
                        "target": link.get("target", ""),
                        "rel": link.get("rel", ""),
                        "pathType": link.get("pathType", ""),
                        "position": link.get("position", ""),
                    })

            return outlinks_by_source
        except Exception as e:
            print(f"ERROR: Loading BlueSnake links: {e}")
            return {}

    def parse_screamingfrog_internal(self) -> Dict[str, Dict[str, Any]]:
        """Parse ScreamingFrog Internal:All export"""
        internal_file = self.sf_output_dir / "internal_all.csv"
        if not internal_file.exists():
            print(f"ERROR: {internal_file} not found")
            return {}

        urls = {}
        filtered_count = 0
        with open(internal_file, "r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            for row in reader:
                url = row.get("Address", "")
                if url:
                    # Filter URLs that should be excluded
                    if self.should_filter_url(url):
                        filtered_count += 1
                        continue

                    # Parse depth as int
                    depth_str = row.get("Crawl Depth", "0")
                    depth = int(depth_str) if depth_str and depth_str.isdigit() else 0

                    # Parse word count as int
                    word_count_str = row.get("Word Count", "0")
                    word_count = int(word_count_str) if word_count_str and word_count_str.isdigit() else 0

                    urls[url] = {
                        "status": int(row.get("Status Code", 0)) if row.get("Status Code") else 0,
                        "content_type": row.get("Content Type", ""),
                        "title": row.get("Title 1", ""),
                        "meta_description": row.get("Meta Description 1", ""),
                        "h1": row.get("H1-1", ""),
                        "h2": row.get("H2-1", ""),
                        "canonical": row.get("Canonical Link Element 1", ""),
                        "word_count": word_count,
                        "indexable": row.get("Indexability", ""),
                        "depth": depth,
                    }

        return urls

    def parse_screamingfrog_outlinks(self) -> Dict[str, List[Dict[str, Any]]]:
        """Parse ScreamingFrog All Outlinks export"""
        outlinks_file = self.sf_output_dir / "all_outlinks.csv"
        if not outlinks_file.exists():
            print(f"ERROR: {outlinks_file} not found")
            return {}

        outlinks = defaultdict(list)
        filtered_outlinks = 0
        with open(outlinks_file, "r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            for row in reader:
                source = row.get("Source", "")
                target = row.get("Destination", "")
                if source and target:
                    # Filter outlinks where source or target should be excluded
                    if self.should_filter_url(source) or self.should_filter_url(target):
                        filtered_outlinks += 1
                        continue

                    # Parse follow as boolean (SF uses "true"/"false" strings)
                    follow_str = row.get("Follow", "true").lower()
                    follow = follow_str == "true"

                    outlinks[source].append({
                        "to": target,
                        "anchor": row.get("Anchor", "") or row.get("Alt Text", ""),
                        "type": row.get("Type", ""),
                        "follow": follow,
                        "target": row.get("Target", ""),
                        "rel": row.get("Rel", ""),
                        "path_type": row.get("Path Type", ""),
                        "position": row.get("Link Position", ""),
                    })

        return outlinks

    def normalize_url(self, url: str) -> str:
        """Normalize URL for comparison"""
        # Remove trailing slash for comparison
        url = url.rstrip("/")
        # Decode URL
        url = urllib.parse.unquote(url)
        return url

    def compare_urls(self, sf_urls: Dict[str, Dict[str, Any]], bs_data: Dict[str, Any]) -> Dict[str, Any]:
        """Compare crawled URLs between ScreamingFrog and BlueSnake"""
        bs_results = bs_data.get("results", [])

        # Build URL maps with resource types
        sf_by_type = defaultdict(set)
        bs_by_type = defaultdict(set)

        for url, info in sf_urls.items():
            norm_url = self.normalize_url(url)
            resource_type = self.categorize_resource_type(info.get("content_type", ""))
            sf_by_type[resource_type].add(norm_url)

        for result in bs_results:
            norm_url = self.normalize_url(result["url"])
            resource_type = self.categorize_resource_type(result.get("contentType", ""))
            bs_by_type[resource_type].add(norm_url)

        # Calculate total sets
        sf_set = set()
        for urls in sf_by_type.values():
            sf_set.update(urls)

        bs_set = set()
        for urls in bs_by_type.values():
            bs_set.update(urls)

        common = sf_set & bs_set

        # Calculate differences by type
        total_missing = 0
        missing_by_type = {}

        for resource_type in sorted(set(list(sf_by_type.keys()) + list(bs_by_type.keys()))):
            missing = sf_by_type.get(resource_type, set()) - bs_by_type.get(resource_type, set())
            missing_count = len(missing)

            total_missing += missing_count
            if missing_count > 0:
                missing_by_type[resource_type] = list(missing)

        # Store detailed diff
        self.detailed_diff["url_diffs"] = {
            "missing_in_bluesnake_by_type": missing_by_type,
            "only_in_bluesnake": list(bs_set - sf_set),
            "sf_by_type": {k: len(v) for k, v in sf_by_type.items()},
            "bs_by_type": {k: len(v) for k, v in bs_by_type.items()},
        }

        return {
            "sf_total": len(sf_set),
            "bs_total": len(bs_set),
            "common": len(common),
            "missing_in_bs": total_missing,
            "sf_by_type": sf_by_type,
            "bs_by_type": bs_by_type,
        }

    def compare_status_codes(self, sf_urls: Dict[str, Dict[str, Any]], bs_data: Dict[str, Any]) -> Dict[str, int]:
        """Compare HTTP status codes"""
        bs_results = bs_data.get("results", [])

        # Build status maps
        sf_status_map = {}
        for url, info in sf_urls.items():
            norm_url = self.normalize_url(url)
            sf_status_map[norm_url] = info.get("status", 0)

        bs_status_map = {}
        for result in bs_results:
            norm_url = self.normalize_url(result["url"])
            bs_status_map[norm_url] = result.get("status", 0)

        # Find common URLs with different status codes
        status_diffs = []
        for url in set(sf_status_map.keys()) & set(bs_status_map.keys()):
            sf_status = sf_status_map[url]
            bs_status = bs_status_map[url]
            if sf_status != bs_status:
                status_diffs.append(
                    {
                        "url": url,
                        "sf_status": sf_status,
                        "bs_status": bs_status,
                    }
                )

        # Store detailed diff
        self.detailed_diff["status_diffs"] = status_diffs

        return {
            "diff_count": len(status_diffs),
        }

    def compare_outlinks(
        self, sf_outlinks: Dict[str, List[Dict[str, str]]], bs_outlinks: Dict[str, List[Dict[str, Any]]], bs_results: List[Dict[str, Any]]
    ) -> Dict[str, int]:
        """Compare outlinks for ALL pages"""
        outlink_diffs = []
        checked_count = 0

        for result in bs_results:
            url = result["url"]
            norm_url = self.normalize_url(url)

            # Get BlueSnake outlinks from loaded data
            bs_links = bs_outlinks.get(url, []) or bs_outlinks.get(norm_url, [])
            if not bs_links:
                # Try with trailing slash variants
                bs_links = bs_outlinks.get(url + "/", []) or bs_outlinks.get(norm_url + "/", [])

            checked_count += 1

            # Get ScreamingFrog outlinks
            sf_out = set(self.normalize_url(link["to"]) for link in sf_outlinks.get(norm_url, []))
            bs_out = set(self.normalize_url(link["url"]) for link in bs_links)

            if sf_out != bs_out:
                only_sf = sf_out - bs_out
                only_bs = bs_out - sf_out
                outlink_diffs.append(
                    {
                        "url": url,
                        "sf_count": len(sf_out),
                        "bs_count": len(bs_out),
                        "only_in_sf": list(only_sf),
                        "only_in_bs": list(only_bs),
                    }
                )

        # Store detailed diff
        self.detailed_diff["outlink_diffs"] = outlink_diffs

        return {
            "checked_count": checked_count,
            "diff_count": len(outlink_diffs),
        }

    def compare_page_attributes(
        self, sf_urls: Dict[str, Dict[str, Any]], bs_data: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Compare page attributes (depth, title, h1, h2, wordCount, indexable, canonical)"""
        bs_results = bs_data.get("results", [])

        # Build BS lookup map
        bs_map = {}
        for result in bs_results:
            norm_url = self.normalize_url(result["url"])
            bs_map[norm_url] = result

        attribute_diffs = {
            "depth": [],
            "title": [],
            "h1": [],
            "word_count": [],
            "indexable": [],
            "canonical": [],
        }

        for url, sf_info in sf_urls.items():
            norm_url = self.normalize_url(url)
            if norm_url not in bs_map:
                continue

            bs_info = bs_map[norm_url]

            # Compare depth
            sf_depth = sf_info.get("depth", 0)
            bs_depth = bs_info.get("depth", 0)
            if sf_depth != bs_depth:
                attribute_diffs["depth"].append({
                    "url": url,
                    "sf": sf_depth,
                    "bs": bs_depth,
                })

            # Compare title (normalize whitespace)
            sf_title = (sf_info.get("title") or "").strip()
            bs_title = (bs_info.get("title") or "").strip()
            if sf_title != bs_title:
                attribute_diffs["title"].append({
                    "url": url,
                    "sf": sf_title[:100],
                    "bs": bs_title[:100],
                })

            # Compare H1
            sf_h1 = (sf_info.get("h1") or "").strip()
            bs_h1 = (bs_info.get("h1") or "").strip()
            if sf_h1 != bs_h1:
                attribute_diffs["h1"].append({
                    "url": url,
                    "sf": sf_h1[:100],
                    "bs": bs_h1[:100],
                })

            # Compare word count (allow 10% tolerance for minor differences)
            sf_wc = sf_info.get("word_count", 0)
            bs_wc = bs_info.get("wordCount", 0)
            if sf_wc > 0 or bs_wc > 0:
                diff_pct = abs(sf_wc - bs_wc) / max(sf_wc, bs_wc, 1) * 100
                if diff_pct > 10:  # More than 10% difference
                    attribute_diffs["word_count"].append({
                        "url": url,
                        "sf": sf_wc,
                        "bs": bs_wc,
                        "diff_pct": round(diff_pct, 1),
                    })

            # Compare indexable (normalize values)
            sf_idx = (sf_info.get("indexable") or "").strip().lower()
            bs_idx = (bs_info.get("indexable") or "").strip().lower()
            # Normalize: SF uses "Indexable"/"Non-Indexable", BS uses "Yes"/"No, ..."
            # For non-HTML resources, BS uses "-" which should match SF's "Indexable"
            content_type = sf_info.get("content_type", "").lower()
            is_html = "text/html" in content_type

            if not is_html:
                # Non-HTML resources: skip comparison (indexability doesn't apply)
                pass
            else:
                sf_is_indexable = sf_idx == "indexable"
                bs_is_indexable = bs_idx == "yes"
                if sf_is_indexable != bs_is_indexable:
                    attribute_diffs["indexable"].append({
                        "url": url,
                        "sf": sf_info.get("indexable", ""),
                        "bs": bs_info.get("indexable", ""),
                    })

            # Compare canonical
            sf_can = self.normalize_url(sf_info.get("canonical") or "")
            bs_can = self.normalize_url(bs_info.get("canonicalUrl") or "")
            if sf_can and bs_can and sf_can != bs_can:
                attribute_diffs["canonical"].append({
                    "url": url,
                    "sf": sf_can,
                    "bs": bs_can,
                })

        # Store in detailed diff
        self.detailed_diff["page_attribute_diffs"] = attribute_diffs

        return {
            "depth_diffs": len(attribute_diffs["depth"]),
            "title_diffs": len(attribute_diffs["title"]),
            "h1_diffs": len(attribute_diffs["h1"]),
            "word_count_diffs": len(attribute_diffs["word_count"]),
            "indexable_diffs": len(attribute_diffs["indexable"]),
            "canonical_diffs": len(attribute_diffs["canonical"]),
        }

    def compare_link_attributes(
        self, sf_outlinks: Dict[str, List[Dict[str, Any]]], bs_outlinks: Dict[str, List[Dict[str, Any]]], bs_results: List[Dict[str, Any]]
    ) -> Dict[str, Any]:
        """Compare link attributes (follow, rel, target, pathType, position, linkType)"""
        attribute_diffs = {
            "follow": [],
            "rel": [],
            "target": [],
            "path_type": [],
            "position": [],
            "link_type": [],
        }
        checked_links = 0

        for result in bs_results:
            url = result["url"]
            norm_url = self.normalize_url(url)

            # Get BlueSnake outlinks from loaded data
            bs_links = bs_outlinks.get(url, []) or bs_outlinks.get(norm_url, [])
            if not bs_links:
                bs_links = bs_outlinks.get(url + "/", []) or bs_outlinks.get(norm_url + "/", [])

            sf_outlinks_for_page = sf_outlinks.get(norm_url, [])

            # Build lookup maps by destination URL
            bs_links_map = {}
            for link in bs_links:
                dest = self.normalize_url(link.get("url", ""))
                bs_links_map[dest] = link

            sf_links_map = {}
            for link in sf_outlinks_for_page:
                dest = self.normalize_url(link.get("to", ""))
                sf_links_map[dest] = link

            # Compare common links
            common_dests = set(bs_links_map.keys()) & set(sf_links_map.keys())
            checked_links += len(common_dests)

            for dest in common_dests:
                sf_link = sf_links_map[dest]
                bs_link = bs_links_map[dest]

                # Get link type early for use in normalization
                sf_type = sf_link.get("type", "").lower()

                # Compare follow
                sf_follow = sf_link.get("follow", True)
                bs_follow = bs_link.get("follow", True)
                if sf_follow != bs_follow:
                    attribute_diffs["follow"].append({
                        "source": url,
                        "dest": dest,
                        "sf": sf_follow,
                        "bs": bs_follow,
                    })

                # Compare target
                sf_target = sf_link.get("target", "")
                bs_target = bs_link.get("target", "")
                if sf_target != bs_target:
                    attribute_diffs["target"].append({
                        "source": url,
                        "dest": dest,
                        "sf": sf_target,
                        "bs": bs_target,
                    })

                # Compare rel
                sf_rel = sf_link.get("rel", "")
                bs_rel = bs_link.get("rel", "")
                if sf_rel != bs_rel:
                    attribute_diffs["rel"].append({
                        "source": url,
                        "dest": dest,
                        "sf": sf_rel,
                        "bs": bs_rel,
                    })

                # Compare path type
                sf_path_type = sf_link.get("path_type", "")
                bs_path_type = bs_link.get("pathType", "")
                if sf_path_type != bs_path_type:
                    attribute_diffs["path_type"].append({
                        "source": url,
                        "dest": dest,
                        "sf": sf_path_type,
                        "bs": bs_path_type,
                    })

                # Compare position (normalize values)
                sf_pos = sf_link.get("position", "").lower()
                bs_pos = bs_link.get("position", "").lower()
                # Normalize: For non-anchor elements (scripts, stylesheets, images),
                # SF uses "Head"/"Content" but BS uses "unknown"
                sf_pos_norm = sf_pos
                if sf_type in ("javascript", "css", "image") and sf_pos in ("head", "content"):
                    sf_pos_norm = "unknown"
                # Normalize case for common positions
                if sf_pos_norm != bs_pos:
                    attribute_diffs["position"].append({
                        "source": url,
                        "dest": dest,
                        "sf": sf_link.get("position", ""),
                        "bs": bs_link.get("position", ""),
                    })

                # Compare link type (normalize naming conventions)
                bs_type = bs_link.get("linkType", "").lower()
                # Normalize: SF uses different names than BS
                sf_type_norm = sf_type
                if sf_type == "hyperlink":
                    sf_type_norm = "anchor"
                elif sf_type == "javascript":
                    sf_type_norm = "script"
                elif sf_type == "css":
                    sf_type_norm = "stylesheet"
                if sf_type_norm != bs_type:
                    attribute_diffs["link_type"].append({
                        "source": url,
                        "dest": dest,
                        "sf": sf_link.get("type", ""),
                        "bs": bs_link.get("linkType", ""),
                    })

        # Store in detailed diff
        self.detailed_diff["link_attribute_diffs"] = attribute_diffs

        return {
            "checked_links": checked_links,
            "follow_diffs": len(attribute_diffs["follow"]),
            "target_diffs": len(attribute_diffs["target"]),
            "rel_diffs": len(attribute_diffs["rel"]),
            "path_type_diffs": len(attribute_diffs["path_type"]),
            "position_diffs": len(attribute_diffs["position"]),
            "link_type_diffs": len(attribute_diffs["link_type"]),
        }

    def normalize_text(self, text: str) -> str:
        """
        Normalize text for comparison.
        Handles whitespace, unicode, case, and common extraction differences.
        """
        if not text:
            return ""

        # Unicode normalization (NFC form)
        text = unicodedata.normalize("NFC", text)

        # Convert to lowercase
        text = text.lower()

        # Replace common unicode variants
        replacements = {
            "\u2018": "'",  # Left single quote
            "\u2019": "'",  # Right single quote
            "\u201c": '"',  # Left double quote
            "\u201d": '"',  # Right double quote
            "\u2013": "-",  # En dash
            "\u2014": "-",  # Em dash
            "\u2026": "...",  # Ellipsis
            "\u00a0": " ",  # Non-breaking space
            "\u00ad": "",   # Soft hyphen
            "\t": " ",      # Tab to space
            "\r": " ",      # Carriage return
            "\n": " ",      # Newline to space
        }
        for old, new in replacements.items():
            text = text.replace(old, new)

        # Collapse multiple spaces to single space
        text = re.sub(r"\s+", " ", text)

        # Remove leading/trailing whitespace
        text = text.strip()

        return text

    def tokenize(self, text: str) -> List[str]:
        """
        Tokenize text into words.
        Handles punctuation and common edge cases.
        """
        if not text:
            return []

        # Split on whitespace and punctuation (but keep hyphenated words)
        # This regex splits on spaces and most punctuation but keeps hyphens within words
        tokens = re.findall(r"\b[\w'-]+\b", text, re.UNICODE)

        # Filter out empty tokens and single punctuation
        tokens = [t for t in tokens if len(t) > 0 and not t in ("'", "-")]

        return tokens

    def url_to_sf_filename(self, url: str, domain: str) -> str:
        """
        Convert URL to ScreamingFrog page_text filename.
        Pattern: original_https_<domain>_<path>.txt
        """
        # Parse URL
        parsed = urllib.parse.urlparse(url)

        # Build the filename parts
        scheme = parsed.scheme or "https"
        host = parsed.netloc or domain

        # Get path, removing leading slash
        path = parsed.path.lstrip("/")

        # Combine all parts
        if path:
            filename = f"original_{scheme}_{host}_{path}.txt"
        else:
            filename = f"original_{scheme}_{host}_.txt"

        # Replace path separators
        filename = filename.replace("/", "_")

        return filename

    def url_to_bs_filename(self, url: str) -> str:
        """
        Convert URL to BlueSnake content filename.
        Pattern: <path_with_underscores>.txt
        """
        parsed = urllib.parse.urlparse(url)

        path = parsed.path
        query = parsed.query

        # Combine path and query
        full_path = path
        if query:
            full_path = f"{path}?{query}"

        # Handle root path
        if full_path == "" or full_path == "/":
            return "index.txt"

        # Remove leading slash
        full_path = full_path.lstrip("/")

        # Replace non-disk-friendly characters
        replacements = [
            ("/", "_"),
            ("?", "_"),
            ("=", "_"),
            ("&", "_"),
            ("#", "_"),
            (":", "_"),
            ("*", "_"),
            ('"', "_"),
            ("<", "_"),
            (">", "_"),
            ("|", "_"),
            (" ", "_"),
        ]
        for old, new in replacements:
            full_path = full_path.replace(old, new)

        # Add .txt extension if not present
        if not full_path.endswith(".txt"):
            full_path = full_path + ".txt"

        return full_path

    def load_content_files(self) -> Tuple[Dict[str, str], Dict[str, str]]:
        """
        Load content files from both crawlers.
        Returns: (sf_content, bs_content) - dicts mapping URL to text content
        """
        sf_content: Dict[str, str] = {}
        bs_content: Dict[str, str] = {}

        # Load ScreamingFrog page text
        sf_page_text_dir = self.sf_output_dir / "page_text"
        if sf_page_text_dir.exists():
            for txt_file in sf_page_text_dir.glob("*.txt"):
                try:
                    content = txt_file.read_text(encoding="utf-8", errors="replace")
                    # Extract URL from filename
                    # Pattern: original_https_domain_path.txt
                    filename = txt_file.name
                    if filename.startswith("original_"):
                        # Remove "original_" prefix and ".txt" suffix
                        url_part = filename[9:-4]  # Remove "original_" and ".txt"
                        # Split on first underscore to get scheme
                        parts = url_part.split("_", 1)
                        if len(parts) >= 2:
                            scheme = parts[0]
                            rest = parts[1]
                            # The domain is everything up to the first path segment
                            # This is tricky because domain can have dots
                            # We'll reconstruct the URL
                            # Replace underscores with slashes for path
                            # The domain ends where the path starts
                            # For now, store with filename as key and try to match later
                            url = f"{scheme}://{rest.replace('_', '/')}"
                            sf_content[url] = content
                except Exception as e:
                    print(f"Warning: Failed to load SF content file {txt_file.name}: {e}")

        # Load BlueSnake content
        bs_content_dir = self.bs_output_dir / "content"
        if bs_content_dir.exists():
            for txt_file in bs_content_dir.glob("*.txt"):
                try:
                    content = txt_file.read_text(encoding="utf-8", errors="replace")
                    # Store with filename as key for matching
                    bs_content[txt_file.name] = content
                except Exception as e:
                    print(f"Warning: Failed to load BS content file {txt_file.name}: {e}")

        return sf_content, bs_content

    def compute_content_metrics(self, text_a: str, text_b: str) -> Dict[str, Any]:
        """
        Compute multiple similarity metrics between two texts.
        """
        # Normalize both texts
        norm_a = self.normalize_text(text_a)
        norm_b = self.normalize_text(text_b)

        # Tokenize
        tokens_a = set(self.tokenize(norm_a))
        tokens_b = set(self.tokenize(norm_b))

        # Handle empty cases
        if not tokens_a and not tokens_b:
            return {
                "jaccard": 1.0,
                "overlap": 1.0,
                "dice": 1.0,
                "sequence_ratio": 1.0,
                "word_count_a": 0,
                "word_count_b": 0,
                "common_words": 0,
                "unique_to_a": 0,
                "unique_to_b": 0,
            }

        if not tokens_a or not tokens_b:
            return {
                "jaccard": 0.0,
                "overlap": 0.0,
                "dice": 0.0,
                "sequence_ratio": 0.0,
                "word_count_a": len(tokens_a),
                "word_count_b": len(tokens_b),
                "common_words": 0,
                "unique_to_a": len(tokens_a),
                "unique_to_b": len(tokens_b),
            }

        # Set operations
        intersection = tokens_a & tokens_b
        union = tokens_a | tokens_b

        # Compute metrics
        jaccard = len(intersection) / len(union) if union else 1.0
        overlap = len(intersection) / min(len(tokens_a), len(tokens_b))
        dice = 2 * len(intersection) / (len(tokens_a) + len(tokens_b))

        # Sequence ratio (more expensive, use normalized text)
        # Limit length to avoid very slow comparisons
        max_len = 50000
        seq_a = norm_a[:max_len]
        seq_b = norm_b[:max_len]
        sequence_ratio = SequenceMatcher(None, seq_a, seq_b).ratio()

        return {
            "jaccard": round(jaccard, 4),
            "overlap": round(overlap, 4),
            "dice": round(dice, 4),
            "sequence_ratio": round(sequence_ratio, 4),
            "word_count_a": len(tokens_a),
            "word_count_b": len(tokens_b),
            "common_words": len(intersection),
            "unique_to_a": len(tokens_a - tokens_b),
            "unique_to_b": len(tokens_b - tokens_a),
        }

    def compare_content(
        self, sf_urls: Dict[str, Dict[str, Any]], bs_data: Dict[str, Any]
    ) -> Dict[str, Any]:
        """
        Compare text content between ScreamingFrog and BlueSnake.
        """
        bs_results = bs_data.get("results", [])

        # Load content files
        sf_content_raw, bs_content_raw = self.load_content_files()

        # Build URL to content mappings
        # For BS: map URL to content using filename conversion
        bs_url_to_content: Dict[str, str] = {}
        for result in bs_results:
            url = result["url"]
            bs_filename = self.url_to_bs_filename(url)
            if bs_filename in bs_content_raw:
                bs_url_to_content[url] = bs_content_raw[bs_filename]

        # For SF: we need to match URLs more flexibly
        # The sf_content_raw keys are reconstructed URLs that may not match exactly
        sf_url_to_content: Dict[str, str] = {}
        sf_page_text_dir = self.sf_output_dir / "page_text"

        if sf_page_text_dir.exists():
            for url in sf_urls.keys():
                # Try to find matching SF file
                sf_filename = self.url_to_sf_filename(url, self.domain.replace("https://", "").replace("http://", ""))
                sf_filepath = sf_page_text_dir / sf_filename
                if sf_filepath.exists():
                    try:
                        sf_url_to_content[url] = sf_filepath.read_text(encoding="utf-8", errors="replace")
                    except Exception:
                        pass

        # Find common URLs with content
        sf_urls_with_content = set(sf_url_to_content.keys())
        bs_urls_with_content = set(bs_url_to_content.keys())

        # Normalize URLs for comparison
        sf_norm_to_orig: Dict[str, str] = {self.normalize_url(u): u for u in sf_urls_with_content}
        bs_norm_to_orig: Dict[str, str] = {self.normalize_url(u): u for u in bs_urls_with_content}

        common_norm_urls = set(sf_norm_to_orig.keys()) & set(bs_norm_to_orig.keys())

        # Compare content for common URLs
        content_comparisons: List[Dict[str, Any]] = []
        total_jaccard = 0.0
        total_overlap = 0.0
        total_dice = 0.0
        total_sequence = 0.0

        for norm_url in common_norm_urls:
            sf_url = sf_norm_to_orig[norm_url]
            bs_url = bs_norm_to_orig[norm_url]

            sf_text = sf_url_to_content.get(sf_url, "")
            bs_text = bs_url_to_content.get(bs_url, "")

            metrics = self.compute_content_metrics(sf_text, bs_text)
            metrics["url"] = sf_url

            content_comparisons.append(metrics)

            total_jaccard += metrics["jaccard"]
            total_overlap += metrics["overlap"]
            total_dice += metrics["dice"]
            total_sequence += metrics["sequence_ratio"]

        # Calculate averages
        n = len(content_comparisons) if content_comparisons else 1
        avg_jaccard = total_jaccard / n
        avg_overlap = total_overlap / n
        avg_dice = total_dice / n
        avg_sequence = total_sequence / n

        # Find pages with low similarity (potential issues)
        low_similarity_pages = [
            c for c in content_comparisons
            if c["jaccard"] < 0.7  # Less than 70% token overlap
        ]

        # Sort by jaccard (lowest first)
        low_similarity_pages.sort(key=lambda x: x["jaccard"])

        # Store detailed comparison in diff
        self.detailed_diff["content_diffs"] = {
            "comparisons": content_comparisons,
            "low_similarity_pages": low_similarity_pages[:20],  # Top 20 worst
        }

        return {
            "sf_pages_with_content": len(sf_urls_with_content),
            "bs_pages_with_content": len(bs_urls_with_content),
            "common_pages_compared": len(content_comparisons),
            "avg_jaccard": round(avg_jaccard, 4),
            "avg_overlap": round(avg_overlap, 4),
            "avg_dice": round(avg_dice, 4),
            "avg_sequence_ratio": round(avg_sequence, 4),
            "low_similarity_count": len(low_similarity_pages),
            "low_similarity_pages": low_similarity_pages[:5],  # Top 5 for summary
        }

    def write_detailed_diff(self) -> Tuple[str, int]:
        """Write detailed diff to JSON file"""
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        domain_safe = self.domain.replace("https://", "").replace("http://", "").replace("/", "_")
        filename = f"/tmp/crawler_diff_{domain_safe}_{timestamp}.json"

        with open(filename, "w") as f:
            json.dump(self.detailed_diff, f, indent=2)

        file_size = os.path.getsize(filename)
        return filename, file_size

    def run(self):
        """Run the full comparison"""
        # Check prerequisites
        success, errors = self.check_prerequisites()
        if not success:
            print("=" * 80)
            print("CRAWLER COMPARISON FAILED - PREREQUISITES NOT MET")
            print("=" * 80)
            for error in errors:
                print(f"  - {error}")
            print("=" * 80)
            return False

        # Step 1: Run ScreamingFrog (skip if bluesnake-only mode)
        if not self.bluesnake_only:
            print("\n[1/5] Running ScreamingFrog crawl...")
            if not self.run_screamingfrog():
                print("ERROR: ScreamingFrog crawl failed")
                return False
            print("ScreamingFrog crawl completed.")
        else:
            print("\n[1/5] Skipping ScreamingFrog (--bluesnake-only mode)")

        # Step 2: Run BlueSnake CLI
        print("\n[2/5] Running BlueSnake CLI crawl...")
        success, project_id, crawl_id = self.run_bluesnake_cli()
        if not success:
            print("ERROR: BlueSnake crawl failed")
            return False
        print(f"BlueSnake crawl completed. Project ID: {project_id}, Crawl ID: {crawl_id}")

        # Step 3: Load BlueSnake data from output files
        print("\n[3/5] Loading BlueSnake data...")
        bs_data = self.load_bluesnake_data()
        if not bs_data:
            print("ERROR: Failed to load BlueSnake data")
            return False

        if not self.skip_outlinks:
            bs_outlinks = self.load_bluesnake_links()
            print(f"Loaded {len(bs_data.get('results', []))} pages and links from {len(bs_outlinks)} sources")
        else:
            bs_outlinks = {}
            print(f"Loaded {len(bs_data.get('results', []))} pages (skipping outlinks)")

        # Step 4: Parse ScreamingFrog data
        print("\n[4/5] Parsing ScreamingFrog data...")
        sf_urls = self.parse_screamingfrog_internal()
        if not self.skip_outlinks:
            sf_outlinks = self.parse_screamingfrog_outlinks()
            print(f"Parsed {len(sf_urls)} URLs and links from {len(sf_outlinks)} sources")
        else:
            sf_outlinks = {}
            print(f"Parsed {len(sf_urls)} URLs (skipping outlinks)")

        # Step 5: Compare
        print("\n[5/5] Comparing results...")

        # Compare URLs
        url_comparison = self.compare_urls(sf_urls, bs_data)

        # Compare status codes
        status_comparison = self.compare_status_codes(sf_urls, bs_data)

        # Compare outlinks
        outlink_comparison = self.compare_outlinks(sf_outlinks, bs_outlinks, bs_data.get("results", []))

        # Compare page attributes
        page_attr_comparison = self.compare_page_attributes(sf_urls, bs_data)

        # Compare link attributes
        link_attr_comparison = self.compare_link_attributes(sf_outlinks, bs_outlinks, bs_data.get("results", []))

        # Compare text content
        content_comparison = self.compare_content(sf_urls, bs_data)

        # Write detailed diff
        diff_file, diff_size = self.write_detailed_diff()

        # Print concise summary for LLM consumption
        print("\n" + "=" * 80)
        print(f"CRAWLER COMPARISON RESULTS: {self.domain}")
        print("=" * 80)

        # Configuration
        print("\nConfiguration:")
        print(f"  - Domain: {self.domain}")
        print(f"  - Crawl ID: {crawl_id}")
        print(f"  - Mode: {'BlueSnake only (using existing ScreamingFrog data)' if self.bluesnake_only else 'Full comparison (both crawlers)'}")
        print(f"  - BlueSnake config: JS rendering {'enabled' if self.js_rendering else 'disabled'}, parallelism=10, check external resources")

        # URL Coverage
        print("\nURL Coverage:")
        print(f"  - ScreamingFrog total: {url_comparison['sf_total']} URLs")
        print(f"  - BlueSnake total: {url_comparison['bs_total']} URLs")
        print(f"  - Common URLs: {url_comparison['common']}")
        print(f"  - Missing in BlueSnake: {url_comparison['missing_in_bs']}")
        print(f"  - Only in BlueSnake: {len(self.detailed_diff['url_diffs']['only_in_bluesnake'])}")

        if url_comparison["sf_total"] > 0:
            coverage = url_comparison["common"] / url_comparison["sf_total"] * 100
            print(f"  - Coverage: {coverage:.1f}%")

        # Resource Type Breakdown
        print("\nResource Type Breakdown:")
        print(f"  {'Type':<15} {'ScreamingFrog':<18} {'BlueSnake':<18} {'Missing in BS':<15}")
        print("  " + "-" * 66)
        for resource_type in sorted(set(list(url_comparison['sf_by_type'].keys()) + list(url_comparison['bs_by_type'].keys()))):
            sf_count = len(url_comparison['sf_by_type'].get(resource_type, set()))
            bs_count = len(url_comparison['bs_by_type'].get(resource_type, set()))
            missing_count = len(self.detailed_diff['url_diffs']['missing_in_bluesnake_by_type'].get(resource_type, []))
            print(f"  {resource_type:<15} {sf_count:<18} {bs_count:<18} {missing_count:<15}")

        # Status Code Differences
        print("\nStatus Code Differences:")
        print(f"  - URLs with different status codes: {status_comparison['diff_count']}")
        if status_comparison['diff_count'] > 0 and self.detailed_diff['status_diffs']:
            print(f"  - Example: {self.detailed_diff['status_diffs'][0]['url'][:60]}")
            print(f"    - ScreamingFrog: {self.detailed_diff['status_diffs'][0]['sf_status']}")
            print(f"    - BlueSnake: {self.detailed_diff['status_diffs'][0]['bs_status']}")

        # Outlink Differences
        print("\nOutlink Differences:")
        print(f"  - URLs checked: {outlink_comparison['checked_count']}")
        print(f"  - URLs with different outlinks: {outlink_comparison['diff_count']}")
        if outlink_comparison['diff_count'] > 0 and self.detailed_diff['outlink_diffs']:
            example = self.detailed_diff['outlink_diffs'][0]
            print(f"  - Example: {example['url'][:60]}")
            print(f"    - ScreamingFrog outlinks: {example['sf_count']}")
            print(f"    - BlueSnake outlinks: {example['bs_count']}")
            print(f"    - Only in ScreamingFrog: {len(example['only_in_sf'])}")
            print(f"    - Only in BlueSnake: {len(example['only_in_bs'])}")

        # Page Attribute Differences
        print("\nPage Attribute Differences:")
        total_page_diffs = sum([
            page_attr_comparison['depth_diffs'],
            page_attr_comparison['title_diffs'],
            page_attr_comparison['h1_diffs'],
            page_attr_comparison['word_count_diffs'],
            page_attr_comparison['indexable_diffs'],
            page_attr_comparison['canonical_diffs'],
        ])
        if total_page_diffs == 0:
            print("  - All page attributes match!")
        else:
            print(f"  - Depth differences: {page_attr_comparison['depth_diffs']}")
            print(f"  - Title differences: {page_attr_comparison['title_diffs']}")
            print(f"  - H1 differences: {page_attr_comparison['h1_diffs']}")
            print(f"  - Word count differences (>10%): {page_attr_comparison['word_count_diffs']}")
            print(f"  - Indexability differences: {page_attr_comparison['indexable_diffs']}")
            print(f"  - Canonical differences: {page_attr_comparison['canonical_diffs']}")

        # Link Attribute Differences
        print("\nLink Attribute Differences:")
        print(f"  - Links checked: {link_attr_comparison['checked_links']}")
        total_link_diffs = sum([
            link_attr_comparison['follow_diffs'],
            link_attr_comparison['target_diffs'],
            link_attr_comparison['rel_diffs'],
            link_attr_comparison['path_type_diffs'],
            link_attr_comparison['position_diffs'],
            link_attr_comparison['link_type_diffs'],
        ])
        if total_link_diffs == 0:
            print("  - All link attributes match!")
        else:
            print(f"  - Follow differences: {link_attr_comparison['follow_diffs']}")
            print(f"  - Target differences: {link_attr_comparison['target_diffs']}")
            print(f"  - Rel differences: {link_attr_comparison['rel_diffs']}")
            print(f"  - Path type differences: {link_attr_comparison['path_type_diffs']}")
            print(f"  - Position differences: {link_attr_comparison['position_diffs']}")
            print(f"  - Link type differences: {link_attr_comparison['link_type_diffs']}")

        # Text Content Comparison
        print("\nText Content Comparison:")
        print(f"  - ScreamingFrog pages with content: {content_comparison['sf_pages_with_content']}")
        print(f"  - BlueSnake pages with content: {content_comparison['bs_pages_with_content']}")
        print(f"  - Common pages compared: {content_comparison['common_pages_compared']}")
        if content_comparison['common_pages_compared'] > 0:
            print(f"\n  Average Similarity Metrics:")
            print(f"    - Jaccard (token overlap):     {content_comparison['avg_jaccard']:.1%}")
            print(f"    - Overlap coefficient:         {content_comparison['avg_overlap']:.1%}")
            print(f"    - Dice coefficient:            {content_comparison['avg_dice']:.1%}")
            print(f"    - Sequence ratio (order-aware):{content_comparison['avg_sequence_ratio']:.1%}")
            print(f"\n  Pages with low similarity (<70% Jaccard): {content_comparison['low_similarity_count']}")

            if content_comparison['low_similarity_pages']:
                print(f"\n  Lowest similarity pages:")
                for page in content_comparison['low_similarity_pages'][:5]:
                    print(f"    - {page['url'][:60]}...")
                    print(f"      Jaccard: {page['jaccard']:.1%}, SF words: {page['word_count_a']}, BS words: {page['word_count_b']}")
                    print(f"      Common: {page['common_words']}, Only SF: {page['unique_to_a']}, Only BS: {page['unique_to_b']}")

        # Detailed output files
        print("\nDetailed Analysis Files:")
        print(f"  - JSON diff file: {diff_file}")
        print(f"    (Contains comparison results, {diff_size:,} bytes)")
        print(f"  - ScreamingFrog directory: {self.sf_output_dir}/")
        print(f"    (Contains internal_all.csv, all_outlinks.csv, and other SF exports)")
        print(f"  - BlueSnake directory: {self.bs_output_dir}/")
        print(f"    (Contains internal_all.json, all_outlinks.json, crawl_summary.json)")
        if not self.bluesnake_only:
            print(f"  - ScreamingFrog log: {self.sf_log_file}")
        print(f"  - BlueSnake log: {self.bs_log_file}")
        print(f"\n  Use BOTH the JSON diff and raw files for complete analysis.")

        print("=" * 80 + "\n")

        return True


def main():
    parser = argparse.ArgumentParser(description="Compare ScreamingFrog and BlueSnake crawler results")
    parser.add_argument("domain", help="Domain to crawl (e.g., https://example.com)")
    parser.add_argument(
        "--bluesnake-only",
        action="store_true",
        help="Run BlueSnake only and use existing ScreamingFrog data (useful for validating fixes)",
    )
    parser.add_argument(
        "--js-rendering",
        action="store_true",
        help="Enable JavaScript rendering in BlueSnake (default: disabled)",
    )

    args = parser.parse_args()

    comparison = CrawlerComparison(args.domain, args.bluesnake_only, args.js_rendering)
    success = comparison.run()

    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
