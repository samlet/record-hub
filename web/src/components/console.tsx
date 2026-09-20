"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";
import {
  api,
  ApiError,
  items,
  OperationsSnapshot,
  RecordRelation,
  RecordItem,
  SchemaCompatibilityReport,
  SchemaDefinition,
  SchemaSummary,
  TenderApplicationAssociation,
  TableDefinition,
  ViewFilter,
  ViewDefinition,
  Workspace,
} from "../lib/api";
import {
  parseSchemaFields,
  SchemaField,
  SchemaFieldType,
  writeSchemaFields,
} from "../lib/schema-fields";

type Tab = "records" | "schema" | "operations" | "approvals";
const consumerOptions = [
  ["record-hub-approver-projection-v1", "Approver"],
  ["record-hub-fluxion-projection-v1", "Fluxion"],
  ["record-hub-bids-projection-v1", "Bids"],
];

const initialSchema =
  '{\n  "$schema": "https://json-schema.org/draft/2020-12/schema",\n  "type": "object",\n  "properties": {},\n  "additionalProperties": true\n}';

type ViewFilterDraft = {
  field: string;
  operator: ViewFilter["operator"];
  value: string;
};

type ViewSortDraft = { field: string; direction: "asc" | "desc" };

type ViewForm = {
  id: string;
  name: string;
  columns: string;
  filters: ViewFilterDraft[];
  sorts: ViewSortDraft[];
};

const emptyViewForm = (): ViewForm => ({
  id: "",
  name: "",
  columns: "",
  filters: [{ field: "", operator: "eq", value: "" }],
  sorts: [{ field: "", direction: "asc" }],
});

function parseFilterValue(raw: string): unknown {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  try {
    return JSON.parse(trimmed);
  } catch {
    return raw;
  }
}

function filterValueDraft(value: unknown): string {
  return typeof value === "string" ? value : (JSON.stringify(value) ?? "");
}

function parseRelationsDraft(raw: string): RecordRelation[] {
  const trimmed = raw.trim();
  if (!trimmed) return [];
  const parsed: unknown = JSON.parse(trimmed);
  if (!Array.isArray(parsed)) {
    throw new Error("关系必须是 JSON 数组");
  }
  return parsed.map((item, index) => {
    if (!item || typeof item !== "object") {
      throw new Error(`关系 ${index + 1} 必须是对象`);
    }
    const relation = item as Partial<RecordRelation>;
    const target = relation.target;
    if (
      !target ||
      typeof target !== "object" ||
      !target.system ||
      !target.type ||
      !target.id ||
      !relation.relationType
    ) {
      throw new Error(`关系 ${index + 1} 缺少 target 或 relationType`);
    }
    return {
      target: {
        system: String(target.system).trim(),
        type: String(target.type).trim(),
        id: String(target.id).trim(),
      },
      relationType: String(relation.relationType).trim(),
      ...(relation.resolvedRecordId
        ? { resolvedRecordId: String(relation.resolvedRecordId).trim() }
        : {}),
      status: relation.status ?? "CURRENT",
    };
  });
}

function formFromView(view: ViewDefinition): ViewForm {
  return {
    id: view.id,
    name: view.name,
    columns: view.columns.join(", "),
    filters:
      view.filters.length > 0
        ? view.filters.map((filter) => ({
            ...filter,
            value: filterValueDraft(filter.value),
          }))
        : [{ field: "", operator: "eq", value: "" }],
    sorts:
      view.sorts.length > 0
        ? view.sorts.map((sort) => ({ ...sort }))
        : [{ field: "", direction: "asc" }],
  };
}

