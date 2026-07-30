# Redux Toolkit store bundle measurement

Client-persisted state and the ephemeral chrome coupled to it live in Redux
Toolkit slices under `src/store`. The store uses `configureStore`, `createSlice`,
`createSelector`, and `createListenerMiddleware`; it does not use RTK Query.
Server state stays in `src/wire`, so nothing here duplicates that cache.

Measured on 2026-07-30 by building a clean `HEAD` archive and this worktree with
the same installed dependency/toolchain, then summing Node `zlib.gzipSync` sizes
for the emitted JavaScript:

| Output | Clean `HEAD` | Redux Toolkit store | Delta |
| --- | ---: | ---: | ---: |
| Main JavaScript chunk | 426,334 B | 439,359 B | +13,025 B (12.7 KiB) |
| All JavaScript chunks | 1,296,127 B | 1,309,204 B | +13,077 B (12.8 KiB) |

The comparison used `npm run build` in each tree. The baseline tree shared the
same `node_modules` directory, so only imported application code affected the
production output.

The delta covers `@reduxjs/toolkit` (which bundles `redux`, `immer`, `reselect`,
and `redux-thunk`) plus `react-redux`, and is about a fifth of the 60.6 KiB the
Effect Schema decision in `../wire/BUNDLE.md` already costs. Immer's development
invariants and RTK's `serializableCheck`/`immutableCheck` middleware are stripped
from the production bundle by Vite's `process.env.NODE_ENV` replacement, so they
are free to keep enabled in development.
