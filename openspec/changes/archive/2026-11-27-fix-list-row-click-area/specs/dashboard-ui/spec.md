# Spec Delta

## ADDED Requirements

### Requirement: Full-row list link click area

Every linked list row on the Status, Items, Runs, and Run details views SHALL open its target when clicked anywhere on the non-interactive row surface, including the poster, empty row space, padding, and trailing metadata. Each row SHALL keep native link behaviour, open in a new tab on Ctrl-click, Meta-click, Shift-click, or middle-click anywhere on the row surface, leave nested interactive controls untouched, and do nothing when a click ends a text selection inside the row.

#### Scenario: Click empty space on a list row

- **WHEN** the user clicks the empty area beside or below the text of a linked list row on desktop
- **THEN** the dashboard opens that item or run just as clicking its title text does

#### Scenario: Click the poster or trailing metadata

- **WHEN** the user clicks the poster or the trailing tries, status, or time metadata of a linked list row
- **THEN** the dashboard opens that item or run

#### Scenario: Open in a new tab from the row surface

- **WHEN** the user Ctrl-clicks, Meta-clicks, Shift-clicks, or middle-clicks the empty area, poster, or trailing metadata of a linked list row
- **THEN** the target opens in a new tab without navigating the current tab

#### Scenario: Drag to select text in a row

- **WHEN** the user drags to highlight text inside a linked list row and releases the mouse button on the row surface
- **THEN** the dashboard keeps the text selection and does not navigate

#### Scenario: Keyboard focus and direct text click

- **WHEN** the user tabs to a linked list row and presses Enter, or clicks its title text directly
- **THEN** the native link opens the target once with no duplicate navigation

#### Scenario: Nested interactive control

- **WHEN** a linked list row contains a nested interactive control and the user clicks that control
- **THEN** the control handles the click and the row surface does not navigate

## MODIFIED Requirements

### Requirement: Action toasts

Every action result SHALL show as a toast with an icon of its outcome: a check for a success, an information icon for a notice, and an exclamation icon with error colours for an error. Every action toast, including an error toast, SHALL auto-close after 5 seconds unless a different duration or manual dismissal is requested, pausing while hovered or focused. A failed item action SHALL explain common failures in plain words.

#### Scenario: Item busy

- **WHEN** an item action fails because another request holds the item
- **THEN** the toast says "This item is busy with another request. Try again in a moment."

#### Scenario: Plex timeout

- **WHEN** Plex does not answer in time
- **THEN** the toast says "Plex did not answer in time. The request may still have reached Plex."

#### Scenario: Item gone from Plex

- **WHEN** an item action fails with `404` `not found in Plex`
- **THEN** the toast says "Plex no longer has this item. It was deleted or moved."

#### Scenario: Error toast auto-closes

- **WHEN** an action fails and shows an error toast
- **THEN** the toast closes automatically after 5 seconds without manual dismissal, unless the user hovers or focuses it, a different duration is given, or manual dismissal is requested

#### Scenario: Bulk refresh error report auto-closes after 10 seconds

- **WHEN** a bulk refresh queue ends with an error outcome and shows its 10-second end report toast
- **THEN** the error toast closes automatically after 10 seconds without manual dismissal, unless hovered or focused
