#!/usr/bin/env python3
"""
Analyze URL consistency across multiple crawls
Usage: python3 analyze_crawls.py <project_id> [crawl_count]
  project_id: The project ID to analyze
  crawl_count: Number of recent crawls to analyze (default: 22)
"""
import json
import urllib.request
from collections import defaultdict
import sys

if len(sys.argv) < 2:
    print("Error: Project ID is required")
    print("Usage: python3 analyze_crawls.py <project_id> [crawl_count]")
    sys.exit(1)

try:
    PROJECT_ID = int(sys.argv[1])
except ValueError:
    print(f"Error: Invalid project ID '{sys.argv[1]}'. Must be an integer.")
    sys.exit(1)

CRAWL_COUNT = int(sys.argv[2]) if len(sys.argv) > 2 else 22

def fetch_crawls_for_project(project_id):
    """Fetch all crawl IDs for a given project"""
    url = f"http://localhost:8080/api/v1/projects/{project_id}"
    try:
        with urllib.request.urlopen(url) as response:
            data = json.loads(response.read())
            # Get crawls from the project data
            # We need to fetch crawls separately
            crawls_url = f"http://localhost:8080/api/v1/projects/{project_id}/crawls"
            with urllib.request.urlopen(crawls_url) as crawls_response:
                crawls_data = json.loads(crawls_response.read())
                # Return most recent crawl IDs
                crawl_ids = [c['id'] for c in crawls_data]
                return sorted(crawl_ids, reverse=True)[:CRAWL_COUNT]
    except Exception as e:
        print(f"Error fetching crawls for project {project_id}: {e}")
        print("Falling back to manual crawl ID list. Please update the script.")
        return []

# Fetch crawl IDs for the project
print(f"Fetching crawls for project {PROJECT_ID}...")
crawls = fetch_crawls_for_project(PROJECT_ID)
if not crawls:
    print("No crawls found. Please ensure the project ID is correct.")
    sys.exit(1)

print(f"Found {len(crawls)} crawls to analyze: {crawls}")

def fetch_urls(crawl_id):
    """Fetch all URLs for a given crawl"""
    url = f"http://localhost:8080/api/v1/crawls/{crawl_id}?limit=1000&type=all"
    with urllib.request.urlopen(url) as response:
        data = json.loads(response.read())
        return sorted([r['url'] for r in data['results']])

# Fetch URLs for all crawls
print("Fetching URLs from all crawls...")
crawl_data = {}
for crawl_id in crawls:
    urls = fetch_urls(crawl_id)
    crawl_data[crawl_id] = urls
    print(f"Crawl {crawl_id}: {len(urls)} URLs")

print("\n" + "="*80)
print("ANALYSIS REPORT")
print("="*80)

# Find URLs that appear in some crawls but not others
all_urls = set()
for urls in crawl_data.values():
    all_urls.update(urls)

# Track which URLs appear in which crawls
url_appearances = defaultdict(list)
for crawl_id, urls in crawl_data.items():
    for url in all_urls:
        if url in urls:
            url_appearances[url].append(crawl_id)

# Identify unstable URLs (don't appear in all crawls)
total_crawls = len(crawls)
unstable_urls = {}
for url, appearances in url_appearances.items():
    if len(appearances) < total_crawls:
        unstable_urls[url] = {
            'appearances': len(appearances),
            'appearance_rate': f"{len(appearances)/total_crawls*100:.1f}%",
            'crawl_ids': appearances
        }

print(f"\nTotal unique URLs across all crawls: {len(all_urls)}")
print(f"Stable URLs (appear in all crawls): {len(all_urls) - len(unstable_urls)}")
print(f"Unstable URLs (missing in some crawls): {len(unstable_urls)}")

# Sort unstable URLs by appearance rate
sorted_unstable = sorted(unstable_urls.items(), key=lambda x: x[1]['appearances'])

print("\n" + "="*80)
print("UNSTABLE URLs (sorted by appearance frequency)")
print("="*80)

# Group by appearance rate
for url, data in sorted_unstable:
    print(f"\nURL: {url}")
    print(f"  Appears in: {data['appearances']}/{total_crawls} crawls ({data['appearance_rate']})")
    print(f"  Present in crawls: {data['crawl_ids'][:5]}{'...' if len(data['crawl_ids']) > 5 else ''}")

# Pattern analysis
print("\n" + "="*80)
print("PATTERN ANALYSIS")
print("="*80)

# Categorize by URL type
categories = {
    'HTML Pages': [],
    'JavaScript': [],
    'CSS': [],
    'Images': [],
    'Fonts': [],
    'Other': []
}

for url, data in unstable_urls.items():
    if 'text/html' in url or any(x in url for x in ['/blog/', '/tools/', '/privacy', '/terms', '/refund', '/pricing', '/newsletter']):
        categories['HTML Pages'].append((url, data))
    elif '.js' in url or 'javascript' in url:
        categories['JavaScript'].append((url, data))
    elif '.css' in url:
        categories['CSS'].append((url, data))
    elif any(ext in url for ext in ['.png', '.jpg', '.svg', '.webp', 'image?']):
        categories['Images'].append((url, data))
    elif '.woff' in url:
        categories['Fonts'].append((url, data))
    else:
        categories['Other'].append((url, data))

for category, items in categories.items():
    if items:
        print(f"\n{category}: {len(items)} unstable URLs")
        for url, data in items[:3]:  # Show first 3
            print(f"  - {url} ({data['appearance_rate']})")
        if len(items) > 3:
            print(f"  ... and {len(items) - 3} more")

# Check for subdomain patterns
print("\n" + "="*80)
print("SUBDOMAIN ANALYSIS")
print("="*80)

subdomain_counts = defaultdict(int)
for url in unstable_urls.keys():
    if 'workspace.agentberlin.ai' in url:
        subdomain_counts['workspace'] += 1
    elif 'handbook.agentberlin.ai' in url:
        subdomain_counts['handbook'] += 1
    elif 'storage.agentberlin.ai' in url:
        subdomain_counts['storage'] += 1
    else:
        subdomain_counts['main'] += 1

for subdomain, count in sorted(subdomain_counts.items(), key=lambda x: -x[1]):
    print(f"{subdomain}: {count} unstable URLs")
