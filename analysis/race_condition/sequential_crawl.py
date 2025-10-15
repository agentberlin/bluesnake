#!/usr/bin/env python3
"""
Script to run 100 crawls SEQUENTIALLY (one at a time, waiting for completion)
"""
import json
import urllib.request
import time
import sys

BASE_URL = "http://localhost:8080"
TARGET_CRAWLS = 100
TARGET_URL = "https://agentberlin.ai"

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
    """Start a new crawl and return immediately"""
    api_call("/api/v1/crawl", method="POST", data={"url": TARGET_URL})

def get_active_crawls():
    """Get all active crawls"""
    return api_call("/api/v1/active-crawls")

def get_projects():
    """Get all projects"""
    return api_call("/api/v1/projects")

def wait_for_all_crawls_complete(timeout=60):
    """Wait for all active crawls to complete"""
    start = time.time()
    while time.time() - start < timeout:
        active = get_active_crawls()
        if not active:
            return True
        time.sleep(0.5)
    return False

print("=" * 80)
print(f"SEQUENTIAL CRAWL ANALYSIS - Running {TARGET_CRAWLS} crawls ONE AT A TIME")
print("=" * 80)

# Check if server is up
try:
    health = api_call("/api/v1/health")
    print(f"\n✓ Server is healthy: {health}")
except Exception as e:
    print(f"\n✗ Server is not responding: {e}")
    sys.exit(1)

# Check initial state
projects = get_projects()
print(f"\nCurrent projects: {len(projects)}")
for p in projects:
    print(f"  - {p['domain']} (ID: {p['id']})")

print(f"\nStarting {TARGET_CRAWLS} SEQUENTIAL crawls for {TARGET_URL}")
print("Each crawl will complete before starting the next one.\n")

completed_crawls = 0
failed_crawls = 0
crawl_stats = []

for i in range(TARGET_CRAWLS):
    crawl_num = i + 1
    print(f"[{crawl_num}/{TARGET_CRAWLS}] Starting crawl...", end=" ", flush=True)

    try:
        # Start the crawl
        start_time = time.time()
        start_crawl()

        # Wait a moment for it to be registered
        time.sleep(0.5)

        # Wait for this crawl to complete
        max_wait = 30  # 30 seconds max per crawl
        wait_start = time.time()

        while time.time() - wait_start < max_wait:
            active = get_active_crawls()
            if not active:
                # Crawl completed
                elapsed = time.time() - start_time
                completed_crawls += 1
                crawl_stats.append({
                    'crawl_number': crawl_num,
                    'duration': elapsed,
                    'success': True
                })
                print(f"✓ Completed in {elapsed:.1f}s")
                break
            time.sleep(0.5)
        else:
            # Timeout
            elapsed = time.time() - start_time
            failed_crawls += 1
            crawl_stats.append({
                'crawl_number': crawl_num,
                'duration': elapsed,
                'success': False,
                'reason': 'timeout'
            })
            print(f"✗ Timeout after {elapsed:.1f}s")

        # Small delay between crawls
        time.sleep(0.5)

    except Exception as e:
        failed_crawls += 1
        crawl_stats.append({
            'crawl_number': crawl_num,
            'success': False,
            'reason': str(e)
        })
        print(f"✗ Error: {e}")
        time.sleep(1)

print("\n" + "=" * 80)
print("CRAWL SUMMARY")
print("=" * 80)
print(f"\nTotal crawls attempted: {TARGET_CRAWLS}")
print(f"Successful: {completed_crawls}")
print(f"Failed: {failed_crawls}")

if crawl_stats:
    successful_stats = [s for s in crawl_stats if s['success']]
    if successful_stats:
        durations = [s['duration'] for s in successful_stats]
        avg_duration = sum(durations) / len(durations)
        min_duration = min(durations)
        max_duration = max(durations)

        print(f"\nCrawl duration statistics:")
        print(f"  Average: {avg_duration:.1f}s")
        print(f"  Min: {min_duration:.1f}s")
        print(f"  Max: {max_duration:.1f}s")

# Get final project state
print("\n" + "=" * 80)
projects = get_projects()
print(f"\nFinal projects: {len(projects)}")
for p in projects:
    print(f"  - {p['domain']} (ID: {p['id']}, Total URLs: {p.get('totalUrls', 'N/A')}, Crawls: {p.get('totalUrls', 'N/A')})")

print("\n✓ All crawls completed!")
print("=" * 80)