export function Console() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [tenantId, setTenantId] = useState("tenant-local");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceId, setWorkspaceId] = useState("");
  const [tables, setTables] = useState<TableDefinition[]>([]);
  const [tableId, setTableId] = useState("");
  const [records, setRecords] = useState<RecordItem[]>([]);
  const [tableSchema, setTableSchema] = useState<SchemaDefinition | null>(null);
  const [editingRecord, setEditingRecord] = useState<RecordItem | null>(null);
  const [views, setViews] = useState<ViewDefinition[]>([]);
  const [viewId, setViewId] = useState("");
  const [editingViewId, setEditingViewId] = useState("");
  const [viewForm, setViewForm] = useState<ViewForm>(emptyViewForm);
  const [tab, setTab] = useState<Tab>("records");
  const [consumer, setConsumer] = useState(consumerOptions[0][0]);
  const [operations, setOperations] = useState<OperationsSnapshot | null>(null);
  const [associations, setAssociations] = useState<TenderApplicationAssociation[]>([]);
  const [schema, setSchema] = useState<SchemaDefinition | null>(null);
  const [schemas, setSchemas] = useState<SchemaSummary[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [workspaceForm, setWorkspaceForm] = useState({ id: "", name: "" });
  const [tableForm, setTableForm] = useState({
    id: "",
    name: "",
    schemaId: "",
    schemaVersion: "1",
  });
  const [recordForm, setRecordForm] = useState({
    id: "",
    tags: "",
    relations: "[]",
    data: "{}",
  });
  const [schemaForm, setSchemaForm] = useState({
    id: "",
    name: "",
    version: "1",
    data: initialSchema,
    semanticTypes: "",
  });

  const selectedWorkspace = useMemo(
    () => workspaces.find((item) => item.id === workspaceId),
    [workspaces, workspaceId],
  );
  const selectedTable = useMemo(
    () => tables.find((item) => item.id === tableId),
    [tables, tableId],
  );

  useEffect(() => {
    api
      .session()
      .then(() => setAuthenticated(true))
      .catch(() => setAuthenticated(false));
  }, []);

  useEffect(() => {
    const saved = localStorage.getItem("record-hub-tenant");
    if (saved) setTenantId(saved);
  }, []);

  useEffect(() => {
    if (!tenantId || authenticated !== true) return;
    localStorage.setItem("record-hub-tenant", tenantId);
    run(async () => {
      const response = await api.workspaces(tenantId);
      const next = items(response.body);
      setWorkspaces(next);
      setWorkspaceId((current) =>
        next.some((item) => item.id === current)
          ? current
          : (next[0]?.id ?? ""),
      );
    });
  }, [tenantId, authenticated]);

  useEffect(() => {
    if (!tenantId || !workspaceId || authenticated !== true) return;
    run(async () => {
      const response = await api.tables(tenantId, workspaceId);
      const next = items(response.body);
      setTables(next);
      setTableId((current) =>
        next.some((item) => item.id === current)
          ? current
          : (next[0]?.id ?? ""),
      );
    });
  }, [tenantId, workspaceId, authenticated]);

  useEffect(() => {
    if (!tenantId || !workspaceId || !tableId || authenticated !== true) return;
    run(async () => {
      const response = await api.views(tenantId, workspaceId, tableId);
      const next = items(response.body);
      setViews(next);
      setViewId((current) =>
        next.some((item) => item.id === current) ? current : "",
      );
      setEditingViewId("");
      setViewForm(emptyViewForm());
    });
  }, [tenantId, workspaceId, tableId, authenticated]);

  useEffect(() => {
    if (!tenantId || !workspaceId || !selectedTable || authenticated !== true) {
      setTableSchema(null);
      return;
    }
    let active = true;
    api
      .getSchema(
        tenantId,
        workspaceId,
        selectedTable.schemaId,
        selectedTable.schemaVersion,
      )
      .then((response) => {
        if (active) setTableSchema(response.body);
      })
      .catch(() => {
        if (active) setTableSchema(null);
      });
    return () => {
      active = false;
    };
  }, [tenantId, workspaceId, selectedTable, authenticated]);

  useEffect(() => {
    if (
      !tenantId ||
      !workspaceId ||
      !tableId ||
      authenticated !== true ||
      tab !== "records"
    )
      return;
    run(async () =>
      setRecords(
        items((await api.records(tenantId, workspaceId, tableId, viewId)).body),
      ),
    );
  }, [tenantId, workspaceId, tableId, viewId, authenticated, tab]);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (caught) {
      setError(
        caught instanceof ApiError
          ? caught.message
          : caught instanceof Error
            ? caught.message
          : "请求失败，请检查 Record Hub API",
      );
    } finally {
      setBusy(false);
    }
  }

  function submit(event: FormEvent, action: () => Promise<void>) {
    event.preventDefault();
    void run(async () => {
      await action();
      setNotice("已保存");
    });
  }

  async function refreshOperations() {
    if (!tenantId || !workspaceId) return;
    await run(async () =>
      setOperations(
        (await api.operations(tenantId, workspaceId, consumer)).body,
      ),
    );
  }

  async function refreshSchemas() {
    if (!tenantId || !workspaceId) return;
    setSchemas(items((await api.schemas(tenantId, workspaceId)).body));
  }

  async function loadSchemaDefinition(schemaId: string, version: number) {
    const loaded = await api.getSchema(
      tenantId,
      workspaceId,
      schemaId,
      version,
    );
    setSchema(loaded.body);
    setSchemaForm({
      id: loaded.body.schemaId,
      name: loaded.body.name,
      version: String(loaded.body.version),
      data: JSON.stringify(loaded.body.jsonSchema, null, 2),
      semanticTypes: loaded.body.semanticTypes.join(", "),
    });
  }

  useEffect(() => {
    if (
      tab !== "operations" ||
      authenticated !== true ||
      !tenantId ||
      !workspaceId
    )
      return;
    void refreshOperations();
    const timer = window.setInterval(() => void refreshOperations(), 10_000);
    return () => window.clearInterval(timer);
  }, [tab, authenticated, tenantId, workspaceId, consumer]);

  useEffect(() => {
    if (tab !== "approvals" || authenticated !== true || !tenantId || !workspaceId) return;
    void run(async () =>
      setAssociations(
        (await api.tenderApplicationAssociations(tenantId, workspaceId)).body.items ?? [],
      ),
    );
  }, [tab, authenticated, tenantId, workspaceId]);

  useEffect(() => {
    if (tab !== "schema" || authenticated !== true || !tenantId || !workspaceId)
      return;
    void run(refreshSchemas);
  }, [tab, authenticated, tenantId, workspaceId]);

  async function signOut() {
    await run(async () => {
      await api.logout();
      setAuthenticated(false);
    });
  }

  if (authenticated === null)
    return <main className="center-state">正在连接 Record Hub…</main>;
  if (!authenticated)
    return (
      <main className="center-state">
        <div className="login-card">
          <span className="eyebrow">RECORD HUB</span>
          <h1>把流程数据放在一个可追踪的工作区</h1>
          <p>使用本地 Dex 登录后，管理 schema、多维表格和投影状态。</p>
          <a className="primary-button" href="/auth/login">
            使用 Dex 登录
          </a>
          <p className="muted">
            若登录入口不可用，请确认 Go BFF 已启用{" "}
            <code>RECORD_HUB_WEB_ENABLED=true</code>。
          </p>
        </div>
      </main>
    );

  return (
    <main className="shell">
      <header className="topbar">
        <div>
          <span className="eyebrow">RECORD HUB / CONSOLE</span>
          <h1>多维记录工作台</h1>
        </div>
        <div className="top-actions">
          <label className="compact-field">
            Tenant ID
            <input
              value={tenantId}
              onChange={(event) => setTenantId(event.target.value)}
            />
          </label>
          <button className="ghost-button" onClick={() => void signOut()}>
            退出
          </button>
        </div>
      </header>
      <section className="workspace-bar">
        <label>
          工作区
          <select
            value={workspaceId}
            onChange={(event) => setWorkspaceId(event.target.value)}
          >
            <option value="">选择工作区</option>
            {workspaces.map((workspace) => (
              <option key={workspace.id} value={workspace.id}>
                {workspace.name} · {workspace.id}
              </option>
            ))}
          </select>
        </label>
        {selectedWorkspace && (
          <span className="scope-chip">{selectedWorkspace.id}</span>
        )}
        <span className="sync-state">{busy ? "同步中…" : "已连接"}</span>
      </section>
      {error && <div className="alert error">{error}</div>}
      {notice && (
        <div className="alert success" onAnimationEnd={() => setNotice("")}>
          {notice}
        </div>
      )}
      <section className="grid-layout">
        <aside className="sidebar">
          <div className="panel-heading">
            <h2>工作区</h2>
            <span>{workspaces.length}</span>
          </div>
          <form
            className="stack-form"
            onSubmit={(event) =>
              submit(event, async () => {
                const created = await api.createWorkspace({
                  tenantId,
                  ...workspaceForm,
                });
                setWorkspaceForm({ id: "", name: "" });
                setWorkspaces((current) => [...current, created.body]);
                setWorkspaceId(created.body.id);
              })
            }
          >
            <input
              required
              placeholder="workspace id"
              value={workspaceForm.id}
              onChange={(event) =>
                setWorkspaceForm({ ...workspaceForm, id: event.target.value })
              }
            />
            <input
              required
              placeholder="工作区名称"
              value={workspaceForm.name}
              onChange={(event) =>
                setWorkspaceForm({ ...workspaceForm, name: event.target.value })
              }
            />
            <button className="secondary-button">新建工作区</button>
          </form>
          <div className="list">
            {workspaces.map((workspace) => (
              <button
                className={`list-row ${workspace.id === workspaceId ? "selected" : ""}`}
                key={workspace.id}
                onClick={() => setWorkspaceId(workspace.id)}
              >
                <strong>{workspace.name}</strong>
                <small>{workspace.id}</small>
              </button>
            ))}
            {workspaces.length === 0 && <p className="muted">还没有工作区。</p>}
          </div>
        </aside>
        <div className="content">
          <nav className="tabs">
            <button
              className={tab === "records" ? "active" : ""}
              onClick={() => setTab("records")}
            >
              表格与记录
            </button>
            <button
              className={tab === "schema" ? "active" : ""}
              onClick={() => setTab("schema")}
            >
              Schema
            </button>
            <button
              className={tab === "operations" ? "active" : ""}
              onClick={() => setTab("operations")}
            >
              投影运维
            </button>
            <button
              className={tab === "approvals" ? "active" : ""}
              onClick={() => setTab("approvals")}
            >
              审批关联
            </button>
          </nav>
          {tab === "records" && (
            <RecordsPanel
              tenantId={tenantId}
              workspaceId={workspaceId}
              tableId={tableId}
              tables={tables}
              views={views}
              viewId={viewId}
              editingViewId={editingViewId}
              viewForm={viewForm}
              setViewForm={setViewForm}
              selectedTable={selectedTable}
              tableSchema={tableSchema}
              records={records}
              tableForm={tableForm}
              setTableForm={setTableForm}
              recordForm={recordForm}
              setRecordForm={setRecordForm}
              onTableChange={setTableId}
              onViewChange={setViewId}
              onEditView={() => {
                const selected = views.find((view) => view.id === viewId);
                if (!selected) return;
                setEditingViewId(selected.id);
                setViewForm(formFromView(selected));
              }}
              onCancelViewEdit={() => {
                setEditingViewId("");
                setViewForm(emptyViewForm());
              }}
              onCreateView={(event) =>
                submit(event, async () => {
                  const input = {
                    tenantId,
                    workspaceId,
                    name: viewForm.name,
                    columns: viewForm.columns
                      .split(",")
                      .map((column) => column.trim())
                      .filter(Boolean),
                    filters: viewForm.filters
                      .filter((filter) => filter.field.trim())
                      .map((filter) => ({
                        ...filter,
                        field: filter.field.trim(),
                        value: parseFilterValue(filter.value),
                      })),
                    sorts: viewForm.sorts
                      .filter((sort) => sort.field.trim())
                      .map((sort) => ({ ...sort, field: sort.field.trim() })),
                  };
                  if (editingViewId) {
                    const current = views.find(
                      (view) => view.id === editingViewId,
                    );
                    if (!current) return;
                    const updated = await api.updateView(
                      tableId,
                      current.id,
                      current.version,
                      input,
                    );
                    setViews((items) =>
                      items.map((view) =>
                        view.id === updated.body.id ? updated.body : view,
                      ),
                    );
                    setViewId(updated.body.id);
                  } else {
                    const created = await api.createView(tableId, {
                      ...input,
                      id: viewForm.id,
                    });
                    setViews((current) => [...current, created.body]);
                    setViewId(created.body.id);
                  }
                  setEditingViewId("");
                  setViewForm(emptyViewForm());
                })
              }
              onCreateTable={(event) =>
                submit(event, async () => {
                  const created = await api.createTable(workspaceId, {
                    tenantId,
                    id: tableForm.id,
                    name: tableForm.name,
                    kind: "CUSTOM",
                    schemaId: tableForm.schemaId,
                    schemaVersion: Number(tableForm.schemaVersion),
                  });
                  setTables((current) => [...current, created.body]);
                  setTableId(created.body.id);
                  setTableForm({
                    id: "",
                    name: "",
                    schemaId: "",
                    schemaVersion: "1",
                  });
                })
              }
              onCreateRecord={(event) =>
                submit(event, async () => {
                  const created = await api.createRecord(tableId, {
                    tenantId,
                    workspaceId,
                    id: recordForm.id,
                    tags: recordForm.tags
                      .split(",")
                      .map((tag) => tag.trim())
                      .filter(Boolean),
                    relations: parseRelationsDraft(recordForm.relations),
                    data: JSON.parse(recordForm.data),
                  });
                  setRecords((current) => [created.body, ...current]);
                  setRecordForm({ id: "", tags: "", relations: "[]", data: "{}" });
                })
              }
              editingRecord={editingRecord}
              onEditRecord={(record) => {
                setEditingRecord(record);
                setRecordForm({
                  id: record.id,
                  tags: record.tags.join(", "),
                  relations: JSON.stringify(record.relations ?? [], null, 2),
                  data: JSON.stringify(record.data, null, 2),
                });
              }}
              onCancelEdit={() => {
                setEditingRecord(null);
                setRecordForm({ id: "", tags: "", relations: "[]", data: "{}" });
              }}
              onUpdateRecord={(event) =>
                submit(event, async () => {
                  if (!editingRecord) return;
                  const updated = await api.updateRecord(
                    editingRecord.id,
                    editingRecord.recordVersion,
                    {
                      tenantId,
                      workspaceId,
                      tableId,
                      id: editingRecord.id,
                      tags: recordForm.tags
                        .split(",")
                        .map((tag) => tag.trim())
                        .filter(Boolean),
                      relations: parseRelationsDraft(recordForm.relations),
                      data: JSON.parse(recordForm.data),
                    },
                  );
                  setRecords((current) =>
                    current.map((record) =>
                      record.id === updated.body.id ? updated.body : record,
                    ),
                  );
                  setEditingRecord(null);
                  setRecordForm({ id: "", tags: "", relations: "[]", data: "{}" });
                })
              }
              onDeleteRecord={(record) =>
                void run(async () => {
                  if (!window.confirm(`删除记录 ${record.id}？`)) return;
                  await api.deleteRecord(record);
                  setRecords((current) =>
                    current.filter((item) => item.id !== record.id),
                  );
                })
              }
            />
          )}{" "}
          {tab === "schema" && (
            <SchemaPanel
              tenantId={tenantId}
              workspaceId={workspaceId}
              schema={schema}
              schemas={schemas}
              form={schemaForm}
              setForm={setSchemaForm}
              onCreate={(event) =>
                submit(event, async () => {
                  const created = await api.createSchema({
                    tenantId,
                    workspaceId,
                    schemaId: schemaForm.id,
                    name: schemaForm.name,
                    version: Number(schemaForm.version),
                    jsonSchema: JSON.parse(schemaForm.data),
                    semanticTypes: schemaForm.semanticTypes
                      .split(",")
                      .map((item) => item.trim())
                      .filter(Boolean),
                  });
                  setSchema(created.body);
                  await refreshSchemas();
                })
              }
              onUpdate={(event) =>
                submit(event, async () => {
                  if (!schema) return;
                  const updated = await api.updateSchema(
                    schema.schemaId,
                    schema.revision,
                    {
                      tenantId,
                      workspaceId,
                      name: schemaForm.name,
                      version: Number(schemaForm.version),
                      jsonSchema: JSON.parse(schemaForm.data),
                      semanticTypes: schemaForm.semanticTypes
                        .split(",")
                        .map((item) => item.trim())
                        .filter(Boolean),
                    },
                  );
                  setSchema(updated.body);
                  await refreshSchemas();
                })
              }
              onLoad={() =>
                void run(async () => {
                  await loadSchemaDefinition(
                    schemaForm.id,
                    Number(schemaForm.version),
                  );
                })
              }
              onSelect={(summary) =>
                void run(async () => {
                  await loadSchemaDefinition(summary.schemaId, summary.version);
                })
              }
              onReset={() => {
                setSchema(null);
                setSchemaForm({
                  ...schemaForm,
                  name: "",
                  data: initialSchema,
                  semanticTypes: "",
                });
              }}
              onPublish={() =>
                void run(async () => {
                  if (!schema) return;
                  setSchema(
                    (
                      await api.publishSchema(
                        schema.schemaId,
                        schema.revision,
                        { tenantId, workspaceId, version: schema.version },
                      )
                    ).body,
                  );
                  await refreshSchemas();
                  setNotice("Schema 已发布");
                })
              }
            />
          )}{" "}
          {tab === "operations" && (
            <OperationsPanel
              snapshot={operations}
              consumer={consumer}
              setConsumer={setConsumer}
              onRefresh={() => void refreshOperations()}
            />
          )}{" "}
          {tab === "approvals" && (
            <ApprovalAssociationsPanel associations={associations} />
          )}{" "}
        </div>
      </section>
    </main>
  );
}

