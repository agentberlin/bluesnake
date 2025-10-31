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

import { ComboboxOption } from '../design-system/components/Combobox';

/**
 * Common user-agent strings for browsers, crawlers, and AI bots
 * Updated: October 2025
 */
export const USER_AGENTS: ComboboxOption[] = [
  // BlueSnake
  {
    value: 'bluesnake/1.0 (+https://snake.blue)',
    label: 'BlueSnake (Default)',
    description: 'BlueSnake web crawler - default user-agent',
    category: 'BlueSnake',
  },

  // Desktop Browsers - Chrome
  {
    value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36',
    label: 'Chrome (Windows)',
    description: 'Latest Chrome on Windows 10/11',
    category: 'Desktop Browsers',
  },
  {
    value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36',
    label: 'Chrome (macOS)',
    description: 'Latest Chrome on macOS',
    category: 'Desktop Browsers',
  },
  {
    value: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36',
    label: 'Chrome (Linux)',
    description: 'Latest Chrome on Linux',
    category: 'Desktop Browsers',
  },

  // Desktop Browsers - Firefox
  {
    value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0',
    label: 'Firefox (Windows)',
    description: 'Latest Firefox on Windows',
    category: 'Desktop Browsers',
  },
  {
    value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0',
    label: 'Firefox (macOS)',
    description: 'Latest Firefox on macOS',
    category: 'Desktop Browsers',
  },
  {
    value: 'Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0',
    label: 'Firefox (Linux)',
    description: 'Latest Firefox on Linux',
    category: 'Desktop Browsers',
  },

  // Desktop Browsers - Safari
  {
    value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15',
    label: 'Safari (macOS)',
    description: 'Latest Safari on macOS',
    category: 'Desktop Browsers',
  },

  // Desktop Browsers - Edge
  {
    value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36 Edg/142.0.0.0',
    label: 'Edge (Windows)',
    description: 'Latest Microsoft Edge on Windows',
    category: 'Desktop Browsers',
  },

  // Mobile Browsers
  {
    value: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1',
    label: 'Safari (iPhone)',
    description: 'Latest Safari on iPhone',
    category: 'Mobile Browsers',
  },
  {
    value: 'Mozilla/5.0 (iPad; CPU OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1',
    label: 'Safari (iPad)',
    description: 'Latest Safari on iPad',
    category: 'Mobile Browsers',
  },
  {
    value: 'Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Mobile Safari/537.36',
    label: 'Chrome (Android)',
    description: 'Latest Chrome on Android',
    category: 'Mobile Browsers',
  },

  // Search Engine Crawlers - Google
  {
    value: 'Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)',
    label: 'Googlebot',
    description: 'Google\'s main web crawler for search indexing',
    category: 'Search Crawlers',
  },
  {
    value: 'Googlebot-Image/1.0',
    label: 'Googlebot-Image',
    description: 'Google\'s crawler for images',
    category: 'Search Crawlers',
  },
  {
    value: 'Googlebot-News',
    label: 'Googlebot-News',
    description: 'Google\'s crawler for news content',
    category: 'Search Crawlers',
  },
  {
    value: 'Googlebot-Video/1.0',
    label: 'Googlebot-Video',
    description: 'Google\'s crawler for video content',
    category: 'Search Crawlers',
  },

  // Search Engine Crawlers - Bing
  {
    value: 'Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)',
    label: 'Bingbot',
    description: 'Microsoft Bing\'s web crawler',
    category: 'Search Crawlers',
  },

  // Search Engine Crawlers - Others
  {
    value: 'Mozilla/5.0 (compatible; Baiduspider/2.0; +http://www.baidu.com/search/spider.html)',
    label: 'Baiduspider',
    description: 'Baidu\'s web crawler (Chinese search engine)',
    category: 'Search Crawlers',
  },
  {
    value: 'Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)',
    label: 'YandexBot',
    description: 'Yandex\'s web crawler (Russian search engine)',
    category: 'Search Crawlers',
  },
  {
    value: 'DuckDuckBot/1.1; (+http://duckduckgo.com/duckduckbot.html)',
    label: 'DuckDuckBot',
    description: 'DuckDuckGo\'s web crawler',
    category: 'Search Crawlers',
  },

  // AI/LLM Crawlers - OpenAI
  {
    value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.1; +https://openai.com/gptbot',
    label: 'GPTBot',
    description: 'OpenAI\'s crawler for training ChatGPT models',
    category: 'AI/LLM Crawlers',
  },
  {
    value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot',
    label: 'ChatGPT-User',
    description: 'OpenAI\'s on-demand fetcher for ChatGPT browsing',
    category: 'AI/LLM Crawlers',
  },
  {
    value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot',
    label: 'OAI-SearchBot',
    description: 'OpenAI\'s search indexing crawler for ChatGPT Search',
    category: 'AI/LLM Crawlers',
  },

  // AI/LLM Crawlers - Anthropic
  {
    value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)',
    label: 'ClaudeBot',
    description: 'Anthropic\'s crawler for training Claude models',
    category: 'AI/LLM Crawlers',
  },

  // AI/LLM Crawlers - Others
  {
    value: 'PerplexityBot/1.0',
    label: 'PerplexityBot',
    description: 'Perplexity AI\'s web crawler',
    category: 'AI/LLM Crawlers',
  },
  {
    value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Google-Extended)',
    label: 'Google-Extended',
    description: 'Google\'s AI training crawler (can be blocked separately from Googlebot)',
    category: 'AI/LLM Crawlers',
  },
  {
    value: 'CCBot/2.0 (https://commoncrawl.org/faq/)',
    label: 'CCBot',
    description: 'Common Crawl\'s web crawler (used for AI training datasets)',
    category: 'AI/LLM Crawlers',
  },
  {
    value: 'Mozilla/5.0 (compatible; FacebookBot/1.0; +https://developers.facebook.com/docs/sharing/webmasters/crawler)',
    label: 'FacebookBot',
    description: 'Meta\'s crawler for link previews (used for AI training)',
    category: 'AI/LLM Crawlers',
  },

  // SEO Tools
  {
    value: 'Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)',
    label: 'AhrefsBot',
    description: 'Ahrefs SEO tool crawler',
    category: 'SEO Tools',
  },
  {
    value: 'Mozilla/5.0 (compatible; SemrushBot/7~bl; +http://www.semrush.com/bot.html)',
    label: 'SemrushBot',
    description: 'SEMrush SEO tool crawler',
    category: 'SEO Tools',
  },
  {
    value: 'Screaming Frog SEO Spider/20.0',
    label: 'Screaming Frog',
    description: 'Screaming Frog SEO Spider tool',
    category: 'SEO Tools',
  },
  {
    value: 'Mozilla/5.0 (compatible; MJ12bot/v1.4.8; http://mj12bot.com/)',
    label: 'MJ12bot',
    description: 'Majestic SEO crawler',
    category: 'SEO Tools',
  },

  // Social Media Crawlers
  {
    value: 'Twitterbot/1.0',
    label: 'Twitterbot',
    description: 'Twitter/X link preview crawler',
    category: 'Social Media',
  },
  {
    value: 'LinkedInBot/1.0 (compatible; Mozilla/5.0; +http://www.linkedin.com)',
    label: 'LinkedInBot',
    description: 'LinkedIn link preview crawler',
    category: 'Social Media',
  },
  {
    value: 'Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)',
    label: 'Slackbot',
    description: 'Slack link preview crawler',
    category: 'Social Media',
  },
];

/**
 * Get default BlueSnake user-agent
 */
export const DEFAULT_USER_AGENT = 'bluesnake/1.0 (+https://snake.blue)';

/**
 * Get user-agent by category
 */
export function getUserAgentsByCategory(category: string): ComboboxOption[] {
  return USER_AGENTS.filter((ua) => ua.category === category);
}

/**
 * Get all available categories
 */
export function getUserAgentCategories(): string[] {
  const categories = new Set(USER_AGENTS.map((ua) => ua.category || 'Other'));
  return Array.from(categories).sort();
}
