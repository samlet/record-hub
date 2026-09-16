export type Workspace = {
  id: string;
  tenantId: string;
  name: string;
  version: number;
  updatedAt?: string;
};

export type TableDefinition = {
  id: string;
  tenantId: string;
  workspaceId: string;
  name: string;
  kind: "CUSTOM" | "PROJECTION";
  schemaId: string;
  schemaVersion: number;
  sourcePolicy?: { system: string; type: string; allowFields: string[] };
  version: number;
};

export type RecordItem = {
  id: string;
  tenantId: string;
  workspaceId: string;
  tableId: string;
  schemaId: string;
  schemaVersion: number;
  recordVersion: number;
  tags: string[];
  data: Record<string, unknown>;
  projection?: { lastEventId: string; syncedAt: string; status: string };
  source?: { system: string; type: string; id: string; version: number };
  updatedAt: string;
};

export type SchemaDefinition = {
  tenantId: string;
  schemaId: string;
  name: string;
  version: number;
  revision: number;
  status: string;
  jsonSchema: Record<string, unknown>;
  semanticTypes: string[];
  updatedAt: string;
};

export type OperationsSnapshot = {
  tenantId: string;
  workspaceId: string;
  consumer: string;
  inbox: {
    processing: number;
    applied: number;
    rejected: number;
    failed: number;
    oldestProcessingAt?: string;
  };
  checkpoints: Array<{
    sourceSystem: string;
    aggregateType: string;
    aggregateId: string;
    sourceVersion: number;
    lastEventId: string;
    syncedAt: string;
    status: string;
  }>;
  generatedAt: string;
};

export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;

  constructor(status: number, message: string, code?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

function csrfToken(): string | undefined {
  if (typeof document === "undefined") return undefined;
  const value = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith("record_hub_csrf="));
  return value
    ? decodeURIComponent(value.slice("record_hub_csrf=".length))
    : undefined;
}

async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<{ body: T; headers: Headers }> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !headers.has("Content-Type"))
    headers.set("Content-Type", "application/json");
  if (
    init.method &&
    !["GET", "HEAD", "OPTIONS"].includes(init.method.toUpperCase())
  ) {
    const token = csrfToken();
    if (token) headers.set("X-CSRF-Token", token);
    if (!headers.has("Idempotency-Key")) {
      const random =
        typeof crypto !== "undefined" && "randomUUID" in crypto
          ? crypto.randomUUID()
          : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      headers.set("Idempotency-Key", `web-${random}`);
    }
    if (!headers.has("X-Request-ID"))
      headers.set(
        "X-Request-ID",
        `web-${Date.now()}-${Math.random().toString(36).slice(2)}`,
      );
  }
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: "include",
  });
  const text = await response.text();
  let body: unknown = undefined;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = { message: text };
    }
  }
  if (!response.ok) {
    const error = body as
      | { error?: { code?: string; message?: string }; message?: string }
      | undefined;
    throw new ApiError(
      response.status,
      error?.error?.message ??
        error?.message ??
        `Request failed (${response.status})`,
      error?.error?.code,
    );
  }
  return { body: body as T, headers: response.headers };
}

const items = <T>(body: { items?: T[] }): T[] => body.items ?? [];

export const api = {
  session: () =>
    request<{ authenticated: boolean; issuer: string; subject: string }>(
      "/auth/session",
    ),
  workspaces: (tenantId: string) =>
    request<{ items: Workspace[] }>(
      `/api/v1/workspaces?tenantId=${encodeURIComponent(tenantId)}`,
    ),
  createWorkspace: (input: { tenantId: string; id: string; name: string }) =>
    request<Workspace>("/api/v1/workspaces", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  tables: (tenantId: string, workspaceId: string) =>
    request<{ items: TableDefinition[] }>(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/tables?tenantId=${encodeURIComponent(tenantId)}`,
    ),
  createTable: (
    workspaceId: string,
    input: {
      tenantId: string;
      id: string;
      name: string;
      kind: TableDefinition["kind"];
      schemaId: string;
      schemaVersion: number;
    },
  ) =>
    request<TableDefinition>(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/tables`,
      { method: "POST", body: JSON.stringify(input) },
    ),
  records: (tenantId: string, workspaceId: string, tableId: string) =>
    request<{ items: RecordItem[]; nextCursor?: string }>(
      `/api/v1/tables/${encodeURIComponent(tableId)}/records?tenantId=${encodeURIComponent(tenantId)}&workspaceId=${encodeURIComponent(workspaceId)}&limit=100`,
    ),
  createRecord: (
    tableId: string,
    input: {
      tenantId: string;
      workspaceId: string;
      id: string;
      data: Record<string, unknown>;
      tags: string[];
    },
  ) =>
    request<RecordItem>(
      `/api/v1/tables/${encodeURIComponent(tableId)}/records`,
      { method: "POST", body: JSON.stringify({ ...input, tableId }) },
    ),
  createSchema: (input: {
    tenantId: string;
    workspaceId: string;
    schemaId: string;
    name: string;
    version: number;
    jsonSchema: Record<string, unknown>;
    semanticTypes: string[];
  }) =>
    request<SchemaDefinition>("/api/v1/schemas", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  getSchema: (
    tenantId: string,
    workspaceId: string,
    schemaId: string,
    version: number,
  ) =>
    request<SchemaDefinition>(
      `/api/v1/schemas/${encodeURIComponent(schemaId)}?tenantId=${encodeURIComponent(tenantId)}&workspaceId=${encodeURIComponent(workspaceId)}&version=${version}`,
    ),
  updateSchema: (
    schemaId: string,
    revision: number,
    input: {
      tenantId: string;
      workspaceId: string;
      name: string;
      version: number;
      jsonSchema: Record<string, unknown>;
      semanticTypes: string[];
    },
  ) =>
    request<SchemaDefinition>(
      `/api/v1/schemas/${encodeURIComponent(schemaId)}/draft`,
      {
        method: "PUT",
        headers: { "If-Match": `"${revision}"` },
        body: JSON.stringify({ ...input, schemaId }),
      },
    ),
  publishSchema: (
    schemaId: string,
    revision: number,
    input: { tenantId: string; workspaceId: string; version: number },
  ) =>
    request<SchemaDefinition>(
      `/api/v1/schemas/${encodeURIComponent(schemaId)}/publish`,
      {
        method: "POST",
        headers: { "If-Match": `"${revision}"` },
        body: JSON.stringify(input),
      },
    ),
  operations: (tenantId: string, workspaceId: string, consumer: string) =>
    request<OperationsSnapshot>(
      `/api/v1/operations/events?tenantId=${encodeURIComponent(tenantId)}&workspaceId=${encodeURIComponent(workspaceId)}&consumer=${encodeURIComponent(consumer)}&limit=50`,
    ),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
};

export { items };