function ApprovalAssociationsPanel({
  associations,
}: {
  associations: TenderApplicationAssociation[];
}) {
  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h2>审批关联</h2>
          <p className="muted">
            只读查看 Tender 与 Approver Application 的安全关联投影；终态只能由业务系统和投影 worker 写入。
          </p>
        </div>
        <span className="scope-chip">Viewer / Operator read-only</span>
      </div>
      {associations.length === 0 ? (
        <p className="muted">当前工作区没有审批关联。</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Tender</th>
                <th>Application</th>
                <th>状态</th>
                <th>版本</th>
                <th>投影</th>
                <th>更新时间</th>
              </tr>
            </thead>
            <tbody>
              {associations.map((association) => (
                <tr key={association.id}>
                  <td><code>{association.tenderRef}</code></td>
                  <td><code>{association.applicationRef}</code></td>
                  <td>{association.approvalStatus}</td>
                  <td>g{association.approvalGeneration} / d{association.decisionVersion}</td>
                  <td>{association.projectionState}</td>
                  <td>{new Date(association.updatedAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function RecordsPanel(props: {
  tenantId: string;
  workspaceId: string;
  tableId: string;
  tables: TableDefinition[];
  views: ViewDefinition[];
  viewId: string;
  editingViewId: string;
  viewForm: ViewForm;
  setViewForm: (value: ViewForm) => void;
  selectedTable?: TableDefinition;
  tableSchema: SchemaDefinition | null;
  records: RecordItem[];
  tableForm: {
    id: string;
    name: string;
    schemaId: string;
    schemaVersion: string;
  };
  setTableForm: (value: {
    id: string;
    name: string;
    schemaId: string;
    schemaVersion: string;
  }) => void;
  recordForm: { id: string; tags: string; relations: string; data: string };
  setRecordForm: (value: { id: string; tags: string; relations: string; data: string }) => void;
  editingRecord: RecordItem | null;
  onEditRecord: (record: RecordItem) => void;
  onCancelEdit: () => void;
  onUpdateRecord: (event: FormEvent) => void;
  onDeleteRecord: (record: RecordItem) => void;
  onTableChange: (value: string) => void;
  onViewChange: (value: string) => void;
  onEditView: () => void;
  onCancelViewEdit: () => void;
  onCreateView: (event: FormEvent) => void;
  onCreateTable: (event: FormEvent) => void;
  onCreateRecord: (event: FormEvent) => void;
}) {
  const {
    tenantId,
    workspaceId,
    tableId,
    tables,
    views,
    viewId,
    editingViewId,
    viewForm,
    setViewForm,
    selectedTable,
    tableSchema,
    records,
    tableForm,
    setTableForm,
    recordForm,
    setRecordForm,
    editingRecord,
    onEditRecord,
    onCancelEdit,
    onUpdateRecord,
    onDeleteRecord,
    onTableChange,
    onViewChange,
    onEditView,
    onCancelViewEdit,
    onCreateView,
    onCreateTable,
    onCreateRecord,
  } = props;

  const selectedView = useMemo(
    () => views.find((view) => view.id === viewId),
    [views, viewId],
  );
  const availableColumns = useMemo(() => {
    const dataColumns = records.flatMap((record) =>
      Object.keys(record.data).map((field) => `data.${field}`),
    );
    return Array.from(
      new Set([
        ...dataColumns,
        ...(selectedView?.columns ?? []),
        ...viewForm.columns
          .split(",")
          .map((column) => column.trim())
          .filter(Boolean),
      ]),
    ).slice(0, 64);
  }, [records, selectedView, viewForm.columns]);
  const configuredColumns = viewForm.columns
    .split(",")
    .map((column) => column.trim())
    .filter(Boolean);
  const selectedColumns = new Set(
    configuredColumns.length > 0 ? configuredColumns : availableColumns,
  );

  function toggleViewColumn(column: string) {
    const next = new Set(selectedColumns);
    if (next.has(column)) {
      if (next.size <= 1) return;
      next.delete(column);
    } else {
      next.add(column);
    }
    setViewForm({ ...viewForm, columns: Array.from(next).join(", ") });
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h2>表格与记录</h2>
          <p className="muted">
            工作区内的 schema 映射和只读投影都从同一个记录入口读取。
          </p>
        </div>
        <select
          className="table-select"
          value={tableId}
          onChange={(event) => onTableChange(event.target.value)}
        >
          <option value="">选择表格</option>
          {tables.map((table) => (
            <option key={table.id} value={table.id}>
              {table.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          className="ghost-button"
          disabled={!viewId}
          onClick={onEditView}
        >
          编辑视图
        </button>
        <select
          className="table-select"
          value={viewId}
          onChange={(event) => onViewChange(event.target.value)}
        >
          <option value="">所有记录</option>
          {views.map((view) => (
            <option key={view.id} value={view.id}>
              视图：{view.name}
            </option>
          ))}
        </select>
      </div>
      <div className="form-grid">
        <form className="inline-form" onSubmit={onCreateTable}>
          <h3>新建自定义表</h3>
          <input
            required
            placeholder="table id"
            value={tableForm.id}
            onChange={(event) =>
              setTableForm({ ...tableForm, id: event.target.value })
            }
          />
          <input
            required
            placeholder="表格名称"
            value={tableForm.name}
            onChange={(event) =>
              setTableForm({ ...tableForm, name: event.target.value })
            }
          />
          <input
            required
            placeholder="schema id"
            value={tableForm.schemaId}
            onChange={(event) =>
              setTableForm({ ...tableForm, schemaId: event.target.value })
            }
          />
          <input
            required
            type="number"
            min="1"
            placeholder="schema version"
            value={tableForm.schemaVersion}
            onChange={(event) =>
              setTableForm({ ...tableForm, schemaVersion: event.target.value })
            }
          />
          <button className="secondary-button" disabled={!workspaceId}>
            创建表格
          </button>
        </form>
        <form
          className="inline-form"
          onSubmit={editingRecord ? onUpdateRecord : onCreateRecord}
        >
          <h3>
            {editingRecord ? `编辑记录 · ${editingRecord.id}` : "添加记录"}
          </h3>
          <input
            required
            placeholder="record id"
            value={recordForm.id}
            disabled={Boolean(editingRecord)}
            onChange={(event) =>
              setRecordForm({ ...recordForm, id: event.target.value })
            }
          />
          <input
            placeholder="tags（逗号分隔）"
            value={recordForm.tags}
            onChange={(event) =>
              setRecordForm({ ...recordForm, tags: event.target.value })
            }
          />
          <textarea
            className="code-input"
            rows={4}
            aria-label="typed relations"
            placeholder={'relations（JSON 数组，如 [{"target":{"system":"fluxion","type":"PROJECT","id":"p-1"},"relationType":"tracks"}]）'}
            value={recordForm.relations}
            onChange={(event) =>
              setRecordForm({ ...recordForm, relations: event.target.value })
            }
          />
          <textarea
            required
            rows={3}
            value={recordForm.data}
            onChange={(event) =>
              setRecordForm({ ...recordForm, data: event.target.value })
            }
          />
          <div className="button-row">
            <button className="secondary-button" disabled={!tableId}>
              {editingRecord ? "保存修改" : "写入记录"}
            </button>
            {editingRecord && (
              <button
                type="button"
                className="ghost-button"
                onClick={onCancelEdit}
              >
                取消
              </button>
            )}
          </div>
        </form>
        <form className="inline-form" onSubmit={onCreateView}>
          <div className="view-builder-heading">
            <h3>{editingViewId ? "编辑视图" : "新建视图"}</h3>
            {editingViewId && (
              <button
                type="button"
                className="ghost-button"
                onClick={onCancelViewEdit}
              >
                取消编辑
              </button>
            )}
          </div>
          <input
            required
            placeholder="view id"
            value={viewForm.id}
            disabled={Boolean(editingViewId)}
            onChange={(event) =>
              setViewForm({ ...viewForm, id: event.target.value })
            }
          />
          <input
            required
            placeholder="视图名称"
            value={viewForm.name}
            onChange={(event) =>
              setViewForm({ ...viewForm, name: event.target.value })
            }
          />
          <input
            placeholder="列（逗号分隔，如 data.status）"
            value={viewForm.columns}
            onChange={(event) =>
              setViewForm({ ...viewForm, columns: event.target.value })
            }
          />
          <div className="view-builder-heading">
            <strong>列显隐</strong>
            <span className="muted">从当前记录快速选择列</span>
          </div>
          {availableColumns.length > 0 ? (
            <div className="column-picker">
              {availableColumns.map((column) => (
                <label className="column-option" key={column}>
                  <input
                    type="checkbox"
                    checked={selectedColumns.has(column)}
                    disabled={
                      selectedColumns.size <= 1 && selectedColumns.has(column)
                    }
                    onChange={() => toggleViewColumn(column)}
                  />
                  <code>{column}</code>
                </label>
              ))}
            </div>
          ) : (
            <p className="muted">暂无可选列；也可以直接输入字段路径。</p>
          )}
          <div className="view-builder-heading">
            <strong>过滤条件</strong>
            <button
              type="button"
              className="ghost-button"
              disabled={viewForm.filters.length >= 16}
              onClick={() =>
                setViewForm({
                  ...viewForm,
                  filters: [
                    ...viewForm.filters,
                    { field: "", operator: "eq", value: "" },
                  ],
                })
              }
            >
              添加条件
            </button>
          </div>
          {viewForm.filters.map((filter, index) => (
            <div className="filter-row" key={`filter-${index}`}>
              <input
                placeholder="过滤字段（如 data.status）"
                value={filter.field}
                onChange={(event) =>
                  setViewForm({
                    ...viewForm,
                    filters: viewForm.filters.map((current, currentIndex) =>
                      currentIndex === index
                        ? { ...current, field: event.target.value }
                        : current,
                    ),
                  })
                }
              />
              <select
                value={filter.operator}
                onChange={(event) =>
                  setViewForm({
                    ...viewForm,
                    filters: viewForm.filters.map((current, currentIndex) =>
                      currentIndex === index
                        ? {
                            ...current,
                            operator: event.target
                              .value as ViewFilter["operator"],
                          }
                        : current,
                    ),
                  })
                }
              >
                <option value="eq">等于</option>
                <option value="contains">包含</option>
                <option value="ne">不等于</option>
                <option value="in">属于列表</option>
                <option value="gt">大于</option>
                <option value="gte">大于等于</option>
                <option value="lt">小于</option>
                <option value="lte">小于等于</option>
              </select>
              <input
                placeholder="过滤值（可填 JSON）"
                value={filter.value}
                onChange={(event) =>
                  setViewForm({
                    ...viewForm,
                    filters: viewForm.filters.map((current, currentIndex) =>
                      currentIndex === index
                        ? { ...current, value: event.target.value }
                        : current,
                    ),
                  })
                }
              />
              {viewForm.filters.length > 1 && (
                <button
                  type="button"
                  className="ghost-button danger-button"
                  onClick={() =>
                    setViewForm({
                      ...viewForm,
                      filters: viewForm.filters.filter(
                        (_, currentIndex) => currentIndex !== index,
                      ),
                    })
                  }
                >
                  移除
                </button>
              )}
            </div>
          ))}
          <div className="view-builder-heading">
            <strong>排序条件</strong>
            <button
              type="button"
              className="ghost-button"
              disabled={viewForm.sorts.length >= 4}
              onClick={() =>
                setViewForm({
                  ...viewForm,
                  sorts: [...viewForm.sorts, { field: "", direction: "asc" }],
                })
              }
            >
              添加排序
            </button>
          </div>
          {viewForm.sorts.map((sort, index) => (
            <div className="filter-row" key={`sort-${index}`}>
              <input
                placeholder="排序字段（可选）"
                value={sort.field}
                onChange={(event) =>
                  setViewForm({
                    ...viewForm,
                    sorts: viewForm.sorts.map((current, currentIndex) =>
                      currentIndex === index
                        ? { ...current, field: event.target.value }
                        : current,
                    ),
                  })
                }
              />
              <select
                value={sort.direction}
                onChange={(event) =>
                  setViewForm({
                    ...viewForm,
                    sorts: viewForm.sorts.map((current, currentIndex) =>
                      currentIndex === index
                        ? {
                            ...current,
                            direction: event.target.value as "asc" | "desc",
                          }
                        : current,
                    ),
                  })
                }
              >
                <option value="asc">升序</option>
                <option value="desc">降序</option>
              </select>
              {viewForm.sorts.length > 1 && (
                <button
                  type="button"
                  className="ghost-button danger-button"
                  onClick={() =>
                    setViewForm({
                      ...viewForm,
                      sorts: viewForm.sorts.filter(
                        (_, currentIndex) => currentIndex !== index,
                      ),
                    })
                  }
                >
                  移除
                </button>
              )}
            </div>
          ))}
          <button className="secondary-button" disabled={!tableId}>
            {editingViewId ? "保存视图" : "创建视图"}
          </button>
        </form>
      </div>
      {selectedTable ? (
        <>
          <div className="subheading">
            <h3>{selectedTable.name}</h3>
            <span className="scope-chip">
              {selectedTable.kind} · {selectedTable.schemaId}@v
              {selectedTable.schemaVersion}
            </span>
          </div>
          <RecordGrid
            records={records}
            selectedView={views.find((view) => view.id === viewId)}
            schema={tableSchema}
            onEdit={onEditRecord}
            onDelete={onDeleteRecord}
          />
        </>
      ) : (
        <div className="empty-state">请选择一个表格查看记录。</div>
      )}
    </div>
  );
}

function RecordGrid({
  records,
  selectedView,
  schema,
  onEdit,
  onDelete,
}: {
  records: RecordItem[];
  selectedView?: ViewDefinition;
  schema: SchemaDefinition | null;
  onEdit: (record: RecordItem) => void;
  onDelete: (record: RecordItem) => void;
}) {
  const availableColumns = useMemo(() => {
    const envelopeColumns = new Set([
      "id",
      "tags",
      "recordVersion",
      "schemaVersion",
      "createdAt",
      "updatedAt",
    ]);
    const inferred = records.flatMap((record) =>
      Object.keys(record.data).map((field) => `data.${field}`),
    );
    const configured = (selectedView?.columns ?? []).filter(
      (column) => !envelopeColumns.has(column.replace(/^data\./, "")),
    );
    return Array.from(
      new Set(configured.length > 0 ? configured : inferred),
    ).slice(0, 32);
  }, [records, selectedView]);
  const columnKey = availableColumns.join("\u0000");
  const [hiddenColumns, setHiddenColumns] = useState<string[]>([]);
  const fieldTypes = useMemo(() => {
    if (!schema) return new Map<string, SchemaField["type"]>();
    const parsed = parseSchemaFields(JSON.stringify(schema.jsonSchema));
    return new Map(parsed.fields.map((field) => [field.name, field.type]));
  }, [schema]);

  useEffect(() => {
    setHiddenColumns([]);
  }, [selectedView?.id, columnKey]);

  const columns = availableColumns.filter(
    (column) => !hiddenColumns.includes(column),
  );
  if (records.length === 0)
    return (
      <div className="empty-state">
        暂无记录。创建一条记录或等待投影事件到达。
      </div>
    );
  return (
    <>
      <div className="column-visibility">
        <span className="muted">列显隐</span>
        {availableColumns.map((column) => (
          <label className="column-option" key={column}>
            <input
              type="checkbox"
              checked={!hiddenColumns.includes(column)}
              onChange={() =>
                setHiddenColumns((current) =>
                  current.includes(column)
                    ? current.filter((item) => item !== column)
                    : [...current, column],
                )
              }
            />
            <code>{column}</code>
          </label>
        ))}
      </div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>ID</th>
              {columns.map((column) => (
                <th key={column}>{column}</th>
              ))}
              <th>标签</th>
              <th>关系</th>
              <th>版本</th>
              <th>投影状态</th>
              <th>更新时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {records.map((record) => (
              <tr key={record.id}>
                <td>
                  <code>{record.id}</code>
                  {record.source && (
                    <small>
                      {record.source.system} / {record.source.type}
                    </small>
                  )}
                </td>
                {columns.map((column) => (
                  <td key={column}>
                    <RecordCell
                      value={readPath(record.data, column)}
                      type={fieldTypes.get(
                        column.replace(/^data\./, "").split(".")[0],
                      )}
                    />
                  </td>
                ))}
                <td>
                  {record.tags.map((tag) => (
                    <span className="tag" key={tag}>
                      {tag}
                    </span>
                  ))}
                </td>
                <td>
                  {record.relations?.length ? (
                    <details>
                      <summary>{record.relations.length} 条 typed relation</summary>
                      {record.relations.map((relation, index) => (
                        <small key={`${relation.relationType}-${index}`}>
                          <code>{relation.relationType}</code> → {relation.target.system}/
                          {relation.target.type}/{relation.target.id} ({relation.status})
                        </small>
                      ))}
                    </details>
                  ) : (
                    <span className="muted">—</span>
                  )}
                </td>
                <td>{record.recordVersion}</td>
                <td>
                  <span
                    className={`status ${record.projection?.status === "GAP" ? "bad" : ""}`}
                  >
                    {record.projection?.status ?? "CUSTOM"}
                  </span>
                  {record.projection?.syncedAt && (
                    <small>
                      {new Date(record.projection.syncedAt).toLocaleString()}
                    </small>
                  )}
                </td>
                <td>{new Date(record.updatedAt).toLocaleString()}</td>
                <td>
                  <div className="button-row">
                    <button
                      className="ghost-button"
                      onClick={() => onEdit(record)}
                    >
                      编辑
                    </button>
                    <button
                      className="ghost-button danger-button"
                      onClick={() => onDelete(record)}
                    >
                      删除
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function RecordCell({
  value,
  type,
}: {
  value: unknown;
  type?: SchemaField["type"];
}) {
  if (value === undefined || value === null)
    return <span className="cell-value muted">—</span>;
  if (type === "boolean" && typeof value === "boolean") {
    return (
      <span className={`status ${value ? "good" : ""}`}>
        {value ? "是" : "否"}
      </span>
    );
  }
  if ((type === "number" || type === "integer") && typeof value === "number") {
    return <span className="cell-value numeric">{value.toLocaleString()}</span>;
  }
  if (type === "date-time" && typeof value === "string") {
    const timestamp = Date.parse(value);
    return Number.isNaN(timestamp) ? (
      <span className="cell-value bad-value">{value}</span>
    ) : (
      <time className="cell-value" dateTime={value}>
        {new Date(timestamp).toLocaleString()}
      </time>
    );
  }
  if (type === "enum") return <span className="tag">{formatCell(value)}</span>;
  if (type === "reference" && value && typeof value === "object") {
    const reference = value as Record<string, unknown>;
    if (
      typeof reference.system === "string" &&
      typeof reference.type === "string" &&
      typeof reference.id === "string"
    ) {
      return (
        <code className="reference-value">
          {reference.system}/{reference.type}/{reference.id}
        </code>
      );
    }
  }
  return <span className="cell-value">{formatCell(value)}</span>;
}

function readPath(data: Record<string, unknown>, path: string): unknown {
  const parts = path
    .replace(/^data\./, "")
    .split(".")
    .filter(Boolean);
  return parts.reduce<unknown>(
    (current, part) =>
      current && typeof current === "object"
        ? (current as Record<string, unknown>)[part]
        : undefined,
    data,
  );
}

function formatCell(value: unknown): string {
  if (value === undefined || value === null) return "—";
  if (
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean"
  )
    return String(value);
  return JSON.stringify(value);
}

function SchemaPanel(props: {
  tenantId: string;
  workspaceId: string;
  schema: SchemaDefinition | null;
  schemas: SchemaSummary[];
  form: {
    id: string;
    name: string;
    version: string;
    data: string;
    semanticTypes: string;
  };
  setForm: (value: {
    id: string;
    name: string;
    version: string;
    data: string;
    semanticTypes: string;
  }) => void;
  onCreate: (event: FormEvent) => void;
  onUpdate: (event: FormEvent) => void;
  onLoad: () => void;
  onSelect: (schema: SchemaSummary) => void;
  onReset: () => void;
  onPublish: () => void;
}) {
  const {
    workspaceId,
    schema,
    schemas,
    form,
    setForm,
    onCreate,
    onUpdate,
    onLoad,
    onSelect,
    onReset,
    onPublish,
  } = props;
  const [compatibility, setCompatibility] = useState<SchemaCompatibilityReport | null>(null);
  const [compatibilityError, setCompatibilityError] = useState("");
  const [compatibilityBusy, setCompatibilityBusy] = useState(false);
  const fieldResult = useMemo(() => parseSchemaFields(form.data), [form.data]);
  const updateFields = (fields: SchemaField[]) =>
    setForm({ ...form, data: writeSchemaFields(form.data, fields) });

  async function compareCompatibility() {
    if (!schema) return;
    setCompatibilityBusy(true);
    setCompatibilityError("");
    try {
      const candidate = JSON.parse(form.data) as Record<string, unknown>;
      const response = await api.compareSchemaCompatibility(
        schema.schemaId,
        props.tenantId,
        props.workspaceId,
        schema.version,
        candidate,
      );
      setCompatibility(response.body);
    } catch (caught) {
      setCompatibility(null);
      setCompatibilityError(
        caught instanceof ApiError ? caught.message : "候选 Schema 不是有效 JSON",
      );
    } finally {
      setCompatibilityBusy(false);
    }
  }
  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h2>Schema 管理</h2>
          <p className="muted">
            以版本和 ETag 控制并发；发布后表格只能引用已发布版本。
          </p>
        </div>
        {schema && (
          <span
            className={`status ${schema.status === "PUBLISHED" ? "good" : ""}`}
          >
            {schema.status} · revision {schema.revision}
          </span>
        )}
      </div>
      <div className="schema-list" aria-label="Schema 版本列表">
        {schemas.map((item) => (
          <button
            type="button"
            className={`schema-list-item ${schema?.schemaId === item.schemaId && schema.version === item.version ? "selected" : ""}`}
            key={`${item.schemaId}:${item.version}`}
            onClick={() => onSelect(item)}
          >
            <span>
              <strong>{item.name}</strong>
              <code>{item.schemaId}</code>
            </span>
            <span>
              v{item.version} · {item.status}
            </span>
          </button>
        ))}
        {schemas.length === 0 && (
          <p className="muted">当前工作区还没有 Schema 版本。</p>
        )}
      </div>
      <div className="load-row">
        <label>
          读取已有版本
          <input
            placeholder="schema id"
            value={form.id}
            disabled={Boolean(schema)}
            onChange={(event) => setForm({ ...form, id: event.target.value })}
          />
        </label>
        <label>
          版本
          <input
            type="number"
            min="1"
            value={form.version}
            disabled={Boolean(schema)}
            onChange={(event) =>
              setForm({ ...form, version: event.target.value })
            }
          />
        </label>
        <button
          type="button"
          className="secondary-button"
          disabled={!workspaceId || !form.id}
          onClick={onLoad}
        >
          读取
        </button>
        {schema && (
          <button type="button" className="ghost-button" onClick={onReset}>
            新建草稿
          </button>
        )}
      </div>
      <form className="schema-form" onSubmit={schema ? onUpdate : onCreate}>
        <div className="form-grid">
          <label>
            名称
            <input
              required
              value={form.name}
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
            />
          </label>
          <label>
            版本
            <input
              required
              type="number"
              min="1"
              value={form.version}
              onChange={(event) =>
                setForm({ ...form, version: event.target.value })
              }
            />
          </label>
          <label>
            semantic types
            <input
              placeholder="schema.org/Thing, ..."
              value={form.semanticTypes}
              onChange={(event) =>
                setForm({ ...form, semanticTypes: event.target.value })
              }
            />
          </label>
        </div>
        <label>
          JSON Schema
          <textarea
            className="code-input"
            required
            rows={16}
            value={form.data}
            onChange={(event) => setForm({ ...form, data: event.target.value })}
          />
        </label>
        <section className="schema-fields">
          <div className="subheading">
            <div>
              <h3>字段编辑器</h3>
              <span className="muted">
                快速维护顶层 properties；复杂约束仍可在 JSON 编辑器中调整。
              </span>
            </div>
            <button
              type="button"
              className="secondary-button"
              disabled={Boolean(fieldResult.error)}
              onClick={() =>
                updateFields([
                  ...fieldResult.fields,
                  {
                    name: `field_${fieldResult.fields.length + 1}`,
                    type: "string",
                    required: false,
                    enumValues: "",
                  },
                ])
              }
            >
              添加字段
            </button>
          </div>
          {fieldResult.error ? (
            <p className="field-error">{fieldResult.error}</p>
          ) : fieldResult.fields.length === 0 ? (
            <p className="muted">暂无顶层字段，点击“添加字段”开始建模。</p>
          ) : (
            <div className="schema-field-list">
              {fieldResult.fields.map((field, index) => (
                <div
                  className="schema-field-row"
                  key={`${field.name}-${index}`}
                >
                  <input
                    aria-label={`字段 ${index + 1} 名称`}
                    value={field.name}
                    onChange={(event) => {
                      const next = [...fieldResult.fields];
                      next[index] = { ...field, name: event.target.value };
                      updateFields(next);
                    }}
                  />
                  <select
                    aria-label={`字段 ${index + 1} 类型`}
                    value={field.type}
                    onChange={(event) => {
                      const next = [...fieldResult.fields];
                      next[index] = {
                        ...field,
                        type: event.target.value as SchemaFieldType,
                      };
                      updateFields(next);
                    }}
                  >
                    <option value="string">文本</option>
                    <option value="number">数字</option>
                    <option value="boolean">布尔</option>
                    <option value="date-time">日期时间</option>
                    <option value="enum">枚举</option>
                    <option value="reference">引用</option>
                    <option value="integer">整数（高级）</option>
                    <option value="object">对象（高级）</option>
                    <option value="array">数组（高级）</option>
                  </select>
                  <label className="required-field">
                    <input
                      type="checkbox"
                      checked={field.required}
                      onChange={(event) => {
                        const next = [...fieldResult.fields];
                        next[index] = {
                          ...field,
                          required: event.target.checked,
                        };
                        updateFields(next);
                      }}
                    />{" "}
                    必填
                  </label>
                  <button
                    type="button"
                    className="ghost-button danger-button"
                    onClick={() =>
                      updateFields(
                        fieldResult.fields.filter(
                          (_, candidate) => candidate !== index,
                        ),
                      )
                    }
                  >
                    移除
                  </button>
                  {field.type === "enum" && (
                    <input
                      className="schema-field-detail"
                      aria-label={`字段 ${index + 1} 枚举值`}
                      placeholder="枚举值（逗号分隔，也可填 JSON 数组）"
                      value={field.enumValues}
                      onChange={(event) => {
                        const next = [...fieldResult.fields];
                        next[index] = {
                          ...field,
                          enumValues: event.target.value,
                        };
                        updateFields(next);
                      }}
                    />
                  )}
                  {field.type === "reference" && (
                    <span className="schema-field-detail muted">
                      typed reference：system / type / id（可在 JSON
                      编辑器中调整约束）
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}
      </section>
      {schema && schema.status === "PUBLISHED" && (
        <section className="schema-fields">
          <div className="subheading">
            <div>
              <h3>兼容性预览</h3>
              <span className="muted">
                将当前编辑器内容与已发布版本 v{schema.version} 比较，不会保存候选版本。
              </span>
            </div>
            <button
              type="button"
              className="secondary-button"
              disabled={compatibilityBusy}
              onClick={() => void compareCompatibility()}
            >
              {compatibilityBusy ? "检查中…" : "检查兼容性"}
            </button>
          </div>
          {compatibilityError && <p className="field-error">{compatibilityError}</p>}
          {compatibility && (
            <div className="compatibility-result">
              <span className={`status ${compatibility.compatible ? "good" : "bad"}`}>
                {compatibility.compatible ? "兼容" : "存在 breaking change"}
              </span>
              {compatibility.changes.length > 0 && (
                <ul>
                  {compatibility.changes.map((change) => (
                    <li key={`${change.path}:${change.kind}`}>
                      <code>{change.path}</code> · {change.kind} · {change.message}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </section>
      )}
      <div className="button-row">
          <button
            className="primary-button"
            disabled={!workspaceId || !form.id}
          >
            {schema ? "保存草稿" : "创建草稿"}
          </button>
          {schema && schema.status !== "PUBLISHED" && (
            <button
              type="button"
              className="secondary-button"
              onClick={onPublish}
            >
              发布版本
            </button>
          )}
        </div>
      </form>
      {schema && (
        <p className="muted">
          最新版本：{schema.name} v{schema.version}，更新于{" "}
          {new Date(schema.updatedAt).toLocaleString()}。
        </p>
      )}
    </div>
  );
}

function OperationsPanel(props: {
  snapshot: OperationsSnapshot | null;
  consumer: string;
  setConsumer: (value: string) => void;
  onRefresh: () => void;
}) {
  const { snapshot, consumer, setConsumer, onRefresh } = props;
  const freshness = snapshot ? freshnessLabel(snapshot.generatedAt) : null;
  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h2>投影运维</h2>
          <p className="muted">
            这里只展示计数、checkpoint 和 freshness，不展示原始事件 payload。
          </p>
        </div>
        <div className="button-row">
          <select
            value={consumer}
            onChange={(event) => setConsumer(event.target.value)}
          >
            {consumerOptions.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
          <button className="secondary-button" onClick={onRefresh}>
            刷新
          </button>
        </div>
      </div>
      {snapshot ? (
        <>
          <span
            className={`status ${freshness?.tone === "good" ? "good" : freshness?.tone === "bad" ? "bad" : ""}`}
          >
            {freshness?.label}
          </span>
          <div className="metric-grid">
            <Metric label="处理中" value={snapshot.inbox.processing} />
            <Metric label="已应用" value={snapshot.inbox.applied} />
            <Metric
              label="拒绝"
              value={snapshot.inbox.rejected}
              bad={snapshot.inbox.rejected > 0}
            />
            <Metric
              label="失败"
              value={snapshot.inbox.failed}
              bad={snapshot.inbox.failed > 0}
            />
          </div>
          <div className="subheading">
            <h3>Checkpoints</h3>
            <span className="muted">
              生成于 {new Date(snapshot.generatedAt).toLocaleString()}
            </span>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>来源</th>
                  <th>聚合</th>
                  <th>版本</th>
                  <th>状态</th>
                  <th>同步时间</th>
                </tr>
              </thead>
              <tbody>
                {snapshot.checkpoints.map((checkpoint) => (
                  <tr
                    key={`${checkpoint.sourceSystem}-${checkpoint.aggregateType}-${checkpoint.aggregateId}`}
                  >
                    <td>{checkpoint.sourceSystem}</td>
                    <td>
                      <code>
                        {checkpoint.aggregateType}/{checkpoint.aggregateId}
                      </code>
                    </td>
                    <td>{checkpoint.sourceVersion}</td>
                    <td>
                      <span
                        className={`status ${checkpoint.status === "GAP" ? "bad" : "good"}`}
                      >
                        {checkpoint.status}
                      </span>
                    </td>
                    <td>{new Date(checkpoint.syncedAt).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : (
        <div className="empty-state">选择一个投影并点击刷新。</div>
      )}
    </div>
  );
}

function freshnessLabel(generatedAt: string): {
  label: string;
  tone: "good" | "warn" | "bad";
} {
  const age = Math.max(0, Date.now() - new Date(generatedAt).getTime());
  if (age < 30_000) return { label: "Fresh · <30s", tone: "good" };
  if (age < 120_000) return { label: "Lagging · <2m", tone: "warn" };
  return { label: "Stale · ≥2m", tone: "bad" };
}

function Metric({
  label,
  value,
  bad,
}: {
  label: string;
  value: number;
  bad?: boolean;
}) {
  return (
    <div className={`metric ${bad ? "metric-bad" : ""}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
