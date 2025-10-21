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

import { useState, useEffect, RefObject } from 'react';

export type Position = 'bottom-left' | 'bottom-right' | 'top-left' | 'top-right';

export interface PositionCoordinates {
  top?: number;
  bottom?: number;
  left?: number;
  right?: number;
}

/**
 * Hook that calculates the position of a menu relative to a trigger element
 *
 * @param triggerRef - Reference to the trigger element
 * @param menuRef - Reference to the menu element
 * @param preferredPosition - Preferred position for the menu
 * @returns Position coordinates for the menu
 *
 * @example
 * const { position } = usePosition(triggerRef, menuRef, 'bottom-right');
 * <div style={{ top: position.top, right: position.right }} />
 */
export function usePosition(
  triggerRef: RefObject<HTMLElement>,
  menuRef: RefObject<HTMLElement>,
  preferredPosition: Position = 'bottom-left'
): { position: PositionCoordinates } {
  const [position, setPosition] = useState<PositionCoordinates>({});

  useEffect(() => {
    if (!triggerRef.current) return;

    const updatePosition = () => {
      const triggerRect = triggerRef.current!.getBoundingClientRect();
      const menuHeight = menuRef.current?.offsetHeight || 0;
      const menuWidth = menuRef.current?.offsetWidth || 0;

      const viewportHeight = window.innerHeight;
      const viewportWidth = window.innerWidth;

      let newPosition: PositionCoordinates = {};

      // Determine vertical position
      const spaceBelow = viewportHeight - triggerRect.bottom;
      const spaceAbove = triggerRect.top;
      const shouldPlaceAbove = preferredPosition.startsWith('top')
        ? spaceAbove >= menuHeight || spaceAbove > spaceBelow
        : spaceBelow < menuHeight && spaceAbove > spaceBelow;

      if (shouldPlaceAbove) {
        newPosition.bottom = viewportHeight - triggerRect.top + 4;
      } else {
        newPosition.top = triggerRect.bottom + 4;
      }

      // Determine horizontal position
      if (preferredPosition.endsWith('right')) {
        const spaceOnRight = viewportWidth - triggerRect.right;
        if (spaceOnRight >= menuWidth) {
          newPosition.right = viewportWidth - triggerRect.right;
        } else {
          newPosition.left = triggerRect.left;
        }
      } else {
        const spaceOnLeft = triggerRect.left;
        if (spaceOnLeft >= menuWidth) {
          newPosition.left = triggerRect.left;
        } else {
          newPosition.right = viewportWidth - triggerRect.right;
        }
      }

      setPosition(newPosition);
    };

    updatePosition();
    window.addEventListener('resize', updatePosition);
    window.addEventListener('scroll', updatePosition);

    return () => {
      window.removeEventListener('resize', updatePosition);
      window.removeEventListener('scroll', updatePosition);
    };
  }, [triggerRef, menuRef, preferredPosition]);

  return { position };
}
