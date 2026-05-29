# Orchestration Upload List And Archives

## Goals

- Keep the orchestration file picker usable when many files are attached.
- Make the attached-file area independently scrollable so it cannot overlap the
  task controls or run button.
- Explicitly support archive uploads such as `.zip`, `.tar`, `.tar.gz`, `.tgz`,
  `.gz`, `.bz2`, `.xz`, and `.7z`.

## Non-Goals

- Server-side archive extraction.
- Changing the existing 12-file count limit or per-file byte limit.
- Changing how uploaded files are persisted; Bridge still writes each uploaded
  blob into the run upload directory and passes local paths to the CLIs.

## Data And Protocol Impact

No wire-protocol change. The frontend continues sending
`protocol.AttachmentPayload` fields through the existing `files` array. Archive
support is explicit at the browser picker/MIME-label layer; Hub and Bridge
already accept binary base64 payloads.

## Implementation Steps

1. Add archive extensions and common archive MIME types to the orchestration
   file input `accept` list.
2. Normalize missing or generic browser MIME values for known archive
   extensions so the UI and run metadata identify them as archives.
3. Render pending files in a bounded, scrollable list with stable row sizing and
   non-overlapping name, size, and remove controls.
4. Add a Bridge regression test that writes an archive attachment as a local
   upload file.

## Exit Gates

- [x] Multiple pending files can be scrolled inside the file picker area.
- [x] File names truncate without overlapping size labels or remove buttons.
- [x] Archive uploads are accepted by the frontend picker and survive Bridge
      preparation as local files.
- [x] `npm run build` refreshes `internal/web/static/`.
