import React from 'react';
import { Button, ButtonProps } from '../Button/Button';
import { Menu, MenuItem } from '../Menu/Menu';
import { Icon } from '../Icon/Icon';
import './SplitButton.css';

export interface SplitButtonProps {
  /** The label for the primary action button */
  label: string;
  /** Handler for the primary action */
  onClick: () => void;
  /** Menu items for the dropdown (if using menu mode) */
  menuItems?: MenuItem[];
  /** Secondary button action (alternative to menu) */
  secondaryAction?: {
    /** Handler for the secondary action */
    onClick: () => void;
    /** Icon for the secondary button */
    icon: React.ReactNode;
    /** Variant for the secondary button */
    variant?: ButtonProps['variant'];
    /** Whether the secondary button is disabled */
    disabled?: boolean;
  };
  /** Button variant */
  variant?: ButtonProps['variant'];
  /** Button size */
  size?: ButtonProps['size'];
  /** Whether the button is disabled */
  disabled?: boolean;
  /** Whether the button is in loading state */
  loading?: boolean;
  /** Icon for the primary button */
  icon?: React.ReactNode;
  /** Position of the menu dropdown */
  menuPosition?: 'bottom-left' | 'bottom-right' | 'top-left' | 'top-right';
}

export function SplitButton({
  label,
  onClick,
  menuItems,
  secondaryAction,
  variant = 'primary',
  size = 'medium',
  disabled = false,
  loading = false,
  icon,
  menuPosition = 'bottom-left',
}: SplitButtonProps) {
  // Validate that either menuItems or secondaryAction is provided, but not both
  if (menuItems && secondaryAction) {
    console.warn('SplitButton: Both menuItems and secondaryAction provided. Using secondaryAction.');
  }

  // If neither menuItems nor secondaryAction is provided, render as a simple button
  if (!menuItems && !secondaryAction) {
    return (
      <Button
        variant={variant}
        size={size}
        disabled={disabled}
        loading={loading}
        onClick={onClick}
        icon={icon}
      >
        {label}
      </Button>
    );
  }

  return (
    <div className="ds-split-button">
      <Button
        variant={variant}
        size={size}
        disabled={disabled}
        loading={loading}
        onClick={onClick}
        icon={icon}
        className="ds-split-button-main"
      >
        {label}
      </Button>
      {secondaryAction ? (
        <Button
          variant={secondaryAction.variant || variant}
          size={size}
          disabled={secondaryAction.disabled || disabled}
          onClick={secondaryAction.onClick}
          className="ds-split-button-secondary"
          icon={secondaryAction.icon}
        />
      ) : (
        <Menu
          trigger={
            <Button
              variant={variant}
              size={size}
              disabled={disabled}
              className="ds-split-button-dropdown"
              icon={<Icon name="chevron-down" size={14} />}
            />
          }
          items={menuItems || []}
          position={menuPosition}
        />
      )}
    </div>
  );
}
