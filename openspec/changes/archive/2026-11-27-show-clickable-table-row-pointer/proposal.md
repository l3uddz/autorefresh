# Proposal

## Why

Clicking an Items or Runs table row opens its details, but the standard arrow gives little indication that the row is clickable. Issue [#155](https://github.com/l3uddz/autorefresh/issues/155) tracks the missing cursor cue.

## What Changes

- Show the hand cursor over the parts of Items and Runs table rows that open details, including text and empty space within a cell.
- Keep the existing mouse and keyboard activation and let nested controls handle their own interactions.

## Capabilities

### Modified Capabilities

- `dashboard-ui` gains a cursor requirement for table rows that open item or run details.

## Impact

The change affects the Items page in Table mode and Runs on wide screens. List navigation, drawer design, API routes, configuration and service behavior are outside the scope.
