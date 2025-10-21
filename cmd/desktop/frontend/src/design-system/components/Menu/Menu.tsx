import React, { useState, useRef } from 'react';
import { useClickOutside } from '../../hooks/useClickOutside';
import { useKeyPress } from '../../hooks/useKeyPress';
import { usePosition } from '../../hooks/usePosition';
import './Menu.css';

export interface MenuItem {
  id: string;
  label: string;
  description?: string;
  icon?: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  variant?: 'default' | 'danger';
}

export interface MenuProps {
  trigger: React.ReactElement;
  items: MenuItem[];
  position?: 'bottom-left' | 'bottom-right' | 'top-left' | 'top-right';
  minWidth?: number;
}

export function Menu({ trigger, items, position = 'bottom-left', minWidth = 200 }: MenuProps) {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const { position: positionStyles } = usePosition(containerRef, menuRef, position);

  useClickOutside(containerRef, () => setIsOpen(false), isOpen);
  useKeyPress('Escape', () => setIsOpen(false), isOpen);

  const handleItemClick = (item: MenuItem) => {
    if (item.disabled) return;
    item.onClick();
    setIsOpen(false);
  };

  const triggerElement = React.cloneElement(trigger, {
    onClick: (e: React.MouseEvent) => {
      e.stopPropagation();
      setIsOpen(!isOpen);
      trigger.props.onClick?.(e);
    },
  });

  return (
    <div className="ds-menu-container" ref={containerRef}>
      {triggerElement}
      {isOpen && (
        <div
          ref={menuRef}
          className="ds-menu"
          style={{ ...positionStyles, minWidth }}
        >
          {items.map((item) => (
            <div
              key={item.id}
              className={`ds-menu-item ${item.disabled ? 'ds-menu-item--disabled' : ''} ${
                item.variant === 'danger' ? 'ds-menu-item--danger' : ''
              }`}
              onClick={() => handleItemClick(item)}
            >
              {item.icon && <div className="ds-menu-item-icon">{item.icon}</div>}
              <div className="ds-menu-item-content">
                <div className="ds-menu-item-label">{item.label}</div>
                {item.description && (
                  <div className="ds-menu-item-description">{item.description}</div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
