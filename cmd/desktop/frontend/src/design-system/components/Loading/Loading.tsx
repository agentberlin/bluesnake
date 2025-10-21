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
import './Loading.css';

export type LoadingSize = 'small' | 'medium' | 'large';

export interface SpinnerProps {
  size?: LoadingSize;
  className?: string;
}

export const Spinner: React.FC<SpinnerProps> = ({ size = 'medium', className }) => {
  const sizeMap = {
    small: 16,
    medium: 24,
    large: 32,
  };

  return (
    <div className={classNames('ds-spinner', `ds-spinner--${size}`, className)}>
      <Icon name="loader" size={sizeMap[size]} />
    </div>
  );
};

export interface LoadingProps {
  text?: string;
  size?: LoadingSize;
  className?: string;
}

export const Loading: React.FC<LoadingProps> = ({
  text = 'Loading...',
  size = 'medium',
  className,
}) => {
  return (
    <div className={classNames('ds-loading', className)}>
      <Spinner size={size} />
      {text && <span className="ds-loading__text">{text}</span>}
    </div>
  );
};

Spinner.displayName = 'Spinner';
Loading.displayName = 'Loading';
