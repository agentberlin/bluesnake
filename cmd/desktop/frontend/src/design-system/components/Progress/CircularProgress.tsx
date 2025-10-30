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
import './Progress.css';

export interface CircularProgressProps {
  value: number; // Current value
  max: number; // Maximum value
  size?: number; // Size in pixels (default: 80)
  strokeWidth?: number; // Stroke width (default: 3)
  showLabel?: boolean; // Show value label (default: true)
  showPercentage?: boolean; // Show percentage instead of value/max (default: false)
  className?: string;
}

export const CircularProgress: React.FC<CircularProgressProps> = ({
  value,
  max,
  size = 80,
  strokeWidth = 3,
  showLabel = true,
  showPercentage = false,
  className,
}) => {
  const percentage = max > 0 ? (value / max) * 100 : 0;
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (percentage / 100) * circumference;

  // Scale font size based on component size for better readability
  // For external labels, use a slightly larger font size
  const fontSize = size < 40 ? Math.max(10, size * 0.6) : 12;

  return (
    <div
      className={classNames('ds-circular-progress', 'ds-circular-progress--with-external-label', className)}
    >
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        {/* Background circle */}
        <circle
          className="ds-circular-progress__bg"
          cx={size / 2}
          cy={size / 2}
          r={radius}
          strokeWidth={strokeWidth}
          fill="none"
        />
        {/* Progress circle */}
        <circle
          className="ds-circular-progress__progress"
          cx={size / 2}
          cy={size / 2}
          r={radius}
          strokeWidth={strokeWidth}
          fill="none"
          strokeDasharray={circumference}
          strokeDashoffset={strokeDashoffset}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
        />
      </svg>
      {showLabel && (
        <div className="ds-circular-progress__label-external" style={{ fontSize: `${fontSize}px` }}>
          {showPercentage ? (
            <span className="ds-circular-progress__percentage">{percentage.toFixed(0)}%</span>
          ) : (
            <span className="ds-circular-progress__value">
              {value} / {max}
            </span>
          )}
        </div>
      )}
    </div>
  );
};

CircularProgress.displayName = 'CircularProgress';
