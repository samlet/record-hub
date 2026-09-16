"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";
import {
  api,
  ApiError,
  items,
  OperationsSnapshot,
  RecordItem,
  SchemaDefinition,
  TableDefinition,
  Workspace,
} from "../lib/api";

type Tab = "records" | "schema" | "operations";
const consumerOptions = [
  ["record-hub-approver-projection-v1", "Approver"],
  ["record-hub-fluxion-projection-v1", "Fluxion"],
  ["record-hub-bids-projection-v1", "Bids"],
];

const initialSchema =
  '{\n  "$schema": "https://json-schema.org/draft/2020-12/schema",\n  "type": "object",\n  "properties": {},\n  "additionalProperties": true\n}';

export function Console() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [tenantId, setTenantId] = useState("tenant-local");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceId, setWorkspaceId] = useState("");
  const [tables, setTables] = useState<TableDefinition[]>([]);
  const [tableId, setTableId] = useState("");
  const [records, setRecords] = useState<RecordItem[]>([]);
  const [tab, setTab] = useState<Tab>("records");
  const [consumer, setConsumer] = useState(consumerOptions[0][0]);
  const [operations, setOperations] = useState<OperationsSnapshot | null>(null);
  const [schema, setSchema] = useState<SchemaDefinition | null>(null);
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
        items((await api.records(tenantId, workspaceId, tableId)).body),
      ),
    );
  }, [tenantId, workspaceId, tableId, authenticated, tab]);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (caught) {
      setError(
        caught instanceof ApiError
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
          </nav>
          {tab === "records" && (
            <RecordsPanel
              tenantId={tenantId}
              workspaceId={workspaceId}
              tableId={tableId}
              tables={tables}
              selectedTable={selectedTable}
              records={records}
              tableForm={tableForm}
              setTableForm={setTableForm}
              recordForm={recordForm}
              setRecordForm={setRecordForm}
              onTableChange={setTableId}
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
                    data: JSON.parse(recordForm.data),
                  });
                  setRecords((current) => [created.body, ...current]);
                  setRecordForm({ id: "", tags: "", data: "{}" });
                })
              }
            />
          )}{" "}
          {tab === "schema" && (
            <SchemaPanel
              tenantId={tenantId}
              workspaceId={workspaceId}
              schema={schema}
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
                })
              }
              onLoad={() =>
                void run(async () => {
                  const loaded = await api.getSchema(
                    tenantId,
                    workspaceId,
                    schemaForm.id,
                    Number(schemaForm.version),
                  );
                  setSchema(loaded.body);
                  setSchemaForm({
                    ...schemaForm,
                    name: loaded.body.name,
                    version: String(loaded.body.version),
                    data: JSON.stringify(loaded.body.jsonSchema, null, 2),
                    semanticTypes: loaded.body.semanticTypes.join(", "),
                  });
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
        </div>
      </section>
    </main>
  );
}

function RecordsPanel(props: {
  tenantId: string;
  workspaceId: string;
  tableId: string;
  tables: TableDefinition[];
  selectedTable?: TableDefinition;
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
  recordForm: { id: string; tags: string; data: string };
  setRecordForm: (value: { id: string; tags: string; data: string }) => void;
  onTableChange: (value: string) => void;
  onCreateTable: (event: FormEvent) => void;
  onCreateRecord: (event: FormEvent) => void;
}) {
  const {
    tenantId,
    workspaceId,
    tableId,
    tables,
    selectedTable,
    records,
    tableForm,
    setTableForm,
    recordForm,
    setRecordForm,
    onTableChange,
    onCreateTable,
    onCreateRecord,
  } = props;
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
        <form className="inline-form" onSubmit={onCreateRecord}>
          <h3>添加记录</h3>
          <input
            required
            placeholder="record id"
            value={recordForm.id}
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
            required
            rows={3}
            value={recordForm.data}
            onChange={(event) =>
              setRecordForm({ ...recordForm, data: event.target.value })
            }
          />
          <button className="secondary-button" disabled={!tableId}>
            写入记录
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
          <RecordGrid records={records} />
        </>
      ) : (
        <div className="empty-state">请选择一个表格查看记录。</div>
      )}
    </div>
  );
}

function RecordGrid({ records }: { records: RecordItem[] }) {
  if (records.length === 0)
    return (
      <div className="empty-state">
        暂无记录。创建一条记录或等待投影事件到达。
      </div>
    );
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>数据</th>
            <th>标签</th>
            <th>版本</th>
            <th>投影状态</th>
            <th>更新时间</th>
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
              <td>
                <pre>{JSON.stringify(record.data, null, 2)}</pre>
              </td>
              <td>
                {record.tags.map((tag) => (
                  <span className="tag" key={tag}>
                    {tag}
                  </span>
                ))}
              </td>
              <td>{record.recordVersion}</td>
              <td>
                <span
                  className={`status ${record.projection?.status === "GAP" ? "bad" : ""}`}
                >
                  {record.projection?.status ?? "CUSTOM"}
                </span>
              </td>
              <td>{new Date(record.updatedAt).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SchemaPanel(props: {
  tenantId: string;
  workspaceId: string;
  schema: SchemaDefinition | null;
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
  onReset: () => void;
  onPublish: () => void;
}) {
  const {
    workspaceId,
    schema,
    form,
    setForm,
    onCreate,
    onUpdate,
    onLoad,
    onReset,
    onPublish,
  } = props;
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
        <div className="button-row">
          <button className="primary-button" disabled={!workspaceId || !form.id}>
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
