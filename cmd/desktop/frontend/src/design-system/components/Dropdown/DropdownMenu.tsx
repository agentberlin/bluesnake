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

import React, { useRef, useState, ReactElement } from 'react';
import { classNames } from '../../utils';
import { useClickOutside, usePosition, Position } from '../../hooks';
import { Checkbox } from '../Checkbox';
import './Dropdown.css';

export interface DropdownMenuItem {
  id: string;
  label: string;
  checked: boolean;
  disabled?: boolean;
}

export interface DropdownMenuProps {
  trigger: ReactElement;
  items: DropdownMenuItem[];
  onItemToggle: (id: string) => void;
  className?: string;
  position?: Position;
  disabled?: boolean;
}

/**
 * DropdownMenu - Multi-select dropdown with checkboxes
 * Perfect for column selectors, filters, etc.
 *
 * @example
 * <DropdownMenu
 *   trigger={<Button>Columns (3)</Button>}
 *   items={[
 *     { id: 'url', label: 'URL', checked: true },
 *     { id: 'status', label: 'Status', checked: true },
 *   ]}
 *   onItemToggle={(id) => handleToggle(id)}
 * />
 */
export const DropdownMenu: React.FC<DropdownMenuProps> = ({
  trigger,
  items,
  onItemToggle,
  className,
  position = 'bottom-right',
  disabled = false,
}) => {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const { position: menuPosition } = usePosition(triggerRef, menuRef, position);

  useClickOutside(dropdownRef, () => setIsOpen(false), isOpen);

  const handleTriggerClick = () => {
    if (!disabled) {
      setIsOpen(!isOpen);
    }
  };

  const handleItemClick = (e: React.MouseEvent, itemId: string, itemDisabled?: boolean) => {
    e.stopPropagation();
    if (!itemDisabled) {
      onItemToggle(itemId);
    }
  };

  return (
    <div ref={dropdownRef} className={classNames('ds-dropdown-menu', className)}>
      <div ref={triggerRef} onClick={handleTriggerClick}>
        {React.cloneElement(trigger, {
          'aria-haspopup': 'menu',
          'aria-expanded': isOpen,
          disabled: disabled || trigger.props.disabled,
        })}
      </div>

      {isOpen && !disabled && (
        <div
          ref={menuRef}
          className="ds-dropdown-menu__list"
          style={menuPosition}
          role="menu"
        >
          {items.map((item) => (
            <label
              key={item.id}
              className={classNames(
                'ds-dropdown-menu__item',
                item.disabled && 'ds-dropdown-menu__item--disabled'
              )}
              onClick={(e) => handleItemClick(e, item.id, item.disabled)}
            >
              <Checkbox
                checked={item.checked}
                onChange={() => {}}
                disabled={item.disabled}
              />
              <span className="ds-dropdown-menu__item-label">{item.label}</span>
            </label>
          ))}
        </div>
      )}
    </div>
  );
};
