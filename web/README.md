# Record Hub Web Console

The console is a small Next.js app that talks to the Go BFF through same-origin
`/api` and `/auth` rewrites. The BFF remains the security boundary: the browser never gets
an OIDC client secret or bearer token.

## Local development

Start the Record Hub API with its Web/OIDC settings enabled, then run:

```bash
npm ci
RECORD_HUB_API_ORIGIN=http://127.0.0.1:8080 npm run dev
```

Open <http://localhost:3000>. `RECORD_HUB_API_ORIGIN` defaults to
`http://127.0.0.1:8080`; it is only used by the Next development/server proxy.
Keep the Go BFF and browser on the same origin in production (or configure a
trusted reverse proxy) so the session and CSRF cookies are not split across
origins.

The first screen asks for a tenant ID because the current API intentionally
does not infer tenant scope from a user profile. The value is kept in
`localStorage` for the local browser only.

## Security and consistency notes

- `GET /auth/session` establishes the signed CSRF cookie; all browser writes
  send the readable cookie value as `X-CSRF-Token`.
- The API client adds a fresh `Idempotency-Key` and `X-Request-ID` to every
  state-changing request. A retry therefore cannot create a second schema or
  record when the server has already committed the first request.
- Schema updates and publishing use the server-provided revision as `If-Match`;
  a stale tab receives a conflict instead of overwriting another editor.
- The Operations tab renders only bounded inbox counters and checkpoint
  metadata. It never requests or displays raw event payloads.

## Verification

From the repository root, `make web-check` runs `npm ci`, TypeScript checking,
the API-client contract tests, and a production Next build. The same target is
part of `make check`/`make ci`.
