# File Text Extraction Service ✅

A lightweight service that extracts text from files (PDFs and images). PDFs use a hybrid approach: use the text layer where possible and fall back to OCR (Mistral) when needed; images use OCR only. The project exposes public endpoints (via the Cloudflare Worker):

- POST /api/pdf/preview — quick preview and OCR-needed hint
- POST /api/pdf/extract — full hybrid extraction (per-page results + combined text)
- POST /api/image/extract — OCR for image URLs (Mistral only)
- POST /api/file/presign — returns a short-lived presigned URL for an R2 key

---

## 🚀 Quick summary

- Public entrypoint: the Worker (Cloudflare) proxies requests to a local container running the Go server.
- The Worker performs rate-limiting and health checks; the Go server performs downloads, PDF text extraction (poppler pdftotext), quality scoring, and OCR via Mistral for PDFs and images.
- The main behaviour is implemented in `internal/hybrid` and types are defined in `internal/types`.

---

## 📦 Running locally

Prerequisites:
- Go 1.25.6+
- Poppler utilities (`pdfinfo`, `pdftotext`) for text-layer extraction
- Node.js (for Worker / wrangler) and `wrangler` for Cloudflare Worker local dev

Options to run:
- Worker (recommended for local run that mirrors production):

```bash
# from repo root
npx wrangler dev
```

- Run the Go container directly (requires building / container infra used by Worker):

```bash
go run ./cmd/server
```

> Note: the server requires `INTERNAL_SHARED_SECRET` to be set (>=32 chars). If `MISTRAL_API_KEY` is not set OCR will fail.

---

## 🔌 API — request shape

The PDF Worker routes accept either a `presignedUrl` or an R2 `key`:

```json
{
  "presignedUrl": "https://.../file.pdf",
  "options": {
    "minWordsThreshold": 20,
    "pageSeparator": "\n\n---\n\n",
    "includePageNumbers": true,
    "ocrTriggerRatio": 0.25,
    "pages": [1,2,3],

    "extractHeader": false,
    "extractFooter": false,
    "ocrModel": "mistral-ocr-latest",

    "previewMaxPages": 8,
    "previewMaxChars": 20000
  }
}
```

- `presignedUrl` (string, optional): public or presigned URL to download the PDF.
- `key` (string, optional): R2 object key (Worker will generate a short-lived presigned URL).
- `options` (object, optional): extraction knobs. Any missing options are filled by the server defaults (see **Config & Defaults** below).

For image OCR, the request shape is (either `imageUrl`, `presignedUrl`, or `key`):
```json
{
  "imageUrl": "https://.../image.png"
}
```

For presigned URLs, the request shape is:
```json
{
  "key": "user/.../files/<id>",
  "expiresIn": 600
}
```

---

## ✅ API routes & behavior

### Public (Worker)
- POST `/api/pdf/preview`
  - Quick preview of the document (only text-layer extraction is used). Returns `PreviewResult`.
- POST `/api/pdf/extract`
  - Full hybrid extraction. Returns `HybridExtractionResult`.
- POST `/api/image/extract`
  - Image OCR only (Mistral). Returns `ImageExtractionResult`.
- POST `/api/file/presign`
  - Returns a short-lived `presignedUrl` for an R2 key.
  - Allowed key prefixes: `user/` and `tests/`.

The Worker validates the request body size, enforces rate limits, starts/health-checks the extraction container, and proxies the request to the container's internal endpoints.

### Internal (container)
- POST `/pdf/preview` — **internal** endpoint; requires header `X-Internal-Auth: <INTERNAL_SHARED_SECRET>`
- POST `/pdf/extract` — **internal** endpoint; requires same header
- POST `/image/extract` — **internal** endpoint; requires same header

> The Worker sets the internal auth header automatically when proxying; if you call the container directly, include the header.

---

## 📣 Response patterns

All error responses follow the pattern:
```json
{ "success": false, "error": "<message>", "code": "<code>" }
```
Common `code` values: `bad_request`, `rate_limit`, `not_found`, `timeout`, `request_too_large`, `internal_error`, `validation_failed`, `download_failed`, `ocr_capacity`, `unauthorized`, `method_not_allowed`, `capacity`.

### PreviewResponse (PreviewResult)
```json
{
  "success": true,
  "needsOcr": false,
  "text": "...preview text...",
  "wordCount": 123,
  "totalPages": 10,
  "textLayerPages": 8
}
```
- `needsOcr` is a heuristic (true if a sufficient ratio of sampled pages appear to need OCR).
- `text` is truncated to `previewMaxChars` if longer.

