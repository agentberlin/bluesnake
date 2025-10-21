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

import React, { useRef, useState } from 'react';
import { classNames } from '../../utils';
import { useClickOutside, usePosition, Position } from '../../hooks';
import { Icon } from '../Icon';
import './Dropdown.css';

export interface DropdownOption<T = any> {
  value: T;
  label: string;
  disabled?: boolean;
}

export interface DropdownProps<T = any> {
  value: T;
  options: DropdownOption<T>[];
  onChange: (value: T) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  position?: Position;
  renderOption?: (option: DropdownOption<T>) => React.ReactNode;
}

export function Dropdown<T = any>({
  value,
  options,
  onChange,
  placeholder = 'Select option',
  disabled = false,
  className,
  position = 'bottom-left',
  renderOption,
}: DropdownProps<T>) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const { position: menuPosition } = usePosition(triggerRef, menuRef, position);

  useClickOutside(dropdownRef, () => setIsOpen(false), isOpen);

  const selectedOption = options.find((opt) => opt.value === value);

  const handleSelect = (option: DropdownOption<T>) => {
    if (option.disabled) return;
    onChange(option.value);
    setIsOpen(false);
  };

  return (
    <div ref={dropdownRef} className={classNames('ds-dropdown', className)}>
      <button
        ref={triggerRef}
        type="button"
        className={classNames(
          'ds-dropdown__trigger',
          isOpen && 'ds-dropdown__trigger--open',
          disabled && 'ds-dropdown__trigger--disabled'
        )}
        onClick={() => !disabled && setIsOpen(!isOpen)}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
      >
        <span className="ds-dropdown__value">
          {selectedOption ? selectedOption.label : placeholder}
        </span>
        <Icon name="chevron-down" size={12} className="ds-dropdown__arrow" />
      </button>

      {isOpen && !disabled && (
        <div
          ref={menuRef}
          className="ds-dropdown__menu"
          style={menuPosition}
          role="listbox"
        >
          {options.map((option, index) => {
            const isSelected = option.value === value;
            const content = renderOption ? renderOption(option) : option.label;

            return (
              <div
                key={index}
                className={classNames(
                  'ds-dropdown__option',
                  isSelected && 'ds-dropdown__option--selected',
                  option.disabled && 'ds-dropdown__option--disabled'
                )}
                onClick={() => handleSelect(option)}
                role="option"
                aria-selected={isSelected}
                aria-disabled={option.disabled}
              >
                {content}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
