#!/bin/bash
set -e

echo "=================================="
echo "MASS CRAWL ANALYSIS PIPELINE"
echo "=================================="
echo ""
echo "Step 1: Running 1000 crawls..."
echo ""

python3 /tmp/mass_crawl.py

echo ""
echo "Step 2: Analyzing results with link analysis..."
echo ""

python3 /tmp/analyze_mass_crawls.py | tee /tmp/crawl_analysis_report.txt

echo ""
echo "=================================="
echo "ANALYSIS COMPLETE"
echo "=================================="
echo ""
echo "Full report saved to: /tmp/crawl_analysis_report.txt"
