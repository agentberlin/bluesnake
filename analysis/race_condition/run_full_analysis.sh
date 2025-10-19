#!/bin/bash
set -e

echo "=================================="
echo "MASS CRAWL ANALYSIS PIPELINE"
echo "=================================="
echo ""
echo "Step 1: Running 1000 crawls..."
echo ""

python3 mass_crawl.py

echo ""
echo "Step 2: Analyzing results with link analysis..."
echo ""

python3 analyze_mass_crawls.py | tee crawl_analysis_report.txt

echo ""
echo "=================================="
echo "ANALYSIS COMPLETE"
echo "=================================="
echo ""
echo "Full report saved to: crawl_analysis_report.txt"
