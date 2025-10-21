# BlueSnake Design System

A comprehensive design system for the BlueSnake desktop application providing reusable, type-safe UI components.

## Quick Start

```tsx
import { Button, Dropdown, DropdownMenu, Modal, Icon } from './design-system';

// Use components
<Button variant="primary" onClick={handleClick}>
  Click me
</Button>
```

## Available Components

### Base Components

#### Button
```tsx
<Button
  variant="primary" | "secondary" | "ghost" | "danger"
  size="small" | "medium" | "large"
  disabled={boolean}
  loading={boolean}
  icon={<Icon name="..." />}
  onClick={() => void}
>
  Button Text
</Button>
```

#### Input
```tsx
<Input
  type="text" | "number" | "email" | "password"
  value={string}
  onChange={(e) => void}
  placeholder="Enter text..."
  disabled={boolean}
  error={string}
  leftIcon={<Icon name="..." />}
  rightIcon={<Icon name="..." />}
/>
```

#### Checkbox
```tsx
<Checkbox
  checked={boolean}
  onChange={(checked) => void}
  label="Check me"
  disabled={boolean}
/>
```

#### Badge
```tsx
<Badge
  variant="success" | "error" | "warning" | "info" | "neutral"
  size="small" | "medium" | "large"
>
  Status
</Badge>
```

#### Icon
```tsx
<Icon
  name="search" | "settings" | "check" | "x" | "chevron-down" | ...
  size={16}
  strokeWidth={1.5}
/>
```

Available icons: search, settings, check, x, chevron-down, chevron-up, grid, loader, external-link, trash, download, upload, filter, plus, minus, info, alert-circle, alert-triangle

### Overlay Components

#### Dropdown (Single Select)
```tsx
<Dropdown
  value={selectedValue}
  options={[
    { value: 1, label: 'Option 1' },
    { value: 2, label: 'Option 2' },
  ]}
  onChange={(value) => void}
  placeholder="Select option"
  disabled={boolean}
/>
```

#### DropdownMenu (Multi-select with Checkboxes)
```tsx
<DropdownMenu
  trigger={<Button size="small">Columns (3)</Button>}
  items={[
    { id: 'url', label: 'URL', checked: true },
    { id: 'status', label: 'Status', checked: false },
  ]}
  onItemToggle={(id) => void}
  position="bottom-right" | "bottom-left" | "top-right" | "top-left"
/>
```

#### Menu (Action Menu)
```tsx
<Menu
  trigger={<Button icon={<Icon name="settings" />} />}
  items={[
    {
      id: 'edit',
      label: 'Edit',
      description: 'Edit this item',
      icon: <Icon name="edit" />,
      onClick: () => void,
    },
    {
      id: 'delete',
      label: 'Delete',
      description: 'Remove this item',
      variant: 'danger',
      onClick: () => void,
    },
  ]}
  position="bottom-right"
/>
```

#### SplitButton (Button + Menu)
```tsx
<SplitButton
  label="New Crawl"
  onClick={handlePrimaryAction}
  icon={<Icon name="arrow-right" />}
  menuItems={[
    {
      id: 'full',
      label: 'Full Website Crawl',
      description: 'Crawl entire website',
      onClick: handleFullCrawl,
    },
    {
      id: 'single',
      label: 'Single Page',
      description: 'Crawl one page only',
      onClick: handleSinglePage,
    },
  ]}
  variant="primary"
  size="medium"
  menuPosition="bottom-right"
/>
```

#### Modal
```tsx
<Modal
  isOpen={boolean}
  onClose={() => void}
  title="Confirm Action"
  variant="default" | "warning" | "danger"
  size="small" | "medium" | "large"
  closeOnOverlayClick={true}
  closeOnEscape={true}
>
  <p>Modal content here</p>
  <div style={{ marginTop: '20px', display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
    <Button variant="secondary" onClick={onClose}>Cancel</Button>
    <Button variant="primary" onClick={onConfirm}>Confirm</Button>
  </div>
</Modal>
```

### Feedback Components

#### Spinner
```tsx
<Spinner size={16} />
```

#### Loading
```tsx
<Loading size="small" | "medium" | "large" />
```

#### CircularProgress
```tsx
<CircularProgress
  value={75}
  max={100}
  size={80}
  strokeWidth={3}
  showLabel={true}
  showPercentage={true}
/>
```

## Design Tokens

### Colors
```typescript
import { colors } from './design-system';

// Neutral colors
colors.neutral[50]    // #fafafa
colors.neutral[100]   // #f5f5f5
colors.neutral[200]   // #e0e0e0
colors.neutral[300]   // #d0d0d0
colors.neutral[400]   // #999999
colors.neutral[500]   // #666666
colors.neutral[600]   // #333333
colors.neutral[800]   // #000000

// Semantic colors
colors.semantic.success   // #4caf50
colors.semantic.error     // #f44336
colors.semantic.warning   // #ff9800
colors.semantic.info      // #2196f3

// HTTP Status colors
colors.status['2xx']      // #4caf50 (green)
colors.status['3xx']      // #2196f3 (blue)
colors.status['4xx']      // #ff9800 (orange)
colors.status['5xx']      // #f44336 (red)
```

