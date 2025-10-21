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

import React, { useState } from 'react';
import {
  Button,
  Input,
  Checkbox,
  Badge,
  Icon,
  Dropdown,
  DropdownMenu,
  Modal,
  ModalContent,
  ModalActions,
  Loading,
  Spinner,
  CircularProgress,
} from '../design-system';
import './ComponentCatalog.css';

/**
 * Component Catalog
 *
 * Browse and test all available components in the BlueSnake design system.
 * This catalog helps you discover existing components and see their variations.
 */
export const ComponentCatalog: React.FC = () => {
  const [activeSection, setActiveSection] = useState<string>('buttons');

  // Demo state
  const [checkboxChecked, setCheckboxChecked] = useState(false);
  const [inputValue, setInputValue] = useState('');
  const [dropdownValue, setDropdownValue] = useState('option1');
  const [modalOpen, setModalOpen] = useState(false);
  const [columnVisibility, setColumnVisibility] = useState({
    url: true,
    status: true,
    title: false,
    meta: false,
  });

  const dropdownOptions = [
    { value: 'option1', label: 'Option 1' },
    { value: 'option2', label: 'Option 2' },
    { value: 'option3', label: 'Option 3' },
    { value: 'option4', label: 'Option 4', disabled: true },
  ];

  const columnItems = [
    { id: 'url', label: 'URL', checked: columnVisibility.url },
    { id: 'status', label: 'Status', checked: columnVisibility.status },
    { id: 'title', label: 'Title', checked: columnVisibility.title },
    { id: 'meta', label: 'Meta Description', checked: columnVisibility.meta },
  ];

  const handleColumnToggle = (id: string) => {
    setColumnVisibility((prev) => ({ ...prev, [id]: !prev[id as keyof typeof prev] }));
  };

  const sections = [
    { id: 'buttons', label: 'Buttons' },
    { id: 'inputs', label: 'Inputs' },
    { id: 'checkboxes', label: 'Checkboxes' },
    { id: 'badges', label: 'Badges' },
    { id: 'icons', label: 'Icons' },
    { id: 'dropdowns', label: 'Dropdowns' },
    { id: 'modals', label: 'Modals' },
    { id: 'loading', label: 'Loading' },
    { id: 'progress', label: 'Progress' },
  ];

  const visibleCount = Object.values(columnVisibility).filter(Boolean).length;

  return (
    <div className="catalog">
      <div className="catalog-sidebar">
        <h2 className="catalog-title">Component Catalog</h2>
        <nav className="catalog-nav">
          {sections.map((section) => (
            <button
              key={section.id}
              className={`catalog-nav-item ${
                activeSection === section.id ? 'catalog-nav-item--active' : ''
              }`}
              onClick={() => setActiveSection(section.id)}
            >
              {section.label}
            </button>
          ))}
        </nav>
      </div>

      <div className="catalog-content">
        {/* Buttons */}
        {activeSection === 'buttons' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Buttons</h3>
            <div className="catalog-demo-group">
              <h4>Variants</h4>
              <div className="catalog-demo-row">
                <Button variant="primary">Primary Button</Button>
                <Button variant="secondary">Secondary Button</Button>
                <Button variant="ghost">Ghost Button</Button>
                <Button variant="danger">Danger Button</Button>
              </div>
            </div>
            <div className="catalog-demo-group">
              <h4>Sizes</h4>
              <div className="catalog-demo-row">
                <Button size="small">Small</Button>
                <Button size="medium">Medium</Button>
                <Button size="large">Large</Button>
              </div>
            </div>
            <div className="catalog-demo-group">
              <h4>States</h4>
              <div className="catalog-demo-row">
                <Button disabled>Disabled</Button>
                <Button loading>Loading</Button>
                <Button icon={<Icon name="check" size={16} />}>With Icon</Button>
              </div>
            </div>
          </div>
        )}

        {/* Inputs */}
        {activeSection === 'inputs' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Inputs</h3>
            <div className="catalog-demo-group">
              <h4>Basic Input</h4>
              <Input
                label="Email"
                type="email"
                placeholder="Enter your email"
                value={inputValue}
                onChange={(e) => setInputValue(e.target.value)}
              />
            </div>
            <div className="catalog-demo-group">
              <h4>With Icons</h4>
              <Input
                placeholder="Search..."
                leftIcon={<Icon name="search" size={16} />}
              />
            </div>
            <div className="catalog-demo-group">
              <h4>With Error</h4>
              <Input
                label="Password"
                type="password"
                error="Password must be at least 8 characters"
              />
            </div>
            <div className="catalog-demo-group">
              <h4>With Hint</h4>
              <Input
                label="Username"
                hint="Choose a unique username"
              />
            </div>
          </div>
        )}

        {/* Checkboxes */}
        {activeSection === 'checkboxes' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Checkboxes</h3>
            <div className="catalog-demo-group">
              <h4>Basic Checkbox</h4>
              <Checkbox
                label="Accept terms and conditions"
                checked={checkboxChecked}
                onChange={(e) => setCheckboxChecked(e.target.checked)}
              />
            </div>
            <div className="catalog-demo-group">
              <h4>With Description</h4>
              <Checkbox
                label="Enable JavaScript Rendering"
                description="When enabled, pages will be rendered with a headless browser to execute JavaScript."
                checked={false}
                onChange={() => {}}
              />
            </div>
            <div className="catalog-demo-group">
              <h4>Disabled</h4>
              <Checkbox
                label="Disabled checkbox"
                checked={true}
                disabled
                onChange={() => {}}
              />
            </div>
          </div>
        )}

        {/* Badges */}
        {activeSection === 'badges' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Badges</h3>
            <div className="catalog-demo-group">
              <h4>Variants</h4>
              <div className="catalog-demo-row">
                <Badge variant="default">Default</Badge>
                <Badge variant="success">Success</Badge>
                <Badge variant="error">Error</Badge>
                <Badge variant="warning">Warning</Badge>
                <Badge variant="info">Info</Badge>
                <Badge variant="neutral">Neutral</Badge>
              </div>
            </div>
          </div>
        )}

        {/* Icons */}
        {activeSection === 'icons' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Icons</h3>
            <div className="catalog-demo-group">
              <h4>Available Icons</h4>
              <div className="catalog-icon-grid">
                <div className="catalog-icon-item"><Icon name="chevron-down" /><span>chevron-down</span></div>
                <div className="catalog-icon-item"><Icon name="chevron-up" /><span>chevron-up</span></div>
                <div className="catalog-icon-item"><Icon name="chevron-left" /><span>chevron-left</span></div>
                <div className="catalog-icon-item"><Icon name="chevron-right" /><span>chevron-right</span></div>
                <div className="catalog-icon-item"><Icon name="x" /><span>x</span></div>
                <div className="catalog-icon-item"><Icon name="check" /><span>check</span></div>
                <div className="catalog-icon-item"><Icon name="search" /><span>search</span></div>
                <div className="catalog-icon-item"><Icon name="settings" /><span>settings</span></div>
                <div className="catalog-icon-item"><Icon name="home" /><span>home</span></div>
                <div className="catalog-icon-item"><Icon name="list" /><span>list</span></div>
                <div className="catalog-icon-item"><Icon name="trash" /><span>trash</span></div>
                <div className="catalog-icon-item"><Icon name="grid" /><span>grid</span></div>
                <div className="catalog-icon-item"><Icon name="copy" /><span>copy</span></div>
                <div className="catalog-icon-item"><Icon name="external-link" /><span>external-link</span></div>
                <div className="catalog-icon-item"><Icon name="loader" /><span>loader</span></div>
                <div className="catalog-icon-item"><Icon name="alert-circle" /><span>alert-circle</span></div>
                <div className="catalog-icon-item"><Icon name="alert-triangle" /><span>alert-triangle</span></div>
                <div className="catalog-icon-item"><Icon name="info" /><span>info</span></div>
                <div className="catalog-icon-item"><Icon name="check-circle" /><span>check-circle</span></div>
                <div className="catalog-icon-item"><Icon name="arrow-right" /><span>arrow-right</span></div>
              </div>
            </div>
          </div>
        )}

        {/* Dropdowns */}
        {activeSection === 'dropdowns' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Dropdowns</h3>
            <div className="catalog-demo-group">
              <h4>Single Select Dropdown</h4>
              <Dropdown
                value={dropdownValue}
                options={dropdownOptions}
                onChange={setDropdownValue}
                placeholder="Select an option"
              />
            </div>
            <div className="catalog-demo-group">
              <h4>Multi-Select Dropdown (Checkbox)</h4>
              <DropdownMenu
                trigger={
                  <Button variant="secondary" icon={<Icon name="grid" size={16} />}>
                    Columns ({visibleCount})
                  </Button>
                }
                items={columnItems}
                onItemToggle={handleColumnToggle}
              />
            </div>
          </div>
        )}

        {/* Modals */}
        {activeSection === 'modals' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Modals</h3>
            <div className="catalog-demo-group">
              <h4>Modal Variants</h4>
              <div className="catalog-demo-row">
                <Button onClick={() => setModalOpen(true)}>Open Modal</Button>
              </div>
            </div>
            <Modal
              isOpen={modalOpen}
              onClose={() => setModalOpen(false)}
              title="Example Modal"
              variant="default"
              size="medium"
            >
              <ModalContent>
                <p>This is an example modal dialog. You can put any content here.</p>
                <p>It supports different variants (default, warning, danger) and sizes (small, medium, large).</p>
              </ModalContent>
              <ModalActions>
                <Button variant="secondary" onClick={() => setModalOpen(false)}>
                  Cancel
                </Button>
                <Button variant="primary" onClick={() => setModalOpen(false)}>
                  Confirm
                </Button>
              </ModalActions>
            </Modal>
          </div>
        )}

        {/* Loading */}
        {activeSection === 'loading' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Loading</h3>
            <div className="catalog-demo-group">
              <h4>Spinner Sizes</h4>
              <div className="catalog-demo-row">
                <Spinner size="small" />
                <Spinner size="medium" />
                <Spinner size="large" />
              </div>
            </div>
            <div className="catalog-demo-group">
              <h4>Loading with Text</h4>
              <Loading text="Loading data..." />
            </div>
          </div>
        )}

        {/* Progress */}
        {activeSection === 'progress' && (
          <div className="catalog-section">
            <h3 className="catalog-section-title">Progress</h3>
            <div className="catalog-demo-group">
              <h4>Circular Progress</h4>
              <div className="catalog-demo-row">
                <CircularProgress value={25} max={100} showPercentage />
                <CircularProgress value={50} max={100} showPercentage />
                <CircularProgress value={75} max={100} showPercentage />
                <CircularProgress value={100} max={100} showPercentage />
              </div>
            </div>
            <div className="catalog-demo-group">
              <h4>With Value/Max Display</h4>
              <CircularProgress value={1245} max={5000} size={120} strokeWidth={4} />
            </div>
          </div>
        )}
      </div>
    </div>
  );
};
