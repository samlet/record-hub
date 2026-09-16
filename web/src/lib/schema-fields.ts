export type SchemaFieldType =
  | "string"
  | "number"
  | "boolean"
  | "date-time"
  | "enum"
  | "reference"
  | "integer"
  | "object"
  | "array";

export type SchemaField = {
  name: string;
  type: SchemaFieldType;
  required: boolean;
  enumValues: string;
};

type SchemaObject = Record<string, unknown>;

const referenceProperties: SchemaObject = {
  system: {
    type: "string",
    enum: ["approver", "fluxion", "bids", "record-hub"],
  },
  type: { type: "string", pattern: "^[A-Z][A-Z0-9_]{0,63}$" },
  id: { type: "string", format: "uuid" },
};

function asObject(value: unknown): SchemaObject | undefined {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as SchemaObject)
    : undefined;
}

function isReferenceSchema(value: SchemaObject): boolean {
  const properties = asObject(value.properties);
  const required = Array.isArray(value.required) ? value.required : [];
  return (
    value.type === "object" &&
    properties !== undefined &&
    ["system", "type", "id"].every((field) => field in properties) &&
    ["system", "type", "id"].every((field) => required.includes(field))
  );
}

function enumValuesFromSchema(value: SchemaObject): string {
  if (!Array.isArray(value.enum)) return "";
  return value.enum
    .map((item) => (typeof item === "string" ? item : JSON.stringify(item)))
    .join(", ");
}

function parseEnumValues(raw: string): unknown[] {
  const trimmed = raw.trim();
  if (!trimmed) return [];
  if (trimmed.startsWith("[")) {
    try {
      const parsed = JSON.parse(trimmed);
      if (Array.isArray(parsed)) return parsed;
    } catch {
      // Fall back to comma-separated values so the editor remains usable.
    }
  }
  return trimmed
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => {
      try {
        return JSON.parse(item);
      } catch {
        return item;
      }
    });
}

function fieldType(value: SchemaObject): SchemaFieldType {
  if (isReferenceSchema(value)) return "reference";
  if (value.type === "string" && value.format === "date-time")
    return "date-time";
  if (value.type === "string" && Array.isArray(value.enum)) return "enum";
  if (
    value.type === "string" ||
    value.type === "number" ||
    value.type === "boolean" ||
    value.type === "integer" ||
    value.type === "object" ||
    value.type === "array"
  ) {
    return value.type;
  }
  return "string";
}

function jsonErrorMessage(raw: string, error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  const match = message.match(/position\s+(\d+)/i);
  let position = match ? Number(match[1]) : undefined;
  if (position === undefined) {
    const token = message.match(/Unexpected token '([^']+)'/i)?.[1];
    if (token) {
      const candidate = raw.lastIndexOf(token);
      if (candidate >= 0) position = candidate;
    } else if (/Unexpected end/i.test(message)) {
      position = raw.length;
    }
  }
  if (position === undefined) return `JSON 格式错误：${message}`;
  const prefix = raw.slice(0, position);
  const lines = prefix.split("\n");
  return `JSON 第 ${lines.length} 行、第 ${lines[lines.length - 1].length + 1} 列附近有错误：${message}`;
}

export function parseSchemaFields(raw: string): {
  fields: SchemaField[];
  error?: string;
} {
  try {
    const document = JSON.parse(raw) as SchemaObject;
    if (!document || typeof document !== "object" || Array.isArray(document)) {
      return { fields: [], error: "Schema 根节点必须是对象" };
    }
    const properties = document.properties;
    if (properties === undefined) return { fields: [] };
    if (
      !properties ||
      typeof properties !== "object" ||
      Array.isArray(properties)
    ) {
      return { fields: [], error: "properties 必须是对象" };
    }
    const required = new Set(
      Array.isArray(document.required)
        ? document.required.filter(
            (item): item is string => typeof item === "string",
          )
        : [],
    );
    return {
      fields: Object.entries(properties).map(([name, rawValue]) => {
        const value = asObject(rawValue) ?? {};
        return {
          name,
          type: fieldType(value),
          required: required.has(name),
          enumValues: enumValuesFromSchema(value),
        };
      }),
    };
  } catch (error) {
    return { fields: [], error: jsonErrorMessage(raw, error) };
  }
}

export function writeSchemaFields(raw: string, fields: SchemaField[]): string {
  let document: SchemaObject;
  try {
    const parsed = JSON.parse(raw) as unknown;
    const object = asObject(parsed);
    if (!object) return raw;
    document = { ...object };
  } catch {
    return raw;
  }
  const current = asObject(document.properties) ?? {};
  const properties: SchemaObject = {};
  for (const field of fields) {
    const existing = asObject(current[field.name]) ?? {};
    const next: SchemaObject = { ...existing };
    if (field.type === "date-time") {
      next.type = "string";
      next.format = "date-time";
      delete next.enum;
    } else if (field.type === "enum") {
      next.type = "string";
      delete next.format;
      const values = parseEnumValues(field.enumValues);
      if (values.length > 0) next.enum = values;
      else delete next.enum;
    } else if (field.type === "reference") {
      next.type = "object";
      next.additionalProperties = false;
      next.required = ["system", "type", "id"];
      next.properties = referenceProperties;
    } else {
      next.type = field.type;
      if (existing.type === "string" && existing.format === "date-time")
        delete next.format;
      if (existing.type === "string" && Array.isArray(existing.enum))
        delete next.enum;
      if (isReferenceSchema(existing)) {
        delete next.additionalProperties;
        delete next.required;
        delete next.properties;
      }
    }
    properties[field.name] = next;
  }
  document.properties = properties;
  const required = fields
    .filter((field) => field.required)
    .map((field) => field.name);
  if (required.length > 0) document.required = required;
  else delete document.required;
  return JSON.stringify(document, null, 2);
}
