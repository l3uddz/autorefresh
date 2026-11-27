## ADDED Requirements

### Requirement: Pointer for table rows that open details

The dashboard SHALL show a hand cursor over the part of each Items and Runs table row that opens details. A control within a row SHALL keep its own cursor and action.

#### Scenario: Hover over an item row

- **GIVEN** the Items page is in Table mode
- **WHEN** the mouse hovers over the text, padding, or empty cell space of an item row that opens details
- **THEN** the cursor shows a hand

#### Scenario: Hover over a run row

- **GIVEN** the Runs page shows a table
- **WHEN** the mouse hovers over the text, padding, or empty cell space of a run row that opens details, including the Started cell
- **THEN** the cursor shows a hand

#### Scenario: Use an Items row selection control

- **GIVEN** an Items table row has a selection checkbox
- **WHEN** the user hovers over and clicks the checkbox
- **THEN** the checkbox keeps its usual cursor and toggles selection without opening item details
