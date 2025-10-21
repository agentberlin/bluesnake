# Design System Migration Guide

## Overview

This guide tracks the migration of BlueSnake's frontend components to the new design system. We're migrating **one component at a time** with verification after each step.

## Migration Strategy

✅ **IMPORTANT**: Migrate one component at a time, verify it works, then move to the next.

### Why One-by-One?

1. **Easier to debug** - If something breaks, you know exactly which change caused it
2. **Safer rollback** - Can revert a single component without losing all progress
3. **Better verification** - Test each component thoroughly before moving on
4. **Learn patterns** - Understand how to use the design system progressively

## Migration Status

### ✅ Completed Migrations

#### 1. ColumnSelector → DropdownMenu ✅ VERIFIED
- **File**: `src/App.tsx` (lines 91-127)
- **Date**: 2025-10-21
- **Status**: Complete and verified

**What was changed:**
- Replaced custom `ColumnSelector` with `<DropdownMenu>` from design system
- Reduced code from 87 lines to 30 lines (65% reduction)
- Added wrapper div with `marginLeft: 'auto'` for right alignment
- Used `size="small"` button with custom font size (12px)
- Icon size reduced to 14px
- Checkbox color set to black (#333)

**Lessons learned:**
- Design system components may need wrapper divs for layout positioning
- Can override button styles with inline `style` prop for specific sizing
- Checkbox color needed to be changed from default blue to black
- Font sizes and padding needed adjustment to match original design

---

#### 2. CustomDropdown → Dropdown ✅ COMPLETE - NEEDS VERIFICATION
- **File**: `src/App.tsx` (lines 36-51)
- **Date**: 2025-10-21
- **Status**: Code migrated, awaiting testing

**What was changed:**
- Replaced custom `CustomDropdown` with `<Dropdown>` from design system
- Reduced code from 48 lines to 15 lines (69% reduction)
- Removed manual state management (isOpen, dropdownRef)
- Removed click-outside useEffect hook
- Transformed CrawlInfo[] → DropdownOption[] format
- Kept wrapper component to maintain same API for consumers

**Code transformation:**
```tsx
// Transform data for design system
const dropdownOptions = options.map(crawl => ({
  value: crawl.id,
  label: formatOption(crawl)
}));

// Use design system Dropdown
<Dropdown
  value={value}
  options={dropdownOptions}
  onChange={onChange}
  disabled={disabled}
  placeholder="Select crawl"
/>
```

**Testing needed:**
- [ ] Dropdown displays currently selected crawl
- [ ] Clicking opens menu with all crawls
- [ ] Selecting a crawl updates selection
- [ ] Click outside closes dropdown
- [ ] Escape key closes dropdown
- [ ] Date/time formatting displays correctly

**Lessons learned:**
- Facade pattern works well - keep wrapper component, change internals
- Data transformation is simple with map()
- formatOption function still works with new structure

---

#### 3. CircularProgress → CircularProgress (Design System) ⚠️ COMPLETE - HAS ISSUES
- **File**: `src/App.tsx` (lines 189-205)
- **Date**: 2025-10-21
- **Status**: Code migrated, awaiting testing

**What was changed:**
- Replaced custom `CircularProgress` with design system `<CircularProgress>`
- Renamed wrapper component to `CrawlProgress` to avoid naming conflict
- Reduced code from 39 lines to 13 lines (67% reduction)
- Removed manual SVG rendering and percentage calculations
- Updated 2 usage locations (project stats and footer status)

**Code transformation:**
```tsx
// Before: Manual SVG rendering
function CircularProgress({ crawled, total }) {
  const percentage = total > 0 ? (crawled / total) * 100 : 0;
  const radius = 8;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (percentage / 100) * circumference;

  return (
    <div className="circular-progress">
      <svg width="20" height="20" viewBox="0 0 20 20">
        {/* Manual SVG circles */}
      </svg>
      <span>{crawled} / {total}</span>
    </div>
  );
}

// After: Design system component
function CrawlProgress({ crawled, total }) {
  return (
    <CircularProgress
      value={crawled}
      max={total}
      size={20}
      strokeWidth={2}
      showLabel={true}
      showPercentage={false}
    />
  );
}
```

**Testing result:**
- ⚠️ **ISSUE FOUND**: Text display is broken - shows "122 / 122rawling..." with weird formatting
- ⚠️ The label positioning/styling doesn't match the original
- ⚠️ Font size and layout need significant adjustments

**Status**: Needs rework - will address later. The design system CircularProgress component needs:
- Fix label positioning (text appears outside/overlapping)
- Match the original compact style (text should fit inside the small 20px circle area)
- May need to create a specialized small variant

**Lessons learned:**
- Design system components may not work for all sizes without adjustment
- Small components (20px) need special handling for text layout
- Should test visual appearance before marking complete

---

#### 4. Remove "New Crawl" Dropdown ✅ COMPLETE
- **File**: `src/App.tsx` (line 1547)
- **Date**: 2025-10-21
- **Status**: Complete

**What was changed:**
- Removed dropdown from "New Crawl" button in dashboard header
- Now just a simple button that triggers full website crawl
- Reduced complexity and simplified UX

**Code change:**
```tsx
// Before: Split button with dropdown
<div className="header-split-button-container">
  <button className="new-crawl-button" onClick={handleNewCrawl}>New Crawl</button>
  <button className="new-crawl-button-dropdown" onClick={...}>▼</button>
  {isCrawlTypeDropdownOpen && <div className="crawl-type-dropdown">...</div>}
</div>

// After: Simple button
<button className="new-crawl-button" onClick={handleNewCrawl}>New Crawl</button>
```

**Lessons learned:**
- Not everything needs a dropdown - simple is often better
- User feedback can simplify UX

---

#### 5. SplitButton + Menu Components ✅ VERIFIED
- **Files**: Created new design system components
- **Date**: 2025-10-21
- **Status**: Complete and verified
- **Complexity**: Medium-High

**Problem Analysis:**
The home page has a split button pattern that doesn't fit existing components:
- Main button: Triggers primary action (Full Website Crawl)
- Dropdown button: Shows menu with 3 options (Full, Single Page, Configure)
- Each option has: Title + Description (rich content)

**Why existing components don't work:**
- `DropdownMenu` = Multi-select with checkboxes (wrong pattern)
- `Dropdown` = Single-select simple options (no rich content)
- Need: Single-select actions with title + description

**Proposed Solution:**
Create two new components:

1. **`Menu` Component** - Flexible action menu
   ```tsx
   <Menu
     trigger={<Button>Options</Button>}
     items={[
       {
         id: 'full',
         title: 'Full Website Crawl',
         description: 'Discover and crawl pages...',
         onClick: handleFullCrawl
       },
       {
         id: 'single',
         title: 'Single Page Crawl',
         description: 'Analyze only this URL...',
         onClick: handleSinglePage
       }
     ]}
   />
   ```

2. **`SplitButton` Component** - Button + Menu combo
   ```tsx
   <SplitButton
     onPrimaryAction={handleNewCrawl}
     primaryIcon={<Icon name="arrow-right" />}
     options={[/* same as Menu items */]}
   />
   ```

**Design decisions:**
- Menu = Generic action menu (reusable for other dropdowns)
- SplitButton = Specialized for split button pattern
- Both support rich content (title + description)
- Clear distinction from DropdownMenu (checkboxes) and Dropdown (simple select)

**Current status:**
- [x] Design Menu component API
- [x] Implement Menu component
- [x] Design SplitButton component API
- [x] Implement SplitButton component
- [x] Migrate home page split button
- [x] Test and verify - ✅ VERIFIED WORKING

**What was built:**

1. **Menu Component** (`src/design-system/components/Menu/`)
   - Flexible action menu with rich content support
   - Supports icons, titles, descriptions
   - Danger variant for destructive actions
   - Disabled state support
   - Auto-closes on selection
   - Uses usePosition, useClickOutside, useKeyPress hooks

2. **SplitButton Component** (`src/design-system/components/SplitButton/`)
   - Combines primary button + menu dropdown
   - Supports all button variants (primary, secondary, danger, ghost)
   - Visual separator between main and dropdown buttons
   - Passes through all button props (size, loading, disabled, icon)

**Migration changes:**

Before (48 lines):
```tsx
// State and refs
const [isCrawlTypeDropdownOpen, setIsCrawlTypeDropdownOpen] = useState(false);
const crawlTypeDropdownRef = useRef<HTMLDivElement>(null);

// Effect for click outside
useEffect(() => {
  const handleClickOutside = (event: MouseEvent) => {
    if (crawlTypeDropdownRef.current && !crawlTypeDropdownRef.current.contains(event.target as Node)) {
      setIsCrawlTypeDropdownOpen(false);
    }
  };
  if (isCrawlTypeDropdownOpen) {
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }
}, [isCrawlTypeDropdownOpen]);

// JSX - split button with manual dropdown
<div className="split-button-container" ref={crawlTypeDropdownRef}>
  <button className="go-button" onClick={handleNewCrawl}>
    {/* SVG icon */}
  </button>
  <button className="go-button-dropdown" onClick={() => setIsCrawlTypeDropdownOpen(!isCrawlTypeDropdownOpen)}>
    {/* Chevron icon */}
  </button>
  {isCrawlTypeDropdownOpen && (
    <div className="crawl-type-dropdown">
      <div className="crawl-type-option" onClick={handleNewCrawl}>
        <div className="crawl-type-option-content">
          <span className="crawl-type-option-title">Full Website Crawl</span>
          <span className="crawl-type-option-desc">Discover and crawl pages...</span>
        </div>
      </div>
      {/* 2 more options */}
    </div>
  )}
</div>
```

After (30 lines):
```tsx
<SplitButton
  label=""
  onClick={handleNewCrawl}
  disabled={!url.trim()}
  icon={<Icon name="arrow-right" />}
  menuItems={[
    {
      id: 'full-crawl',
      label: 'Full Website Crawl',
      description: 'Discover and crawl pages by following links and sitemaps',
      onClick: handleNewCrawl,
    },
    {
      id: 'single-page',
      label: 'Single Page Crawl',
      description: 'Analyze only this specific URL without following any links',
      onClick: handleSinglePageCrawl,
    },
    {
      id: 'configure',
      label: 'Configure And Crawl',
      description: 'Customize settings before starting your crawl',
      onClick: handleOpenConfigFromHome,
    },
  ]}
  variant="primary"
  size="medium"
  menuPosition="bottom-left"
/>
```

**Cleanup:**
- Removed `isCrawlTypeDropdownOpen` state
- Removed `crawlTypeDropdownRef` ref
- Removed click-outside effect handler
- Removed dropdown close calls from handlers (handleSinglePageCrawl, handleOpenConfigFromHome, handleNewCrawl)
- Menu component handles all state, positioning, and interactions internally

**Files created:**
- `src/design-system/components/Menu/Menu.tsx`
- `src/design-system/components/Menu/Menu.css`
- `src/design-system/components/Menu/index.ts`
- `src/design-system/components/SplitButton/SplitButton.tsx`
- `src/design-system/components/SplitButton/SplitButton.css`
- `src/design-system/components/SplitButton/index.ts`

**Files modified:**
- `src/design-system/components/index.ts` - Added exports
- `src/App.tsx` - Migrated split button (lines 1220-1253)

**Code reduction:** ~48 lines → ~30 lines (37% reduction)

**Lessons learned:**
- Menu and SplitButton are distinct patterns from DropdownMenu/Dropdown
- Rich content (title + description) requires dedicated component
- SplitButton is reusable pattern for primary action + alternatives
- State management, positioning, and interactions handled by design system
- Menu uses `position: fixed` with viewport coordinates (not `position: absolute`)
- SplitButton container needs `align-items: stretch` and `overflow: visible`
- Menu positioning defaults to `bottom-left`, use `bottom-right` for right-aligned menus

**Issues fixed during testing:**
1. **Button height mismatch**: Added `align-items: stretch` to split button container
2. **Dropdown not appearing**: Changed Menu from `position: absolute` to `position: fixed`
3. **Menu positioning**: Changed from `bottom-left` to `bottom-right` for proper alignment

---

### 🔄 Pending Migrations

#### 6. SmallLoadingSpinner → Spinner Component
- **File**: `src/App.tsx` (lines 28-84)
- **Current**: Custom dropdown with manual state management
- **Target**: `<Dropdown>` component from design system
- **Complexity**: Medium
- **Estimated reduction**: ~50 lines

**Current implementation:**
```tsx
function CustomDropdown({ value, options, onChange, disabled, formatOption }: CustomDropdownProps) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  // Manual click-outside handling
  useEffect(() => { /* ... */ }, []);

  // Manual rendering logic
  return (/* 50+ lines of JSX */);
}
```

**Target implementation:**
```tsx
<Dropdown
  value={selectedCrawlId}
  options={crawls.map(c => ({
    value: c.id,
    label: formatCrawlDate(c.startedAt)
  }))}
  onChange={setCrawlId}
  disabled={isLoading}
/>
```

**Considerations:**
- Need to transform `CrawlInfo[]` to `DropdownOption[]` format
- Custom rendering via `formatOption` prop → use `renderOption` prop in design system
- May need size/font adjustments like ColumnSelector

---

#### 3. CircularProgress → CircularProgress (Design System)
- **File**: `src/App.tsx` (lines 254-288)
- **Current**: Custom CircularProgress component
- **Target**: `<CircularProgress>` from design system
- **Complexity**: Low
- **Note**: Both have same name, just import from design system instead

**Current implementation:**
```tsx
function CircularProgress({ percentage, isLoading }: CircularProgressProps) {
  // Custom SVG rendering
  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  // ... calculation logic
  return (/* SVG with custom styling */);
}
```

**Target implementation:**
```tsx
import { CircularProgress } from './design-system';

<CircularProgress
  value={percentage}
  max={100}
  showPercentage
  size={80}
  strokeWidth={3}
/>
```

**Considerations:**
- Design system uses `value/max` instead of `percentage`
- May need to adjust size and strokeWidth to match current design
- Loading state handled differently - may need conditional rendering

---

#### 4. SmallLoadingSpinner → Spinner Component
- **File**: `src/App.tsx` (lines 290-305)
- **Current**: Custom spinner component
- **Target**: `<Spinner>` from design system
- **Complexity**: Very Low

**Current implementation:**
```tsx
function SmallLoadingSpinner() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" className="small-loading-spinner">
      <circle className="small-loading-spinner-circle" cx="8" cy="8" r="6" strokeWidth="2" fill="none" />
    </svg>
  );
}
```

**Target implementation:**
```tsx
import { Spinner } from './design-system';

<Spinner size="small" />
```

**Considerations:**
- Direct 1:1 replacement
- Size might need adjustment (design system has small/medium/large)

---

#### 5. FaviconImage Component
- **File**: `src/App.tsx` (lines 314-352)
- **Current**: Custom favicon loader with fallback
- **Target**: Extract to design system as feature component
- **Complexity**: Medium
- **Action**: Move to `design-system/components/FaviconImage/`

**Current implementation:**
```tsx
function FaviconImage({ domain, size = 16 }: FaviconImageProps) {
  const [faviconUrl, setFaviconUrl] = useState<string | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    GetFaviconData(domain).then(/* ... */);
  }, [domain]);

  // Fallback logic
  return (/* img or fallback SVG */);
}
```

**Target location:**
- Move to: `src/design-system/components/FaviconImage/FaviconImage.tsx`
- Keep same implementation, just relocate
- Update imports across the app

**Considerations:**
- This is a feature component, not a base UI component
- Should live in design system but in a separate category
- Update all imports: `import { FaviconImage } from './design-system';`

---

#### 6. Modal Components → Modal (Design System)
- **Files**: `src/App.tsx` (multiple locations)
- **Current**: Custom delete confirmation modals, version update modals
- **Target**: `<Modal>`, `<ModalContent>`, `<ModalActions>` from design system
- **Complexity**: Medium

**Current implementation examples:**
```tsx
// Delete project modal (lines ~800-850)
{showDeleteProjectModal && (
  <div className="modal-overlay" onClick={handleCloseDeleteModal}>
    <div className="modal-container delete-modal">
      <h3>Delete Project</h3>
      <p>Are you sure...?</p>
      <div className="modal-actions">
        <button onClick={handleCloseDeleteModal}>Cancel</button>
        <button onClick={confirmDeleteProject}>Delete</button>
      </div>
    </div>
  </div>
)}

// Version warning modal (lines ~900-950)
{showVersionWarning && (/* similar structure */)}

// Version blocking modal (lines ~950-1000)
{showVersionBlock && (/* similar structure */)}
```

**Target implementation:**
```tsx
import { Modal, ModalContent, ModalActions, Button } from './design-system';

<Modal
  isOpen={showDeleteProjectModal}
  onClose={handleCloseDeleteModal}
  title="Delete Project"
  variant="danger"
  size="medium"
>
  <ModalContent>
    <p>Are you sure you want to delete this project? This action cannot be undone.</p>
  </ModalContent>
  <ModalActions>
    <Button variant="secondary" onClick={handleCloseDeleteModal}>
      Cancel
    </Button>
    <Button variant="danger" onClick={confirmDeleteProject}>
      Delete
    </Button>
  </ModalActions>
</Modal>
```

**Modals to migrate:**
1. Delete project modal
2. Delete crawl modal
3. Version warning modal
4. Version blocking modal

**Considerations:**
- Each modal should use appropriate variant (danger for delete, warning for updates)
- Replace custom buttons with design system `<Button>` components
- Remove custom modal CSS classes
- Update click-outside and escape key handling (built into Modal)

---

#### 7. Utility Functions → Design System Utilities
- **File**: `src/App.tsx` (inline functions throughout)
- **Current**: Various utility functions scattered in component
- **Target**: Import from `design-system/utils`
- **Complexity**: Low

**Functions to replace:**

1. **Date/Time Formatting**
   ```tsx
   // Current (inline)
   const formatDate = (timestamp: number) => {
     const date = new Date(timestamp * 1000);
     return date.toLocaleDateString(/* ... */);
   };

   // Target
   import { formatDate, formatDateTime } from './design-system';
   ```

2. **Duration Formatting**
   ```tsx
   // Current (inline)
   const formatDuration = (ms: number) => {
     const seconds = Math.floor(ms / 1000);
     // ... logic
   };

   // Target
   import { formatDuration } from './design-system';
   ```

3. **Content Type Categorization**
   ```tsx
   // Target
   import { categorizeContentType, getContentTypeDisplay } from './design-system';
   ```

**Considerations:**
- Find and replace all inline implementations
- Remove local function definitions
- Test that behavior remains the same

---

#### 8. Button Elements → Button Component
- **Files**: Various throughout `src/App.tsx`
- **Current**: Native `<button>` elements with custom classes
- **Target**: `<Button>` component
- **Complexity**: Low-Medium

**Examples to replace:**

```tsx
// Current
<button className="go-button" onClick={handleStartCrawl} disabled={isLoading}>
  Start Crawl
</button>

// Target
<Button variant="primary" onClick={handleStartCrawl} disabled={isLoading}>
  Start Crawl
</Button>
```

**Button types found:**
- Primary action buttons (go-button, start-button)
- Secondary buttons (config-button, cancel)
- Danger buttons (delete actions)
- Icon buttons

**Considerations:**
- Map custom classes to button variants (primary, secondary, ghost, danger)
- Some buttons may need size adjustments
- Icon buttons may need `icon` prop

---

## Migration Checklist Template

For each migration, follow this checklist:

### Before Starting
- [ ] Read the component's current implementation
- [ ] Identify dependencies and usage locations
- [ ] Review design system component API
- [ ] Plan the replacement strategy

### During Migration
- [ ] Create new implementation using design system
- [ ] Add design system imports
- [ ] Remove old component code
- [ ] Adjust styling if needed (size, spacing, colors)
- [ ] Update any dependent code

### After Migration
- [ ] Run `npm run build` - ensure TypeScript compiles
- [ ] Run the app and test the migrated component
- [ ] Verify all functionality works (clicks, keyboard, edge cases)
- [ ] Test responsiveness and edge cases
- [ ] Document any issues or adjustments made
- [ ] Create migration notes document (like `MIGRATION_01_*.md`)
- [ ] Commit changes with clear message

### Verification Testing
- [ ] Visual appearance matches original (or is better)
- [ ] All interactions work (click, hover, keyboard)
- [ ] Click-outside behavior works (for dropdowns/modals)
- [ ] Escape key closes overlays
- [ ] Loading states display correctly
- [ ] Error states display correctly
- [ ] Disabled states work correctly
- [ ] Responsive behavior is correct
- [ ] Accessibility (keyboard navigation, screen readers)

---

## Known Issues & Solutions

### Issue 1: Component Size Too Large
**Problem**: Design system components are larger than originals
**Solution**:
- Use `size="small"` prop
- Override with inline `style={{ fontSize: '12px', padding: '8px 12px' }}`
- Adjust icon sizes (use 14px instead of 16px)

### Issue 2: Wrong Positioning
**Problem**: Component not aligned correctly
**Solution**: Wrap in a div with appropriate layout styles
```tsx
<div style={{ marginLeft: 'auto', paddingLeft: '16px' }}>
  <DesignSystemComponent />
</div>
```

### Issue 3: Wrong Colors
**Problem**: Colors don't match original design
**Solution**:
- Check original CSS for specific colors
- Update design system CSS if needed (e.g., checkbox color)
- May need to adjust both component and design system

### Issue 4: TypeScript Errors
**Problem**: Type mismatches when using design system
**Solution**:
- Use `!!` to convert to boolean: `!!leftIcon && 'class-name'`
- Add proper type definitions
- Check for missing required props

---

## CSS Cleanup

After migrating a component, you can **optionally** remove the old CSS classes (but wait until you've verified everything works):

### After ColumnSelector Migration
Can remove from `App.css`:
- `.column-selector`
- `.column-selector-button`
- `.column-selector-text`
- `.column-selector-arrow`
- `.column-selector-menu`
- `.column-selector-item`
- `.column-selector-checkbox`
- `.column-selector-label`

**Note**: Don't rush to remove CSS. Keep it until you're 100% confident the migration is stable.

---

## Rollback Instructions

If a migration goes wrong:

### Single Component Rollback
```bash
# Revert just the App.tsx file
git checkout HEAD -- src/App.tsx

# Or revert the entire design system
git checkout HEAD -- src/design-system/
```

### Full Rollback
```bash
# See what changed
git status
git diff

# Revert all changes
git checkout -- .
```

---

## Progress Tracking

Keep track of your progress:

- [x] **Migration 1**: ColumnSelector → DropdownMenu ✅
- [x] **Migration 2**: CustomDropdown → Dropdown ✅
- [x] **Migration 3**: CircularProgress → CircularProgress (DS) ⚠️ (needs rework)
- [x] **Migration 4**: Remove "New Crawl" Dropdown ✅
- [ ] **Migration 5**: SplitButton + Menu components 🔄 (in progress)
- [ ] **Migration 6**: SmallLoadingSpinner → Spinner
- [ ] **Migration 7**: FaviconImage → Extract to DS
- [ ] **Migration 8**: Modals → Modal component
- [ ] **Migration 9**: Utilities → Import from DS
- [ ] **Migration 10**: Buttons → Button component
- [ ] **Migration 11**: Fix CircularProgress issues ⚠️
- [ ] **Migration 12**: Final cleanup & CSS removal
- [ ] **Migration 13**: Full app verification

---

## Next Session: How to Continue

When you return to this work:

1. **Review Status**
   - Read this guide
   - Check `MIGRATION_01_COLUMN_SELECTOR.md` to see what was done
   - Run the app to verify current state

2. **Pick Next Migration**
   - Start with **Migration 2: CustomDropdown**
   - It's the next logical step (another dropdown component)

3. **Follow the Process**
   - Read the "Current implementation" section above
   - Read the "Target implementation" section
   - Follow the "Migration Checklist Template"
   - Test thoroughly
   - Document your changes

4. **Create Migration Doc**
   - Copy the format from `MIGRATION_01_COLUMN_SELECTOR.md`
   - Create `MIGRATION_02_CUSTOM_DROPDOWN.md`
   - Document before/after code, issues, solutions

5. **Verify & Commit**
   - Test the component
   - Run `npm run build`
   - Commit with clear message: "Migrate CustomDropdown to design system Dropdown component"
   - Move to next migration

---

## Resources

- **Design System Docs**: `src/design-system/README.md`
- **Component Catalog**: Import and render `<ComponentCatalog />` to browse all components
- **Architecture**: `DESIGN_SYSTEM.md`
- **Completed Migration**: `MIGRATION_01_COLUMN_SELECTOR.md`

---

## Tips for Success

1. **Don't rush** - Take time to understand each component
2. **Test thoroughly** - Click everything, test edge cases
3. **Document issues** - Write down problems and solutions
4. **Keep old CSS** - Don't delete until you're certain
5. **Commit often** - One migration = one commit
6. **Ask questions** - Check design system docs if unsure
7. **Use the catalog** - Browse components visually
8. **Compare carefully** - Use browser dev tools to compare before/after

---

## Success Metrics

You'll know the migration is successful when:

✅ Build compiles without errors
✅ All components look the same (or better)
✅ All interactions work correctly
✅ No console errors
✅ Keyboard navigation works
✅ Code is shorter and cleaner
✅ You understand the design system better

---

## Questions?

If you get stuck:
1. Check the design system README
2. Look at `ComponentCatalog.tsx` for usage examples
3. Review `MIGRATION_01_COLUMN_SELECTOR.md` for patterns
4. Check the component's TypeScript types for available props
5. Use browser dev tools to inspect and compare

Good luck! 🚀