### ExtractResponse (HybridExtractionResult)
```json
{
  "success": true,
  "text": "...combined full text...",
  "pages": [
    { "pageNumber": 1, "text": "...", "method": "text-layer", "wordCount": 345 },
    { "pageNumber": 2, "text": "...", "method": "ocr", "wordCount": 120 }
  ],
  "totalPages": 10,
  "textLayerPages": 8,
  "ocrPages": 2,
  "costSavingsPercent": 80
}
```
- `pages` contains per-page results. `method` is one of: `text-layer`, `needs-ocr`, or `ocr`.
- `costSavingsPercent` estimates the percentage of pages served from the cheaper text layer.
- If OCR fails or other errors occur while processing, `error` will be present and `success` may be `false`.

---

## 🛡️ Rate limits & throttling
- The Worker consults the configured rate limiter and returns 429 with `Retry-After: 60` when limits are exceeded.
- The server enforces a per-IP limiter, concurrency limits and OCR capacity gating; when capacity is reached it returns `503` with codes like `capacity` or `ocr_capacity`.

---

## ⚙️ Config & environment variables (important)

Most server config is driven by environment variables (defaults shown):

- `INTERNAL_SHARED_SECRET` (required) — secret used between Worker and container (must be >= 32 chars).
- `MISTRAL_API_KEY` (optional) — required for OCR (Mistral). If empty OCR requests will fail.

Server limits & defaults (selected):
- `PORT` = "8080"
- `MAX_JSON_BODY_BYTES` = 2 MiB
- `MAX_PDF_BYTES` = 200 MiB
- `MAX_CONCURRENT_REQUESTS` = 15
- `MAX_OCR_CONCURRENT` = 3
- `DEFAULT_MIN_WORDS` = 20
- `DEFAULT_OCR_TRIGGER_RATIO` = 0.25
- `DEFAULT_PAGE_SEPARATOR` = "\n\n---\n\n"
- `DEFAULT_OCR_MODEL` = "mistral-ocr-latest"
- `DEFAULT_PREVIEW_PAGES` = 8
- `DEFAULT_PREVIEW_CHARS` = 20000

(See `internal/config/config.go` for the full list and validation rules.)

---

## 🧪 Example cURL requests

Minimal preview request (via Worker):
```bash
curl -X POST "https://your-worker.dev/api/pdf/preview" \
  -H "Content-Type: application/json" \
  -d '{"presignedUrl":"https://.../doc.pdf"}'
```

Minimal extract request (via Worker):
```bash
curl -X POST "https://your-worker.dev/api/pdf/extract" \
  -H "Content-Type: application/json" \
  -d '{"presignedUrl":"https://.../doc.pdf"}'
```

Extract request using an R2 key (via Worker):
```bash
curl -X POST "https://your-worker.dev/api/pdf/extract" \
  -H "Content-Type: application/json" \
  -d '{"key":"user/1c2edf8376defc7ee7653224f5c58dbf/files/12d11a37-8fe7-4266-86f8-6358af0399fb"}'
```

Direct container call (for debugging) — include internal auth header:
```bash
curl -X POST "http://localhost:8080/pdf/extract" \
  -H "Content-Type: application/json" \
  -H "X-Internal-Auth: $INTERNAL_SHARED_SECRET" \
  -d '{"presignedUrl":"https://.../doc.pdf"}'
```

Minimal image OCR request (via Worker):
```bash
curl -X POST "https://your-worker.dev/api/image/extract" \
  -H "Content-Type: application/json" \
  -d '{"imageUrl":"https://.../image.png"}'
```

Image OCR request using an R2 key (via Worker):
```bash
curl -X POST "https://your-worker.dev/api/image/extract" \
  -H "Content-Type: application/json" \
  -d '{"key":"user/1c2edf8376defc7ee7653224f5c58dbf/files/12d11a37-8fe7-4266-86f8-6358af0399fb"}'
```

---

## 🧭 Troubleshooting & notes
- If you see `presignedUrl or key required` — ensure either `presignedUrl` or `key` is present and non-empty.
- `download_failed` often means the remote server returned non-200 or disallowed content-type.
- If OCR is needed but `MISTRAL_API_KEY` is missing, OCR will fail and the server will return `ocr`-related error messages.
- The PDF routes expect PDFs and check `Content-Type` for `pdf` or `octet-stream` when downloading.

---

## 🧩 Developer notes
- Main server: `cmd/server/main.go`
- Hybrid logic: `internal/hybrid/hybrid.go`
- OCR wrapper: `internal/ocr/mistral.go`
- Extraction helpers: `internal/extractor/poppler.go`
- Worker entrypoint: `worker/src/index.ts`

---