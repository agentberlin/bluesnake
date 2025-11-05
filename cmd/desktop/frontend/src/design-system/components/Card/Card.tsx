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
import './Card.css';

export type CardVariant = 'default' | 'outlined' | 'elevated';

export interface CardProps {
  variant?: CardVariant;
  hoverable?: boolean;
  onClick?: () => void;
  className?: string;
  children: React.ReactNode;
}

export const Card: React.FC<CardProps> = ({
  variant = 'default',
  hoverable = false,
  onClick,
  className,
  children,
}) => {
  return (
    <div
      className={classNames(
        'ds-card',
        `ds-card--${variant}`,
        hoverable && 'ds-card--hoverable',
        onClick && 'ds-card--clickable',
        className
      )}
      onClick={onClick}
      role={onClick ? 'button' : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={onClick ? (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick();
        }
      } : undefined}
    >
      {children}
    </div>
  );
};

export const CardHeader: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className,
}) => {
  return <div className={classNames('ds-card__header', className)}>{children}</div>;
};

export const CardContent: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className,
}) => {
  return <div className={classNames('ds-card__content', className)}>{children}</div>;
};

export const CardFooter: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className,
}) => {
  return <div className={classNames('ds-card__footer', className)}>{children}</div>;
};

Card.displayName = 'Card';
CardHeader.displayName = 'Card.Header';
CardContent.displayName = 'Card.Content';
CardFooter.displayName = 'Card.Footer';
