# UI-state wire bundle measurement

The v1 client uses Effect Schema for strict boundary decoding and keeps its
connection lifecycle in plain TypeScript. It does not instantiate a
`ManagedRuntime` or use Effect `Schedule`, `Scope`, or `Stream` for that
lifecycle.

Measured on 2026-07-26 by building a clean `HEAD` archive and this worktree with
the same installed dependency/toolchain, then summing Node `zlib.gzipSync`
sizes for the emitted JavaScript:

| Output | Clean `HEAD` | UI-state wire | Delta |
| --- | ---: | ---: | ---: |
| Main JavaScript chunk | 359,661 B | 421,754 B | +62,093 B (60.6 KiB) |
| All JavaScript chunks | 1,229,603 B | 1,291,627 B | +62,024 B (60.6 KiB) |

The comparison used `npm run build` in each tree. The baseline tree shared the
same `node_modules` directory so only imported application code affected the
production output.
