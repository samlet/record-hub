# Workflow Binding SDKs

The SDKs in this directory are thin, framework-neutral facades over the
public OpenAPI binding contract. They do not import Record Hub server
internals, Temporal, Spring, or Conductor.

- `go/recordhub`: context-aware Go client with bounded responses and typed API errors.
- `java`: Java 17+/Kotlin-callable client based on `java.net.http.HttpClient` and Jackson.
- `typescript`: framework-neutral parity types for typed refs, command/results, receipts and API errors.

All three local facades expose the same metadata-only parity vocabulary: `RecordRef`,
`CommandResult`, `Receipt` and an extensible `ErrorCode`/`ApiError`. They do not
carry raw business payloads in receipts and remain independent of workflow SDKs.

The complete generated clients (Go, Java, TypeScript and Go server stubs) are
still reproducible with `make generate-clients`; generated output remains in
`build/generated-clients` and is intentionally not committed.

Both facades preserve the binding protocol: callers provide a stable
`Idempotency-Key`, expected record/source versions, and a purpose; retries use
the same operation ID and never perform I/O from deterministic workflow code.
