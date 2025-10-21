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
import { Icon } from '../Icon';
import './Checkbox.css';

export interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'type'> {
  label?: string;
  description?: string;
}

export const Checkbox = React.forwardRef<HTMLInputElement, CheckboxProps>(
  ({ label, description, className, id, ...props }, ref) => {
    const inputId = id || `checkbox-${Math.random().toString(36).substr(2, 9)}`;

    if (!label && !description) {
      // Standalone checkbox without label
      return (
        <div className={classNames('ds-checkbox-wrapper', className)}>
          <input
            ref={ref}
            type="checkbox"
            id={inputId}
            className="ds-checkbox-input"
            {...props}
          />
          <div className="ds-checkbox-box">
            <Icon name="check" size={14} strokeWidth={2.5} />
          </div>
        </div>
      );
    }

    return (
      <label htmlFor={inputId} className={classNames('ds-checkbox-label', className)}>
        <div className="ds-checkbox-wrapper">
          <input
            ref={ref}
            type="checkbox"
            id={inputId}
            className="ds-checkbox-input"
            {...props}
          />
          <div className="ds-checkbox-box">
            <Icon name="check" size={14} strokeWidth={2.5} />
          </div>
        </div>
        {(label || description) && (
          <div className="ds-checkbox-content">
            {label && <span className="ds-checkbox-label-text">{label}</span>}
            {description && <p className="ds-checkbox-description">{description}</p>}
          </div>
        )}
      </label>
    );
  }
);

Checkbox.displayName = 'Checkbox';
