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

import { useEffect } from 'react';

/**
 * Hook that detects when a specific key is pressed
 *
 * @param key - The key to listen for (e.g., 'Escape', 'Enter')
 * @param handler - Callback function to execute when the key is pressed
 * @param enabled - Whether the listener is enabled (default: true)
 *
 * @example
 * useKeyPress('Escape', () => setIsOpen(false));
 */
export function useKeyPress(
  key: string,
  handler: (event: KeyboardEvent) => void,
  enabled: boolean = true
): void {
  useEffect(() => {
    if (!enabled) return;

    const handleKeyPress = (event: KeyboardEvent) => {
      if (event.key === key) {
        handler(event);
      }
    };

    document.addEventListener('keydown', handleKeyPress);
    return () => document.removeEventListener('keydown', handleKeyPress);
  }, [key, handler, enabled]);
}
