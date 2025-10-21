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
import './Button.css';

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
export type ButtonSize = 'small' | 'medium' | 'large';

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  icon?: React.ReactNode;
  children?: React.ReactNode;
  style?: React.CSSProperties;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      variant = 'primary',
      size = 'medium',
      loading = false,
      icon,
      className,
      disabled,
      children,
      style,
      ...props
    },
    ref
  ) => {
    const isDisabled = disabled || loading;

    return (
      <button
        ref={ref}
        className={classNames(
          'ds-button',
          `ds-button--${variant}`,
          `ds-button--${size}`,
          loading && 'ds-button--loading',
          className
        )}
        disabled={isDisabled}
        style={style}
        {...props}
      >
        {loading && (
          <span className="ds-button__loader">
            <Icon name="loader" size={16} />
          </span>
        )}
        {!loading && icon && <span className="ds-button__icon">{icon}</span>}
        {children && <span className="ds-button__text">{children}</span>}
      </button>
    );
  }
);

Button.displayName = 'Button';
