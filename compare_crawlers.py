#!/usr/bin/env python3
"""
Crawler Comparison Script
Compares ScreamingFrog and BlueSnake crawler results
"""

import argparse
import csv
import json
import os
import subprocess
import sys
import time
import urllib.parse
from collections import defaultdict
from datetime import datetime
from pathlib import Path
from typing import Dict, List, Set, Tuple

import requests


class CrawlerComparison:
    def __init__(self, domain: str, server_url: str = "http://localhost:8001"):
        self.domain = domain
        self.server_url = server_url
        self.sf_output_dir = Path("/tmp/crawlertest/sf")
        self.scream_executable = (
            "/Applications/Screaming Frog SEO Spider.app/Contents/MacOS/ScreamingFrogSEOSpiderLauncher"
        )
        self.config_path = "/Users/hhsecond/rendering.seospiderconfig"

        # For log and diff output
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        domain_safe = domain.replace("https://", "").replace("http://", "").replace("/", "_")
        self.sf_log_file = f"/tmp/screamingfrog_{domain_safe}_{timestamp}.log"

        # For detailed diff output
        self.detailed_diff = {
            "metadata": {
                "domain": domain,
                "timestamp": datetime.now().isoformat(),
                "screamingfrog_log": self.sf_log_file,
            },
            "url_diffs": {},
            "status_diffs": {},
            "outlink_diffs": {},
        }

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
        print(f"\n{'='*80}")
        print(f"Running ScreamingFrog for {self.domain}")
        print(f"{'='*80}\n")

        # Create output directory
        self.sf_output_dir.mkdir(parents=True, exist_ok=True)

        # Ensure domain has protocol
        crawl_url = self.domain
        if not crawl_url.startswith(('http://', 'https://')):
            crawl_url = f'https://{crawl_url}'

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

        print(f"Running ScreamingFrog (output will be logged to {self.sf_log_file})...")

        try:
            # Capture output to log file
            with open(self.sf_log_file, "w") as log_file:
                result = subprocess.run(args, stdout=log_file, stderr=subprocess.STDOUT, timeout=3600)

            if result.returncode != 0:
                print(f"\nScreamingFrog failed with return code {result.returncode}")
                print(f"Check log file for details: {self.sf_log_file}")
                return False

            log_size = os.path.getsize(self.sf_log_file)
            print(f"ScreamingFrog crawl completed successfully")
            print(f"Log file: {self.sf_log_file} ({log_size:,} bytes)\n")
            return True
        except subprocess.TimeoutExpired:
            print("ScreamingFrog timed out after 1 hour")
            print(f"Check log file for details: {self.sf_log_file}")
            return False
        except Exception as e:
            print(f"Error running ScreamingFrog: {e}")
            return False

    def check_server_running(self) -> bool:
        """Check if BlueSnake server is running"""
        try:
            response = requests.get(f"{self.server_url}/api/v1/health", timeout=5)
            return response.status_code == 200
        except:
            return False

    def configure_bluesnake(self) -> bool:
        """Configure BlueSnake crawler settings"""
        crawl_url = self.domain
        if not crawl_url.startswith(('http://', 'https://')):
            crawl_url = f'https://{crawl_url}'

        config = {
            "url": crawl_url,
            "jsRendering": True,
            "parallelism": 10,
            "userAgent": "Mozilla/5.0 (compatible; BlueSnake/1.0)",
            "includeSubdomains": False,
            "spiderEnabled": True,
            "sitemapEnabled": True,
            "sitemapURLs": [],
            "checkExternalResources": True,
        }

        try:
            response = requests.put(f"{self.server_url}/api/v1/config", json=config, timeout=10)
            if response.status_code != 200:
                print(f"Warning: Failed to update config: {response.status_code}")
                print(response.text)
                return False
            print("BlueSnake configured with JS rendering enabled")
            return True
        except Exception as e:
            print(f"Error configuring BlueSnake: {e}")
            return False

    def start_crawl(self) -> bool:
        """Start BlueSnake crawl"""
        print(f"\n{'='*80}")
        print(f"Running BlueSnake crawler for {self.domain}")
        print(f"{'='*80}\n")

        if not self.check_server_running():
            print(f"ERROR: BlueSnake server is not running at {self.server_url}")
            print("Please start the server with: cd cmd/server && go run . &")
            return False

        # Configure crawler settings
        if not self.configure_bluesnake():
            print("Warning: Failed to configure BlueSnake, continuing with default settings")

        # Ensure domain has protocol
        crawl_url = self.domain
        if not crawl_url.startswith(('http://', 'https://')):
            crawl_url = f'https://{crawl_url}'

        # Start crawl
        try:
            response = requests.post(f"{self.server_url}/api/v1/crawl", json={"url": crawl_url}, timeout=10)

            if response.status_code != 202:
                print(f"Failed to start crawl: {response.status_code}")
                print(response.text)
                return False

            print("Crawl started successfully")
            return True
        except Exception as e:
            print(f"Error starting crawl: {e}")
            return False

    def wait_for_crawl_completion(self, max_wait_seconds: int = 3600) -> Tuple[bool, int, int]:
        """
        Wait for crawl to complete
        Returns: (success, project_id, crawl_id)
        """
        print("\nWaiting for crawl to complete...")
        start_time = time.time()
        last_progress = 0

        while time.time() - start_time < max_wait_seconds:
            try:
                response = requests.get(f"{self.server_url}/api/v1/active-crawls", timeout=10)
                if response.status_code != 200:
                    print(f"Error checking crawl status: {response.status_code}")
                    time.sleep(5)
                    continue

                active_crawls = response.json()

                # Find our crawl
                our_crawl = None
                for crawl in active_crawls:
                    if self.domain in crawl.get("url", "") or self.domain in crawl.get("domain", ""):
                        our_crawl = crawl
                        break

                if our_crawl:
                    pages = our_crawl.get("pagesCrawled", 0)
                    if pages != last_progress:
                        print(f"Progress: {pages} pages crawled")
                        last_progress = pages

                    if not our_crawl.get("isCrawling", True):
                        print(f"\nCrawl completed! Total pages: {pages}")
                        return True, our_crawl.get("projectId"), our_crawl.get("crawlId")
                else:
                    # No active crawl found - might be completed
                    # Try to find the project
                    projects_response = requests.get(f"{self.server_url}/api/v1/projects", timeout=10)
                    if projects_response.status_code == 200:
                        projects = projects_response.json()
                        for project in projects:
                            if self.domain in project.get("url", "") or self.domain in project.get("domain", ""):
                                print(f"\nCrawl completed! Found project {project['id']}")
                                return True, project["id"], project.get("latestCrawlId", 0)

                    print("No active crawl found, but no completed crawl either. Still waiting...")

                time.sleep(5)

            except Exception as e:
                print(f"Error checking crawl status: {e}")
                time.sleep(5)

        print(f"\nTimeout waiting for crawl completion after {max_wait_seconds} seconds")
        return False, 0, 0

    def fetch_bluesnake_data(self, crawl_id: int) -> Dict:
        """Fetch crawl results from BlueSnake API"""
        print(f"\n{'='*80}")
        print(f"Fetching BlueSnake crawl data")
        print(f"{'='*80}\n")

        try:
            response = requests.get(f"{self.server_url}/api/v1/crawls/{crawl_id}", timeout=30)
            if response.status_code != 200:
                print(f"Failed to fetch crawl data: {response.status_code}")
                return None

            data = response.json()
            print(f"Fetched {len(data.get('results', []))} URLs from BlueSnake")
            return data
        except Exception as e:
            print(f"Error fetching BlueSnake data: {e}")
            return None

    def fetch_bluesnake_links(self, crawl_id: int, url: str) -> Dict:
        """Fetch outlinks for a specific URL"""
        try:
            # URL encode the page URL
            encoded_url = urllib.parse.quote(url, safe="")
            response = requests.get(f"{self.server_url}/api/v1/crawls/{crawl_id}/pages/{encoded_url}/links", timeout=10)
            if response.status_code != 200:
                return None
            return response.json()
        except Exception as e:
            # Silently fail to avoid cluttering output
            return None

    def parse_screamingfrog_internal(self) -> Dict[str, Dict]:
        """Parse ScreamingFrog Internal:All export"""
        internal_file = self.sf_output_dir / "internal_all.csv"
        if not internal_file.exists():
            print(f"Warning: {internal_file} not found")
            return {}

        urls = {}
        with open(internal_file, "r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            for row in reader:
                url = row.get("Address", "")
                if url:
                    urls[url] = {
                        "status": int(row.get("Status Code", 0)) if row.get("Status Code") else 0,
                        "content_type": row.get("Content Type", ""),
                        "title": row.get("Title", ""),
                        "indexable": row.get("Indexability", ""),
                    }

        print(f"Parsed {len(urls)} URLs from ScreamingFrog Internal:All")
        return urls

    def parse_screamingfrog_outlinks(self) -> Dict[str, List[Dict]]:
        """Parse ScreamingFrog All Outlinks export"""
        outlinks_file = self.sf_output_dir / "all_outlinks.csv"
        if not outlinks_file.exists():
            print(f"Warning: {outlinks_file} not found")
            return {}

        outlinks = defaultdict(list)
        with open(outlinks_file, "r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            for row in reader:
                source = row.get("Source", "")
                target = row.get("Destination", "")
                if source and target:
                    outlinks[source].append(
                        {"to": target, "anchor": row.get("Anchor Text", ""), "type": row.get("Type", "")}
                    )

        print(f"Parsed outlinks for {len(outlinks)} URLs from ScreamingFrog")
        return outlinks

    def normalize_url(self, url: str) -> str:
        """Normalize URL for comparison"""
        # Remove trailing slash for comparison
        url = url.rstrip("/")
        # Decode URL
        url = urllib.parse.unquote(url)
        return url

    def compare_urls(self, sf_urls: Dict, bs_data: Dict) -> Dict:
        """Compare crawled URLs between ScreamingFrog and BlueSnake"""
        print(f"\n{'='*80}")
        print("Comparing Crawled URLs")
        print(f"{'='*80}\n")

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
        print(f"Resource Type Breakdown:")
        print(f"{'Type':<15} {'SF':<8} {'BS':<8} {'Missing in BS':<15}")
        print("-" * 50)

        total_missing = 0
        missing_by_type = {}

        for resource_type in sorted(set(list(sf_by_type.keys()) + list(bs_by_type.keys()))):
            sf_count = len(sf_by_type.get(resource_type, set()))
            bs_count = len(bs_by_type.get(resource_type, set()))
            missing = sf_by_type.get(resource_type, set()) - bs_by_type.get(resource_type, set())
            missing_count = len(missing)

            print(f"{resource_type:<15} {sf_count:<8} {bs_count:<8} {missing_count:<15}")

            total_missing += missing_count
            if missing_count > 0:
                missing_by_type[resource_type] = list(missing)

        print(f"\nTotal URLs:")
        print(f"  ScreamingFrog: {len(sf_set)}")
        print(f"  BlueSnake:     {len(bs_set)}")
        print(f"  Common:        {len(common)}")
        print(f"  Missing in BS: {total_missing}")

        # Store detailed diff
        self.detailed_diff["url_diffs"] = {
            "missing_in_bluesnake_by_type": missing_by_type,
            "only_in_bluesnake": list(bs_set - sf_set),
        }

        return {
            "sf_total": len(sf_set),
            "bs_total": len(bs_set),
            "common": len(common),
            "missing_in_bs": total_missing,
        }

    def compare_status_codes(self, sf_urls: Dict, bs_data: Dict) -> Dict:
        """Compare HTTP status codes"""
        print(f"\n{'='*80}")
        print("Comparing Status Codes")
        print(f"{'='*80}\n")

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

        print(f"URLs with different status codes: {len(status_diffs)}")

        if status_diffs and len(status_diffs) > 0:
            print(f"\nExample:")
            example = status_diffs[0]
            print(f"  URL: {example['url'][:80]}")
            print(f"  ScreamingFrog: {example['sf_status']}")
            print(f"  BlueSnake:     {example['bs_status']}")

        # Store detailed diff
        self.detailed_diff["status_diffs"] = status_diffs

        return {
            "diff_count": len(status_diffs),
        }

    def compare_outlinks(self, crawl_id: int, sf_outlinks: Dict, bs_results: List[Dict]) -> Dict:
        """Compare outlinks for ALL pages"""
        print(f"\n{'='*80}")
        print("Comparing Outlinks")
        print(f"{'='*80}\n")

        print(f"Fetching outlinks from BlueSnake API for {len(bs_results)} URLs...")
        print("(This may take a while...)\n")

        outlink_diffs = []
        checked_count = 0

        for i, result in enumerate(bs_results):
            url = result["url"]
            norm_url = self.normalize_url(url)

            # Progress indicator every 10 URLs
            if (i + 1) % 10 == 0:
                print(f"Progress: {i + 1}/{len(bs_results)} URLs checked")

            # Get BlueSnake outlinks
            bs_links_data = self.fetch_bluesnake_links(crawl_id, url)
            if not bs_links_data:
                continue

            checked_count += 1

            # Get ScreamingFrog outlinks
            sf_out = set(self.normalize_url(link["to"]) for link in sf_outlinks.get(norm_url, []))
            bs_out = set(self.normalize_url(link["url"]) for link in bs_links_data.get("outlinks", []))

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

        print(f"\nCompleted checking {checked_count} URLs")
        print(f"URLs with different outlinks: {len(outlink_diffs)}")

        if outlink_diffs:
            print(f"\nExample:")
            example = outlink_diffs[0]
            print(f"  URL: {example['url'][:80]}")
            print(f"  ScreamingFrog outlinks: {example['sf_count']}")
            print(f"  BlueSnake outlinks:     {example['bs_count']}")
            print(f"  Only in ScreamingFrog:  {len(example['only_in_sf'])} links")
            if example["only_in_sf"]:
                print(f"    Example: {example['only_in_sf'][0][:80]}")
            print(f"  Only in BlueSnake:      {len(example['only_in_bs'])} links")
            if example["only_in_bs"]:
                print(f"    Example: {example['only_in_bs'][0][:80]}")

        # Store detailed diff
        self.detailed_diff["outlink_diffs"] = outlink_diffs

        return {
            "checked_count": checked_count,
            "diff_count": len(outlink_diffs),
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
        print("\n" + "=" * 80)
        print(f"CRAWLER COMPARISON: {self.domain}")
        print("=" * 80)

        # Step 1: Run ScreamingFrog
        if not self.run_screamingfrog():
            print("ScreamingFrog crawl failed. Exiting.")
            return False

        # Step 2: Run BlueSnake
        if not self.start_crawl():
            print("BlueSnake crawl failed to start. Exiting.")
            return False

        success, project_id, crawl_id = self.wait_for_crawl_completion()
        if not success:
            print("BlueSnake crawl did not complete. Exiting.")
            return False

        # Step 3: Fetch BlueSnake data
        bs_data = self.fetch_bluesnake_data(crawl_id)
        if not bs_data:
            print("Failed to fetch BlueSnake data. Exiting.")
            return False

        # Step 4: Parse ScreamingFrog data
        print(f"\n{'='*80}")
        print("Parsing ScreamingFrog Data")
        print(f"{'='*80}\n")

        sf_urls = self.parse_screamingfrog_internal()
        sf_outlinks = self.parse_screamingfrog_outlinks()

        # Step 5: Compare URLs
        url_comparison = self.compare_urls(sf_urls, bs_data)

        # Step 6: Compare status codes
        status_comparison = self.compare_status_codes(sf_urls, bs_data)

        # Step 7: Compare outlinks
        outlink_comparison = self.compare_outlinks(crawl_id, sf_outlinks, bs_data.get("results", []))

        # Step 8: Write detailed diff
        diff_file, diff_size = self.write_detailed_diff()

        # Final summary
        print(f"\n{'='*80}")
        print("FINAL SUMMARY")
        print(f"{'='*80}\n")

        print(f"URL Coverage:")
        print(f"  ScreamingFrog found: {url_comparison['sf_total']} URLs")
        print(f"  BlueSnake found:     {url_comparison['bs_total']} URLs")
        print(f"  Common:              {url_comparison['common']} URLs")
        print(f"  Missing in BlueSnake: {url_comparison['missing_in_bs']} URLs")

        if url_comparison["sf_total"] > 0:
            coverage = url_comparison["common"] / url_comparison["sf_total"] * 100
            print(f"  BlueSnake coverage:  {coverage:.1f}%")

        print(f"\nStatus Code Differences:")
        print(f"  URLs with different status codes: {status_comparison['diff_count']}")

        print(f"\nOutlink Accuracy:")
        print(f"  URLs checked: {outlink_comparison['checked_count']}")
        print(f"  URLs with different outlinks: {outlink_comparison['diff_count']}")
        if outlink_comparison["checked_count"] > 0:
            match_pct = (
                (outlink_comparison["checked_count"] - outlink_comparison["diff_count"])
                / outlink_comparison["checked_count"]
                * 100
            )
            print(f"  Match rate: {match_pct:.1f}%")

        print(f"\nOutput Files:")
        print(f"  Detailed diff: {diff_file}")
        print(f"    Size: {diff_size:,} bytes ({diff_size / 1024:.1f} KB)")

        sf_log_size = os.path.getsize(self.sf_log_file)
        print(f"  ScreamingFrog log: {self.sf_log_file}")
        print(f"    Size: {sf_log_size:,} bytes ({sf_log_size / 1024:.1f} KB)")

        print("\n" + "=" * 80 + "\n")

        return True


def main():
    parser = argparse.ArgumentParser(description="Compare ScreamingFrog and BlueSnake crawler results")
    parser.add_argument("domain", help="Domain to crawl (e.g., https://example.com)")
    parser.add_argument(
        "--server-url", default="http://localhost:8080", help="BlueSnake server URL (default: http://localhost:8080)"
    )

    args = parser.parse_args()

    comparison = CrawlerComparison(args.domain, args.server_url)
    success = comparison.run()

    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