### Spacing
```typescript
import { spacing } from './design-system';

spacing.space1    // 4px
spacing.space2    // 8px
spacing.space3    // 12px
spacing.space4    // 16px
spacing.space5    // 20px
spacing.space6    // 24px
spacing.space8    // 32px
spacing.space10   // 40px
spacing.space12   // 48px
spacing.space16   // 64px
spacing.space32   // 128px
```

### Typography
```typescript
import { typography } from './design-system';

// Font sizes
typography.fontSize.xs     // 12px
typography.fontSize.sm     // 13px
typography.fontSize.base   // 14px
typography.fontSize.md     // 15px
typography.fontSize.lg     // 16px
typography.fontSize.xl     // 20px
typography.fontSize['2xl'] // 24px
typography.fontSize['3xl'] // 32px

// Font weights
typography.fontWeight.light      // 300
typography.fontWeight.regular    // 400
typography.fontWeight.medium     // 500
typography.fontWeight.semibold   // 600

// Line heights
typography.lineHeight.tight   // 1.2
typography.lineHeight.normal  // 1.5
typography.lineHeight.relaxed // 1.8

// Letter spacing
typography.letterSpacing.tight  // -0.02em
typography.letterSpacing.normal // 0em
typography.letterSpacing.wide   // 0.02em
```

## Hooks

### useClickOutside
Detect clicks outside an element (for closing dropdowns/modals).

```tsx
import { useClickOutside } from './design-system';

const ref = useRef<HTMLDivElement>(null);
useClickOutside(ref, () => setIsOpen(false), enabled);
```

### useKeyPress
Listen for specific keyboard events.

```tsx
import { useKeyPress } from './design-system';

useKeyPress('Escape', () => setIsOpen(false), enabled);
```

### usePosition
Calculate smart viewport-aware positioning for overlays.

```tsx
import { usePosition } from './design-system';

const { position } = usePosition(
  triggerRef,
  menuRef,
  'bottom-right' // preferred position
);

// Returns coordinates: { top?: number, bottom?: number, left?: number, right?: number }
```

## Utilities

### classNames
Conditionally join class names.

```tsx
import { classNames } from './design-system';

const className = classNames(
  'base-class',
  isActive && 'active',
  isDisabled && 'disabled',
  customClass
);
```

### Formatters
Common formatting utilities.

```tsx
import { formatters } from './design-system';

formatters.formatDate(date);           // "2025-01-21"
formatters.formatTime(date);           // "14:30:45"
formatters.formatDateTime(date);       // "2025-01-21 14:30:45"
formatters.formatNumber(1234567);      // "1,234,567"
formatters.formatBytes(1024);          // "1 KB"
formatters.formatDuration(3661000);    // "1h 1m 1s"
```

## File Structure

```
src/design-system/
├── tokens/
│   ├── colors.ts           # Color palette
│   ├── spacing.ts          # Spacing scale
│   ├── typography.ts       # Font sizes, weights, etc.
│   └── index.ts
├── components/
│   ├── Button/
│   │   ├── Button.tsx
│   │   ├── Button.css
│   │   └── index.ts
│   ├── Input/
│   ├── Checkbox/
│   ├── Badge/
│   ├── Icon/
│   ├── Dropdown/
│   ├── Menu/
│   ├── SplitButton/
│   ├── Modal/
│   ├── Loading/
│   ├── Progress/
│   └── index.ts
├── hooks/
│   ├── useClickOutside.ts
│   ├── useKeyPress.ts
│   ├── usePosition.ts
│   └── index.ts
├── utils/
│   ├── classNames.ts
│   ├── formatters.ts
│   └── index.ts
└── index.ts               # Main export
```

## Usage Tips

1. **Import from root**: Always import from `./design-system`, not from individual files
2. **Use tokens**: Prefer design tokens over hardcoded values
3. **Type safety**: All components have full TypeScript support
4. **Accessibility**: Components include ARIA labels and keyboard navigation
5. **Customization**: Use `style` prop or `className` for one-off customizations
6. **Positioning**: Overlay components auto-adjust position to stay in viewport

## Examples

### Common Patterns

**Search input with icon:**
```tsx
<Input
  type="text"
  placeholder="Search..."
  leftIcon={<Icon name="search" size={16} />}
  value={searchTerm}
  onChange={(e) => setSearchTerm(e.target.value)}
/>
```

**Loading button:**
```tsx
<Button
  variant="primary"
  loading={isSubmitting}
  onClick={handleSubmit}
>
  {isSubmitting ? 'Saving...' : 'Save'}
</Button>
```

**Status badge:**
```tsx
<Badge variant={status === 200 ? 'success' : 'error'}>
  {status}
</Badge>
```

**Confirmation modal:**
```tsx
<Modal
  isOpen={showDeleteModal}
  onClose={() => setShowDeleteModal(false)}
  title="Delete Project"
  variant="danger"
>
  <p>Are you sure you want to delete this project? This action cannot be undone.</p>
  <div style={{ marginTop: '20px', display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
    <Button variant="secondary" onClick={() => setShowDeleteModal(false)}>
      Cancel
    </Button>
    <Button variant="danger" onClick={handleDelete}>
      Delete
    </Button>
  </div>
</Modal>
```
