import { describe, expect, it } from "vitest";
import {
  parseSchemaFields,
  SchemaField,
  writeSchemaFields,
} from "./schema-fields";

const sixFieldSchema = JSON.stringify({
  $schema: "https://json-schema.org/draft/2020-12/schema",
  type: "object",
  required: ["text", "number", "enabled", "occurredAt", "state", "reference"],
  properties: {
    text: { type: "string", minLength: 1 },
    number: { type: "number" },
    enabled: { type: "boolean" },
    occurredAt: { type: "string", format: "date-time" },
    state: { type: "string", enum: ["OPEN", "CLOSED"] },
    reference: {
      type: "object",
      additionalProperties: false,
      required: ["system", "type", "id"],
      properties: {
        system: { type: "string" },
        type: { type: "string" },
        id: { type: "string" },
      },
    },
  },
});

describe("schema field editor model", () => {
  it("recognizes the six MVP field types", () => {
    const result = parseSchemaFields(sixFieldSchema);
    expect(result.error).toBeUndefined();
    expect(result.fields.map((field) => field.type)).toEqual([
      "string",
      "number",
      "boolean",
      "date-time",
      "enum",
      "reference",
    ]);
    expect(
      result.fields.find((field) => field.type === "enum")?.enumValues,
    ).toBe("OPEN, CLOSED");
    expect(result.fields.every((field) => field.required)).toBe(true);
  });

  it("writes date-time, enum and typed reference constraints", () => {
    const fields: SchemaField[] = [
      { name: "occurredAt", type: "date-time", required: true, enumValues: "" },
      {
        name: "state",
        type: "enum",
        required: false,
        enumValues: '["OPEN", "CLOSED"]',
      },
      { name: "reference", type: "reference", required: true, enumValues: "" },
    ];
    const document = JSON.parse(
      writeSchemaFields('{"type":"object"}', fields),
    ) as {
      properties: Record<string, Record<string, unknown>>;
      required: string[];
    };
    expect(document.properties.occurredAt).toMatchObject({
      type: "string",
      format: "date-time",
    });
    expect(document.properties.state).toMatchObject({
      type: "string",
      enum: ["OPEN", "CLOSED"],
    });
    expect(document.properties.reference).toMatchObject({
      type: "object",
      additionalProperties: false,
      required: ["system", "type", "id"],
    });
    expect(document.required).toEqual(["occurredAt", "reference"]);
  });

  it("reports malformed JSON without mutating it", () => {
    expect(parseSchemaFields("{").error).toBeTruthy();
    expect(writeSchemaFields("{", [])).toBe("{");
  });
});
