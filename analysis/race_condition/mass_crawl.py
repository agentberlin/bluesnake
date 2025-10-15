#!/usr/bin/env python3
"""
Script to run 1000 crawls and analyze URL discrepancies with link analysis
"""
import json
import urllib.request
import time
import sys
from collections import defaultdict

BASE_URL = "http://localhost:8080"
TARGET_CRAWLS = 1000
PROJECT_ID = 6

def api_call(endpoint, method="GET", data=None):
    """Make API call to the server"""
    url = f"{BASE_URL}{endpoint}"
    req = urllib.request.Request(url, method=method)
    if data:
        req.add_header('Content-Type', 'application/json')
        req.data = json.dumps(data).encode('utf-8')

    with urllib.request.urlopen(req) as response:
        return json.loads(response.read())

def start_crawl():
    """Start a new crawl"""
    api_call("/api/v1/crawl", method="POST", data={"url": "https://agentberlin.ai"})

def get_crawls():
    """Get all crawls for the project"""
    return api_call(f"/api/v1/projects/{PROJECT_ID}/crawls")

def get_crawl_urls(crawl_id):
    """Get all URLs for a crawl"""
    data = api_call(f"/api/v1/crawls/{crawl_id}?limit=10000&type=all")
    return [r['url'] for r in data['results']]

def get_page_links(crawl_id, page_url):
    """Get all links from a specific page in a crawl"""
    try:
        # URL encode the page_url for the API call
        import urllib.parse
        encoded_url = urllib.parse.quote(page_url, safe='')
        data = api_call(f"/api/v1/crawls/{crawl_id}/pages/{encoded_url}/links")
        return data
    except Exception as e:
        return []

def is_crawl_complete(crawl_id):
    """Check if a crawl is complete by checking active crawls"""
    active = api_call("/api/v1/active-crawls")
    return not any(c['id'] == crawl_id for c in active)

def wait_for_crawl(crawl_id, timeout=30):
    """Wait for a crawl to complete"""
    start = time.time()
    while time.time() - start < timeout:
        if is_crawl_complete(crawl_id):
            return True
        time.sleep(0.5)
    return False

print("=" * 80)
print(f"MASS CRAWL ANALYSIS - Running {TARGET_CRAWLS} crawls")
print("=" * 80)

# Get initial crawl count
initial_crawls = get_crawls()
initial_count = len(initial_crawls)
print(f"\nInitial crawl count: {initial_count}")

# Get the latest crawl ID to know where to start
latest_crawl_id = max(c['id'] for c in initial_crawls) if initial_crawls else 0

print(f"Starting from crawl ID: {latest_crawl_id}")
print(f"\nStarting {TARGET_CRAWLS} crawls...\n")

crawls_started = 0
for i in range(TARGET_CRAWLS):
    try:
        start_crawl()
        crawls_started += 1

        if (i + 1) % 10 == 0:
            print(f"Started {i + 1}/{TARGET_CRAWLS} crawls...")

        # Small delay to avoid overwhelming the server
        time.sleep(0.1)

    except Exception as e:
        print(f"Error starting crawl {i + 1}: {e}")
        time.sleep(1)

print(f"\n✓ Started {crawls_started} crawls")
print("\nWaiting for all crawls to complete...")

# Wait for all crawls to complete
max_wait = 600  # 10 minutes
start_time = time.time()
while time.time() - start_time < max_wait:
    active = api_call("/api/v1/active-crawls")
    if not active:
        print("✓ All crawls completed!")
        break

    if int(time.time() - start_time) % 10 == 0:
        print(f"  {len(active)} crawls still running... ({int(time.time() - start_time)}s elapsed)")

    time.sleep(2)
else:
    print("⚠ Timeout waiting for crawls to complete")

print("\nCrawls completed. Starting analysis...")
print("=" * 80)
