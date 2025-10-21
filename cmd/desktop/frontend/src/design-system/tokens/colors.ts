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
 * Color tokens for the BlueSnake design system
 */

export const colors = {
  // Neutral colors - used for backgrounds, borders, text
  neutral: {
    50: '#fafafa',
    100: '#f5f5f5',
    200: '#e0e0e0',
    300: '#d0d0d0',
    400: '#999999',
    500: '#666666',
    600: '#333333',
    700: '#1a1a1a',
    800: '#000000',
  },

  // Semantic colors - used for status, feedback
  semantic: {
    success: '#4caf50',
    successLight: '#81c784',
    successDark: '#388e3c',

    error: '#f44336',
    errorLight: '#e57373',
    errorDark: '#d32f2f',

    warning: '#ff9800',
    warningLight: '#ffb74d',
    warningDark: '#f57c00',

    info: '#2196f3',
    infoLight: '#64b5f6',
    infoDark: '#1976d2',
  },

  // HTTP status code colors
  status: {
    '2xx': '#4caf50', // Success (200-299)
    '3xx': '#2196f3', // Redirect (300-399)
    '4xx': '#ff9800', // Client Error (400-499)
    '5xx': '#f44336', // Server Error (500-599)
    '0': '#f44336',   // Network/timeout errors
  },

  // Content type colors
  contentType: {
    html: '#2196f3',
    javascript: '#f7df1e',
    css: '#264de4',
    image: '#4caf50',
    font: '#9c27b0',
    other: '#757575',
  },

  // Special purpose colors
  special: {
    overlay: 'rgba(0, 0, 0, 0.5)',
    selection: '#e3f2fd',
    selectionText: '#1a1a1a',
    focus: '#999999',
    link: '#2196f3',
    linkHover: '#1976d2',
  },
} as const;

export type ColorToken = typeof colors;
