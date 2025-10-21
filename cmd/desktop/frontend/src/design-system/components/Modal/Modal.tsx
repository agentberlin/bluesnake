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

import React, { useEffect } from 'react';
import { classNames } from '../../utils';
import { useKeyPress } from '../../hooks';
import { Icon } from '../Icon';
import './Modal.css';

export type ModalVariant = 'default' | 'warning' | 'danger';
export type ModalSize = 'small' | 'medium' | 'large';

export interface ModalProps {
  isOpen: boolean;
  onClose: () => void;
  title?: string;
  variant?: ModalVariant;
  size?: ModalSize;
  closeOnOverlayClick?: boolean;
  closeOnEscape?: boolean;
  showCloseButton?: boolean;
  children: React.ReactNode;
  className?: string;
}

export const Modal: React.FC<ModalProps> = ({
  isOpen,
  onClose,
  title,
  variant = 'default',
  size = 'medium',
  closeOnOverlayClick = true,
  closeOnEscape = true,
  showCloseButton = true,
  children,
  className,
}) => {
  useKeyPress('Escape', onClose, isOpen && closeOnEscape);

  useEffect(() => {
    if (isOpen) {
      document.body.style.overflow = 'hidden';
    } else {
      document.body.style.overflow = '';
    }

    return () => {
      document.body.style.overflow = '';
    };
  }, [isOpen]);

  if (!isOpen) return null;

  const handleOverlayClick = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget && closeOnOverlayClick) {
      onClose();
    }
  };

  return (
    <div className="ds-modal-overlay" onClick={handleOverlayClick}>
      <div
        className={classNames(
          'ds-modal',
          `ds-modal--${variant}`,
          `ds-modal--${size}`,
          className
        )}
        role="dialog"
        aria-modal="true"
        aria-labelledby={title ? 'modal-title' : undefined}
      >
        {(title || showCloseButton) && (
          <div className="ds-modal__header">
            {title && (
              <h3 id="modal-title" className="ds-modal__title">
                {title}
              </h3>
            )}
            {showCloseButton && (
              <button
                className="ds-modal__close-button"
                onClick={onClose}
                aria-label="Close modal"
              >
                <Icon name="x" size={18} />
              </button>
            )}
          </div>
        )}
        <div className="ds-modal__content">{children}</div>
      </div>
    </div>
  );
};

// Sub-components for better composition
export const ModalContent: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className,
}) => {
  return <div className={classNames('ds-modal__body', className)}>{children}</div>;
};

export const ModalActions: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className,
}) => {
  return <div className={classNames('ds-modal__actions', className)}>{children}</div>;
};

Modal.displayName = 'Modal';
ModalContent.displayName = 'Modal.Content';
ModalActions.displayName = 'Modal.Actions';
