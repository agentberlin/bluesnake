#!/usr/bin/env python3
"""
Analyze URL discrepancies across all crawls with link analysis
"""
import json
import urllib.request
import sys
from collections import defaultdict
import urllib.parse

BASE_URL = "http://localhost:8080"
PROJECT_ID = 2

def api_call(endpoint):
    """Make API call to the server"""
    url = f"{BASE_URL}{endpoint}"
    with urllib.request.urlopen(url) as response:
        return json.loads(response.read())

def get_crawls():
    """Get all crawls for the project"""
    return api_call(f"/api/v1/projects/{PROJECT_ID}/crawls")

def get_crawl_urls(crawl_id):
    """Get all URLs for a crawl"""
    data = api_call(f"/api/v1/crawls/{crawl_id}?limit=10000&type=all")
    return [r['url'] for r in data['results']]

def get_page_links(crawl_id, page_url):
    """Get all inbound links to a specific page in a crawl"""
    try:
        # The API endpoint is: /api/v1/crawls/{id}/pages/{url}/links
        # We need to properly encode the URL
        encoded_url = urllib.parse.quote(page_url, safe='')
        links = api_call(f"/api/v1/crawls/{crawl_id}/pages/{encoded_url}/links")
        return links
    except Exception as e:
        # If the page doesn't exist in the crawl, we'll get an error
        return []

# Get all crawls
all_crawls = get_crawls()
crawls = sorted(all_crawls, key=lambda x: x['id'], reverse=True)

# Fetch URLs for all crawls
crawl_data = {}
url_count_distribution = defaultdict(int)

for crawl in crawls:
    crawl_id = crawl['id']
    urls = get_crawl_urls(crawl_id)
    crawl_data[crawl_id] = urls
    url_count_distribution[len(urls)] += 1

# Find all unique URLs
all_urls = set()
for urls in crawl_data.values():
    all_urls.update(urls)

print(f"=" * 80)
print(f"SEQUENTIAL CRAWL ANALYSIS RESULTS")
print(f"=" * 80)
print(f"Total crawls: {len(crawls)} (IDs: {crawls[-1]['id']} to {crawls[0]['id']})")
print(f"Total unique URLs across all crawls: {len(all_urls)}")

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
            'appearance_rate': len(appearances) / total_crawls * 100,
            'crawl_ids': appearances
        }

stable_count = len(all_urls) - len(unstable_urls)
print(f"Stable URLs (appear in all crawls): {stable_count}")
print(f"Unstable URLs (missing in some crawls): {len(unstable_urls)}")

print("\n" + "=" * 80)
print("URL COUNT DISTRIBUTION")
print("=" * 80)
for url_count in sorted(url_count_distribution.keys(), reverse=True):
    count = url_count_distribution[url_count]
    percentage = count / total_crawls * 100
    print(f"{url_count} URLs: {count} crawls ({percentage:.1f}%)")

print("\n" + "=" * 80)
print("UNSTABLE URLs (sorted by appearance frequency)")
print("=" * 80)

# Sort unstable URLs by appearance rate
sorted_unstable = sorted(unstable_urls.items(), key=lambda x: x[1]['appearances'])

for url, data in sorted_unstable:
    print(f"\n{url}")
    print(f"  Appears in: {data['appearances']}/{total_crawls} crawls ({data['appearance_rate']:.1f}%)")

# Detailed link analysis for most unstable URLs
print("\n" + "=" * 80)
print("LINK ANALYSIS FOR UNSTABLE URLs")
print("=" * 80)

# Focus on URLs that appear in less than 80% of crawls
highly_unstable = [(url, data) for url, data in sorted_unstable if data['appearance_rate'] < 80]

print(f"Analyzing {len(highly_unstable)} highly unstable URLs (< 80% appearance rate)\n")

for url, data in highly_unstable[:10]:  # Limit to top 10 most unstable
    print(f"\n{'=' * 80}")
    print(f"URL: {url}")
    print(f"Appearance rate: {data['appearance_rate']:.1f}% ({data['appearances']}/{total_crawls} crawls)")
    print(f"{'=' * 80}")

    # Sample some crawls where the URL was found
    present_crawls = data['crawl_ids'][:5]
    missing_crawls = [cid for cid in crawl_data.keys() if cid not in data['crawl_ids']][:5]

    print("\nInbound links analysis:")

    # Get inbound links from crawls where URL was present
    inbound_links_present = {}
    for crawl_id in present_crawls:
        links = get_page_links(crawl_id, url)
        if links:
            inbound_links_present[crawl_id] = links
            print(f"\n  Crawl {crawl_id} (URL PRESENT):")
            print(f"    Total inbound links: {len(links)}")
            if links:
                # Show first few source URLs
                sources = [link.get('sourceUrl', link.get('source', 'unknown')) for link in links[:3]]
                for src in sources:
                    print(f"      ← {src}")
                if len(links) > 3:
                    print(f"      ... and {len(links) - 3} more")

    # Check if those source pages exist in crawls where the URL was missing
    if inbound_links_present:
        print(f"\n  Checking if source pages exist in crawls where URL was MISSING:")

        # Get a representative set of source URLs
        all_sources = set()
        for links in inbound_links_present.values():
            for link in links:
                source = link.get('sourceUrl', link.get('source'))
                if source:
                    all_sources.add(source)

        for crawl_id in missing_crawls:
            crawl_urls = crawl_data[crawl_id]
            sources_present = [src for src in all_sources if src in crawl_urls]

            print(f"\n    Crawl {crawl_id} (URL MISSING):")
            print(f"      Source pages that exist: {len(sources_present)}/{len(all_sources)}")

            if sources_present and len(sources_present) > 0:
                # Check if these source pages have links to the missing URL
                print(f"      Source pages found in this crawl:")
                for src in list(sources_present)[:3]:
                    print(f"        - {src}")
                    # Try to get links from this source page
                    try:
                        source_links = get_page_links(crawl_id, src)
                        target_links = [l for l in source_links if l.get('targetUrl') == url or l.get('target') == url]
                        if target_links:
                            print(f"          ✓ HAS link to target URL!")
                        else:
                            print(f"          ✗ NO link to target URL")
                    except:
                        pass

    print()

print("\n" + "=" * 80)
print("PATTERN SUMMARY")
print("=" * 80)

# Categorize unstable URLs
categories = {
    'workspace.agentberlin.ai': [],
    'handbook.agentberlin.ai': [],
    'main (agentberlin.ai)': [],
    'other subdomains': []
}

for url, data in unstable_urls.items():
    if 'workspace.agentberlin.ai' in url:
        categories['workspace.agentberlin.ai'].append((url, data))
    elif 'handbook.agentberlin.ai' in url:
        categories['handbook.agentberlin.ai'].append((url, data))
    elif 'agentberlin.ai' in url:
        categories['main (agentberlin.ai)'].append((url, data))
    else:
        categories['other subdomains'].append((url, data))

for category, items in categories.items():
    if items:
        avg_appearance = sum(d['appearance_rate'] for _, d in items) / len(items)
        print(f"\n{category}:")
        print(f"  Count: {len(items)} URLs")
        print(f"  Average appearance rate: {avg_appearance:.1f}%")
        for url, data in sorted(items, key=lambda x: x[1]['appearance_rate'])[:3]:
            print(f"    - {url.split('/')[-1][:50]}: {data['appearance_rate']:.1f}%")

print("\n" + "=" * 80)
print("ANALYSIS COMPLETE")
print("=" * 80)
