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

import React, { useRef, useState, useEffect } from 'react';
import { classNames } from '../../utils';
import { useClickOutside, usePosition, Position } from '../../hooks';
import { Icon } from '../Icon';
import './Combobox.css';

export interface ComboboxOption {
  value: string;
  label: string;
  description?: string;
  category?: string;
  disabled?: boolean;
}

export interface ComboboxProps {
  value: string;
  options: ComboboxOption[];
  onChange: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  position?: Position;
  allowCustomValue?: boolean;
  filterOptions?: (options: ComboboxOption[], inputValue: string) => ComboboxOption[];
}

const defaultFilterOptions = (options: ComboboxOption[], inputValue: string): ComboboxOption[] => {
  if (!inputValue.trim()) {
    return options;
  }

  const searchTerm = inputValue.toLowerCase();
  return options.filter((option) => {
    const labelMatch = option.label.toLowerCase().includes(searchTerm);
    const valueMatch = option.value.toLowerCase().includes(searchTerm);
    const descriptionMatch = option.description?.toLowerCase().includes(searchTerm);
    const categoryMatch = option.category?.toLowerCase().includes(searchTerm);

    return labelMatch || valueMatch || descriptionMatch || categoryMatch;
  });
};

export function Combobox({
  value,
  options,
  onChange,
  placeholder = 'Type or select an option',
  disabled = false,
  className,
  position = 'bottom-left',
  allowCustomValue = true,
  filterOptions = defaultFilterOptions,
}: ComboboxProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [inputValue, setInputValue] = useState(value);
  const [filteredOptions, setFilteredOptions] = useState(options);
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const [showAllOptions, setShowAllOptions] = useState(false);

  const comboboxRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const inputWrapperRef = useRef<HTMLDivElement>(null);

  const { position: menuPosition } = usePosition(inputWrapperRef, menuRef, position);

  useClickOutside(comboboxRef, () => {
    setIsOpen(false);
    // Reset input value to the actual value if not allowing custom values
    if (!allowCustomValue && inputValue !== value) {
      setInputValue(value);
    }
  }, isOpen);

  // Update input value when prop value changes
  useEffect(() => {
    setInputValue(value);
  }, [value]);

  // Update filtered options when input value or options change
  useEffect(() => {
    // If showing all options (dropdown arrow clicked), show all
    if (showAllOptions) {
      setFilteredOptions(options);
    } else {
      // Otherwise filter based on input value
      const filtered = filterOptions(options, inputValue);
      setFilteredOptions(filtered);
    }
    setHighlightedIndex(-1);
  }, [inputValue, options, filterOptions, showAllOptions]);

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;
    setInputValue(newValue);
    setIsOpen(true);
    setShowAllOptions(false); // User is typing, so filter results

    // If custom values are allowed, update the value immediately
    if (allowCustomValue) {
      onChange(newValue);
    }
  };

  const handleInputFocus = () => {
    // Only open if not already open (avoid interfering with dropdown toggle)
    if (!isOpen) {
      setIsOpen(true);
      setShowAllOptions(false);
    }
  };

  const handleSelect = (option: ComboboxOption) => {
    if (option.disabled) return;

    setInputValue(option.value);
    onChange(option.value);
    setIsOpen(false);
    inputRef.current?.blur();
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (disabled) return;

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        if (!isOpen) {
          // Opening with arrow keys shows all options
          setShowAllOptions(true);
          setIsOpen(true);
        } else {
          setHighlightedIndex((prev) =>
            prev < filteredOptions.length - 1 ? prev + 1 : 0
          );
        }
        break;

      case 'ArrowUp':
        e.preventDefault();
        if (!isOpen) {
          // Opening with arrow keys shows all options
          setShowAllOptions(true);
          setIsOpen(true);
        } else {
          setHighlightedIndex((prev) =>
            prev > 0 ? prev - 1 : filteredOptions.length - 1
          );
        }
        break;

      case 'Enter':
        e.preventDefault();
        if (isOpen && highlightedIndex >= 0 && highlightedIndex < filteredOptions.length) {
          handleSelect(filteredOptions[highlightedIndex]);
        }
        break;

      case 'Escape':
        e.preventDefault();
        setIsOpen(false);
        inputRef.current?.blur();
        break;

      case 'Tab':
        setIsOpen(false);
        break;
    }
  };

  const handleToggleDropdown = () => {
    if (disabled) return;

    if (isOpen) {
      setIsOpen(false);
      setShowAllOptions(false);
    } else {
      // When clicking the dropdown arrow, show ALL options
      // Set both states before focusing to avoid race condition
      setShowAllOptions(true);
      setIsOpen(true);
      // Focus after state update to ensure showAllOptions is already true
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  };

  // Group options by category
  const groupedOptions: { category: string; options: ComboboxOption[] }[] = [];
  const categoriesMap = new Map<string, ComboboxOption[]>();

  filteredOptions.forEach((option) => {
    const category = option.category || 'Other';
    if (!categoriesMap.has(category)) {
      categoriesMap.set(category, []);
    }
    categoriesMap.get(category)!.push(option);
  });

  categoriesMap.forEach((opts, category) => {
    groupedOptions.push({ category, options: opts });
  });

  return (
    <div ref={comboboxRef} className={classNames('ds-combobox', className)}>
      <div ref={inputWrapperRef} className="ds-combobox__input-wrapper">
        <input
          ref={inputRef}
          type="text"
          className={classNames(
            'ds-combobox__input',
            disabled && 'ds-combobox__input--disabled'
          )}
          value={inputValue}
          onChange={handleInputChange}
          onFocus={handleInputFocus}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={disabled}
          role="combobox"
          aria-expanded={isOpen}
          aria-controls="combobox-menu"
          aria-autocomplete="list"
        />
        <button
          type="button"
          className={classNames(
            'ds-combobox__toggle',
            disabled && 'ds-combobox__toggle--disabled'
          )}
          onClick={handleToggleDropdown}
          disabled={disabled}
          tabIndex={-1}
          aria-label="Toggle dropdown"
        >
          <Icon
            name="chevron-down"
            size={12}
            className={classNames(
              'ds-combobox__arrow',
              isOpen && 'ds-combobox__arrow--open'
            )}
          />
        </button>
      </div>

      {isOpen && !disabled && (
        <div
          ref={menuRef}
          id="combobox-menu"
          className="ds-combobox__menu"
          style={{
            ...menuPosition,
            width: inputWrapperRef.current?.offsetWidth || 'auto',
          }}
          role="listbox"
        >
          {filteredOptions.length === 0 ? (
            <div className="ds-combobox__empty">
              {allowCustomValue
                ? 'Type to enter a custom value'
                : 'No options found'
              }
            </div>
          ) : (
            groupedOptions.map((group) => (
              <div key={group.category} className="ds-combobox__group">
                {group.category !== 'Other' && (
                  <div className="ds-combobox__group-label">{group.category}</div>
                )}
                {group.options.map((option, index) => {
                  const globalIndex = filteredOptions.indexOf(option);
                  const isHighlighted = globalIndex === highlightedIndex;
                  const isSelected = option.value === value;

                  return (
                    <div
                      key={option.value}
                      className={classNames(
                        'ds-combobox__option',
                        isSelected && 'ds-combobox__option--selected',
                        isHighlighted && 'ds-combobox__option--highlighted',
                        option.disabled && 'ds-combobox__option--disabled'
                      )}
                      onClick={() => handleSelect(option)}
                      onMouseEnter={() => setHighlightedIndex(globalIndex)}
                      role="option"
                      aria-selected={isSelected}
                      aria-disabled={option.disabled}
                    >
                      <div className="ds-combobox__option-content">
                        <div className="ds-combobox__option-label">
                          {option.label}
                        </div>
                        {option.description && (
                          <div className="ds-combobox__option-description">
                            {option.description}
                          </div>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}
