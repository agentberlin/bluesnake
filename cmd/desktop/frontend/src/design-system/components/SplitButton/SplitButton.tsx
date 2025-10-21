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
  /** Menu items for the dropdown */
  menuItems: MenuItem[];
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
  variant = 'primary',
  size = 'medium',
  disabled = false,
  loading = false,
  icon,
  menuPosition = 'bottom-left',
}: SplitButtonProps) {
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
        items={menuItems}
        position={menuPosition}
      />
    </div>
  );
}
