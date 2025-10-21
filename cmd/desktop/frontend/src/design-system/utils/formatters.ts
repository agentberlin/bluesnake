// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/**
 * Format a Unix timestamp to a human-readable date
 * @example formatDate(1609459200) // "Jan 1, 2021"
 */
export function formatDate(timestamp: number): string {
  const date = new Date(timestamp * 1000);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

/**
 * Format a Unix timestamp to a human-readable date and time
 * @example formatDateTime(1609459200) // "Jan 1, 2021, 12:00 PM"
 */
export function formatDateTime(timestamp: number): string {
  const date = new Date(timestamp * 1000);
  return date.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    hour12: true,
  });
}

/**
 * Format milliseconds to a human-readable duration
 * @example formatDuration(125000) // "2m 5s"
 */
export function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  return `${minutes}m ${remainingSeconds}s`;
}

/**
 * Truncate a URL to a maximum length
 * @example truncateUrl('https://example.com/very/long/path', 30) // "https://example.com/very/lo..."
 */
export function truncateUrl(url: string, maxLength: number = 40): string {
  if (url.length <= maxLength) return url;
  return url.substring(0, maxLength - 3) + '...';
}

/**
 * Get a human-readable label for HTTP status codes
 * @example getStatusLabel(404) // "Not Found"
 */
export function getStatusLabel(status: number): string {
  if (status === 0) return 'Network Error';
  if (status >= 200 && status < 300) return 'Success';
  if (status >= 300 && status < 400) return 'Redirect';
  if (status >= 400 && status < 500) return 'Client Error';
  if (status >= 500) return 'Server Error';
  return 'Unknown';
}

/**
 * Categorize content type into standard categories
 * @example categorizeContentType('text/html') // "html"
 */
export function categorizeContentType(contentType: string | undefined): string {
  if (!contentType) return 'other';

  const ct = contentType.toLowerCase();
  if (ct.includes('text/html') || ct.includes('application/xhtml')) return 'html';
  if (ct.includes('javascript') || ct.includes('application/x-javascript') || ct.includes('text/javascript')) return 'javascript';
  if (ct.includes('text/css')) return 'css';
  if (ct.includes('image/')) return 'image';
  if (ct.includes('font/') || ct.includes('application/font') || ct.includes('woff') || ct.includes('ttf') || ct.includes('eot') || ct.includes('otf')) return 'font';
  return 'other';
}

/**
 * Get a display-friendly name for content types
 * @example getContentTypeDisplay('image/png') // "PNG"
 */
export function getContentTypeDisplay(contentType: string | undefined): string {
  if (!contentType) return 'Unknown';

  const category = categorizeContentType(contentType);
  const ct = contentType.toLowerCase();

  // Return more specific type for images
  if (category === 'image') {
    if (ct.includes('jpeg') || ct.includes('jpg')) return 'JPEG';
    if (ct.includes('png')) return 'PNG';
    if (ct.includes('gif')) return 'GIF';
    if (ct.includes('webp')) return 'WebP';
    if (ct.includes('svg')) return 'SVG';
    return 'Image';
  }

  // Return capitalized category for others
  return category.charAt(0).toUpperCase() + category.slice(1);
}
