import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function jsonResponse(
  value: unknown,
  status = 200,
  headers?: Record<string, string>,
) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

describe("Record Hub browser API client", () => {
  it("sends CSRF, idempotency, request and CAS headers for a mutation", async () => {
    vi.stubGlobal("document", { cookie: "record_hub_csrf=signed-token" });
    fetchMock.mockResolvedValue(jsonResponse({ id: "record-1" }));

    await api.updateRecord("record-1", 7, {
      tenantId: "tenant-1",
      workspaceId: "workspace-1",
      tableId: "table-1",
      id: "record-1",
      data: { status: "READY" },
      tags: ["important"],
    });

    const [, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init?.headers);
    expect(headers.get("X-CSRF-Token")).toBe("signed-token");
    expect(headers.get("If-Match")).toBe('"7"');
    expect(headers.get("Idempotency-Key")).toMatch(/^web-/);
    expect(headers.get("X-Request-ID")).toMatch(/^web-/);
    expect(JSON.parse(String(init?.body))).toMatchObject({
      tableId: "table-1",
    });
  });

  it("keeps schema and view scope in encoded URLs", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ schemaId: "schema/a" }));
    await api.getSchema("tenant one", "workspace/one", "schema/a", 2);
    expect(String(fetchMock.mock.calls[0][0])).toContain(
      "/api/v1/schemas/schema%2Fa?",
    );
    expect(String(fetchMock.mock.calls[0][0])).toContain(
      "tenantId=tenant%20one",
    );
    expect(String(fetchMock.mock.calls[0][0])).toContain(
      "workspaceId=workspace%2Fone",
    );
    expect(String(fetchMock.mock.calls[0][0])).toContain("version=2");

    fetchMock.mockResolvedValueOnce(jsonResponse({ items: [] }));
    await api.views("tenant one", "workspace/one", "table/a");
    expect(String(fetchMock.mock.calls[1][0])).toContain(
      "/api/v1/tables/table%2Fa/views?",
    );
  });

  it("serializes bounded multi-condition view definitions", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ id: "view-1" }));
    await api.createView("table-1", {
      tenantId: "tenant-1",
      workspaceId: "workspace-1",
      id: "view-1",
      name: "待处理",
      columns: ["data.status", "data.priority"],
      filters: [
        { field: "data.status", operator: "eq", value: "OPEN" },
        { field: "data.priority", operator: "gte", value: 3 },
      ],
      sorts: [
        { field: "data.priority", direction: "desc" },
        { field: "data.status", direction: "asc" },
      ],
    });

    const [, init] = fetchMock.mock.calls[0];
    expect(JSON.parse(String(init?.body))).toMatchObject({
      tableId: "table-1",
      filters: [
        { field: "data.status", operator: "eq", value: "OPEN" },
        { field: "data.priority", operator: "gte", value: 3 },
      ],
      sorts: [
        { field: "data.priority", direction: "desc" },
        { field: "data.status", direction: "asc" },
      ],
    });
  });

  it("decodes bounded API errors into ApiError", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(
        { error: { code: "SCHEMA_REVISION_CONFLICT", message: "stale" } },
        409,
      ),
    );
    let caught: unknown;
    try {
      await api.publishSchema("schema-1", 4, {
        tenantId: "tenant-1",
        workspaceId: "workspace-1",
        version: 1,
      });
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect(caught).toMatchObject({
      status: 409,
      code: "SCHEMA_REVISION_CONFLICT",
      message: "stale",
    });
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
