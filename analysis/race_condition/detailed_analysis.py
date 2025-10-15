#!/usr/bin/env python3
import json
import urllib.request
from collections import defaultdict

# Crawl IDs with their metadata
crawls = [
    (167, 17, 2330), (166, 17, 2342), (165, 17, 2452), (164, 17, 2645),
    (163, 19, 2462), (162, 17, 2772), (161, 17, 3518), (160, 17, 3268),
    (159, 16, 2558), (158, 17, 2675), (157, 16, 2891), (156, 15, 5106),
    (155, 18, 2270), (154, 16, 2414), (153, 16, 2308), (152, 16, 2524),
    (151, 18, 2519), (150, 15, 2720), (149, 17, 2630), (148, 16, 2728),
    (147, 18, 2866), (146, 16, 6855)
]

def fetch_urls(crawl_id):
    """Fetch all URLs for a given crawl"""
    url = f"http://localhost:8080/api/v1/crawls/{crawl_id}?limit=1000&type=all"
    with urllib.request.urlopen(url) as response:
        data = json.loads(response.read())
        return sorted([r['url'] for r in data['results']])

# Fetch URLs for all crawls
print("Fetching URLs from all crawls...")
crawl_data = {}
for crawl_id, pages, duration in crawls:
    urls = fetch_urls(crawl_id)
    crawl_data[crawl_id] = {
        'urls': urls,
        'pages_crawled': pages,
        'duration_ms': duration,
        'total_urls': len(urls)
    }

print("\n" + "="*80)
print("DETAILED URL DIFFERENCE ANALYSIS")
print("="*80)

# Most unstable URLs
unstable_urls = [
    'https://workspace.agentberlin.ai/signup',
    'https://workspace.agentberlin.ai/login',
    'https://workspace.agentberlin.ai/_next/static/chunks/app/signup/page-6ca5b4ffa049f25d.js',
    'https://agentberlin.ai/pricing',
    'https://handbook.agentberlin.ai/intro',
    'https://handbook.agentberlin.ai/topic_first_seo',
    'https://agentberlin.ai/blog',
]

for url in unstable_urls:
    print(f"\n{url}")
    print("  Present in crawls:")
    present = []
    missing = []
    for crawl_id, data in crawl_data.items():
        if url in data['urls']:
            present.append(crawl_id)
        else:
            missing.append(crawl_id)

    print(f"    Present: {present}")
    print(f"    Missing: {missing}")

    # Check if there's a correlation with duration or page count
    avg_duration_present = sum(crawl_data[c]['duration_ms'] for c in present) / len(present) if present else 0
    avg_duration_missing = sum(crawl_data[c]['duration_ms'] for c in missing) / len(missing) if missing else 0

    avg_pages_present = sum(crawl_data[c]['pages_crawled'] for c in present) / len(present) if present else 0
    avg_pages_missing = sum(crawl_data[c]['pages_crawled'] for c in missing) / len(missing) if missing else 0

    print(f"  Average duration when PRESENT: {avg_duration_present:.0f}ms")
    print(f"  Average duration when MISSING: {avg_duration_missing:.0f}ms")
    print(f"  Average pages when PRESENT: {avg_pages_present:.1f}")
    print(f"  Average pages when MISSING: {avg_pages_missing:.1f}")

print("\n" + "="*80)
print("CRAWL STATISTICS CORRELATION")
print("="*80)

# Check correlation between crawl stats and total URLs found
print(f"\n{'Crawl':<8} {'Pages':<8} {'Duration':<12} {'Total URLs':<12}")
print("-" * 40)
for crawl_id, data in sorted(crawl_data.items(), reverse=True):
    print(f"{crawl_id:<8} {data['pages_crawled']:<8} {data['duration_ms']:<12} {data['total_urls']:<12}")

# Group by total URL count
url_count_groups = defaultdict(list)
for crawl_id, data in crawl_data.items():
    url_count_groups[data['total_urls']].append((crawl_id, data['pages_crawled'], data['duration_ms']))

print("\n" + "="*80)
print("GROUPED BY TOTAL URL COUNT")
print("="*80)
for url_count in sorted(url_count_groups.keys(), reverse=True):
    crawls_list = url_count_groups[url_count]
    print(f"\nTotal URLs: {url_count} ({len(crawls_list)} crawls)")
    for crawl_id, pages, duration in crawls_list:
        print(f"  Crawl {crawl_id}: {pages} pages, {duration}ms")
