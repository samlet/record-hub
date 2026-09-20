/** Framework-neutral parity types for Record Hub owner adapters. */

/** Stable owner/type/id reference; business payloads are intentionally absent. */
export interface RecordRef {
  owner: string;
  type: string;
  id: string;
}

/** Idempotent command acknowledgement containing metadata only. */
export interface Receipt {
  operationId: string;
  status: string;
  replayed: boolean;
  occurredAt: string;
}

/** Shared command/result envelope used by generated and hand-written clients. */
export interface CommandResult<T = unknown> {
  operationId: string;
  accepted: boolean;
  receipt: Receipt;
  data?: T;
  error?: ApiError;
}

/** Portable API error with forward-compatible string codes. */
export class ApiError extends Error {
  readonly statusCode: number;
  readonly code: string;

  constructor(statusCode: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.statusCode = statusCode;
    this.code = code;
  }
}

export type ErrorCode =
  | "INVALID_ARGUMENT"
  | "UNAUTHORIZED"
  | "FORBIDDEN"
  | "NOT_FOUND"
  | "CONFLICT"
  | "RATE_LIMITED"
  | (string & {});
