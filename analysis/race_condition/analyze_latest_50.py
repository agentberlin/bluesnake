#!/usr/bin/env python3
"""Analyze the latest 50 crawls"""
import json
import urllib.request
from collections import defaultdict

# Latest 50 crawl IDs
CRAWLS = [1163, 1162, 1161, 1160, 1159, 1158, 1157, 1156, 1155, 1154, 1153, 1152, 1151, 1150, 
          1149, 1148, 1147, 1146, 1145, 1144, 1143, 1142, 1141, 1140, 1139, 1138, 1137, 1136, 
          1135, 1134, 1133, 1132, 1131, 1130, 1129, 1128, 1127, 1126, 1125, 1124, 1123, 1122, 
          1121, 1120, 1119, 1118, 1117, 1116, 1115, 1114]

def fetch_urls(crawl_id):
    """Fetch all URLs for a given crawl"""
    url = f"http://localhost:8080/api/v1/crawls/{crawl_id}?limit=1000&type=all"
    with urllib.request.urlopen(url) as response:
        data = json.loads(response.read())
        return sorted([r['url'] for r in data['results']])

# Fetch URLs for all crawls
print("Fetching URLs from latest 50 crawls...")
crawl_data = {}
for crawl_id in CRAWLS:
    urls = fetch_urls(crawl_id)
    crawl_data[crawl_id] = urls
    print(f"Crawl {crawl_id}: {len(urls)} URLs")

print("\n" + "="*80)
print("ANALYSIS REPORT - Latest 50 Crawls")
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
total_crawls = len(CRAWLS)
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

if len(unstable_urls) == 0:
    print("\n" + "="*80)
    print("✅ SUCCESS! NO RACE CONDITIONS DETECTED!")
    print("="*80)
    print("\nAll URLs appear consistently across all 50 crawls.")
    print("The race condition fix appears to be working correctly.")
else:
    # Sort unstable URLs by appearance rate
    sorted_unstable = sorted(unstable_urls.items(), key=lambda x: x[1]['appearances'])
    
    print("\n" + "="*80)
    print("⚠️  UNSTABLE URLs DETECTED (sorted by appearance frequency)")
    print("="*80)
    
    # Show most problematic URLs (appearing in <90% of crawls)
    problematic = [(url, data) for url, data in sorted_unstable if data['appearances'] < total_crawls * 0.9]
    
    if problematic:
        print(f"\nMost problematic URLs (appear in <90% of crawls): {len(problematic)}")
        for url, data in problematic[:10]:
            print(f"\nURL: {url}")
            print(f"  Appears in: {data['appearances']}/{total_crawls} crawls ({data['appearance_rate']})")
    
    # Show moderately unstable URLs (90-99%)
    moderate = [(url, data) for url, data in sorted_unstable if total_crawls * 0.9 <= data['appearances'] < total_crawls]
    
    if moderate:
        print(f"\n\nModerately unstable URLs (appear in 90-99% of crawls): {len(moderate)}")
        for url, data in moderate[:10]:
            print(f"\nURL: {url}")
            print(f"  Appears in: {data['appearances']}/{total_crawls} crawls ({data['appearance_rate']})")

# Check for previously problematic URLs
print("\n" + "="*80)
print("PREVIOUSLY PROBLEMATIC URLs CHECK")
print("="*80)

problematic_urls = [
    'handbook.agentberlin.ai/intro',
    'agentberlin.ai/privacy-policy', 
    'agentberlin.ai/refund-policy',
    'agentberlin.ai/terms-of-service'
]

for prob_url in problematic_urls:
    matching = [url for url in all_urls if prob_url in url]
    if matching:
        for url in matching:
            appearances = len(url_appearances[url])
            rate = appearances / total_crawls * 100
            status = "✅" if appearances == total_crawls else "❌"
            print(f"{status} {url}: {appearances}/{total_crawls} ({rate:.1f}%)")
    else:
        print(f"⚠️  {prob_url}: NOT FOUND in any crawl")
