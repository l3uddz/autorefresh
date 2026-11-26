# Stage 4 dashboard frontend

Issue #100 delivers the frontend foundation from the approved maintenance dashboard design.
The branch starts at main after PR #99 merged.

1. Scaffold `web/` with Vite, React, TypeScript, Vitest, and the pinned Astryx
   CLI, core, and Neutral theme. Use relative Vite URLs and adapt the Dashboard
   and Table Inbox templates. Verify that the approved theme tokens are valid.
2. Embed the Vite output under the `webui` build tag. Serve the existing
   fallback in plain Go builds. Use the same frontend build in mise and
   GoReleaser. Verify gzip negotiation, asset paths, and the tagged binary.
3. Add typed queries, cursor paging, and live status updates. Build Status
   with server progress, run controls, recent fixes, and recent runs.
4. Build Items with the three tabs, list and table layouts, filters, selection,
   and the shared item detail. Wire item actions and bulk work to the write API.
   Keep the fix match dialog, full timeline, Runs page, and phone polish in Stage 5.
5. Run frontend lint and tests, Go race tests and lint, tagged packaging checks,
   and browser smoke tests on a sub-path. Review with Opus and Gemini before
   opening the PR for CodeRabbit.
