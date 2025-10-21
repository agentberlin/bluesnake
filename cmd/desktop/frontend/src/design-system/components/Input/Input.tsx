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

import React from 'react';
import { classNames } from '../../utils';
import './Input.css';

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  hint?: string;
  leftIcon?: React.ReactNode;
  rightIcon?: React.ReactNode;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  (
    {
      label,
      error,
      hint,
      leftIcon,
      rightIcon,
      className,
      id,
      disabled,
      ...props
    },
    ref
  ) => {
    const inputId = id || `input-${Math.random().toString(36).substr(2, 9)}`;
    const hasError = !!error;

    return (
      <div className={classNames('ds-input-container', className)}>
        {label && (
          <label htmlFor={inputId} className="ds-input-label">
            {label}
          </label>
        )}
        <div className="ds-input-wrapper">
          {leftIcon && <span className="ds-input-icon ds-input-icon--left">{leftIcon}</span>}
          <input
            ref={ref}
            id={inputId}
            className={classNames(
              'ds-input',
              hasError && 'ds-input--error',
              !!leftIcon && 'ds-input--has-left-icon',
              !!rightIcon && 'ds-input--has-right-icon',
              !!disabled && 'ds-input--disabled'
            )}
            disabled={disabled}
            aria-invalid={hasError}
            aria-describedby={error ? `${inputId}-error` : hint ? `${inputId}-hint` : undefined}
            {...props}
          />
          {rightIcon && <span className="ds-input-icon ds-input-icon--right">{rightIcon}</span>}
        </div>
        {error && (
          <span id={`${inputId}-error`} className="ds-input-error">
            {error}
          </span>
        )}
        {hint && !error && (
          <span id={`${inputId}-hint`} className="ds-input-hint">
            {hint}
          </span>
        )}
      </div>
    );
  }
);

Input.displayName = 'Input';
