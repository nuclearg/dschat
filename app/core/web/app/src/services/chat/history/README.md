# DeepSeek ZIP import

- `readArchive.ts` reads a DeepSeek `deepseek_data-*.zip` (`export_format_version: 3`) into local sessions and image blobs.
- `importDeepSeek.ts` uploads images to chatsvr, maps attachment ids, and calls `/api/sessions/import`.
- `schema.ts` / `toLocal.ts` validate the archive and convert DeepSeek's message tree into the local `Session` shape.
- Import is merge-only. There is no export path.
