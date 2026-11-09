// Runs the shared formula vectors (server/internal/sheet/testdata/formulas.json,
// also run by the Go package) against the browser engine: `npm test`.
// Node modules and engine.ts are loaded with dynamic imports: Node needs the
// ".ts" extension, while this file is also type-checked by the app project,
// which has neither Node types nor allowImportingTsExtensions.
import type * as Engine from "./engine";

type Vector = { name: string; valid?: boolean; cells: Record<string, string>; expect: Record<string, string> };
type Assert = {
  equal(actual: unknown, expected: unknown, message?: string): void;
  deepEqual(actual: unknown, expected: unknown, message?: string): void;
  ok(value: unknown, message?: string): void;
};

const load = (spec: string): Promise<unknown> => import(spec);
const { test } = (await load("node:test")) as { test: (name: string, fn: () => void) => void };
const assert = ((await load("node:assert/strict")) as { default: Assert }).default;
const { readFileSync } = (await load("node:fs")) as { readFileSync: (path: URL, encoding: "utf8") => string };
const E = (await load("./engine.ts")) as typeof Engine;

const vectors = JSON.parse(
  readFileSync(new URL("../../../server/internal/sheet/testdata/formulas.json", import.meta.url), "utf8"),
) as Vector[];

test("shared vectors are loaded", () => {
  assert.ok(vectors.length >= 60 && vectors.length <= 100, `${vectors.length} vectors`);
});

for (const v of vectors) {
  test(`vector: ${v.name}`, () => {
    const got = E.evaluate(v.cells);
    for (const [key, want] of Object.entries(v.expect)) assert.equal(got[key], want, `${key} of ${v.name}`);
    if (v.valid === false) {
      assert.equal(E.validate(v.cells.A1), false, "formula must be rejected");
    } else {
      for (const raw of Object.values(v.cells)) if (raw.startsWith("=")) assert.equal(E.validate(raw), true, raw.slice(0, 40));
    }
  });
}

test("colName and parseRef", () => {
  assert.deepEqual([0, 1, 25, 26, 51].map(E.colName), ["A", "B", "Z", "AA", "AZ"]);
  assert.equal(E.cellKey(6, 1), "B7");
  assert.deepEqual(E.parseRef("B7"), { row: 6, col: 1 });
  assert.deepEqual(E.parseRef("az1000"), { row: 999, col: 51 });
  for (const bad of ["", "A", "7", "A0", "A01", "A1001", "BA1", "AAA1", "$A$1", "A1 ", " A1", "A-1", "Ä1", "A1.5", "__proto__"]) {
    assert.equal(E.parseRef(bad), null, bad);
  }
});

test("formatNumber", () => {
  const cases: [number, string][] = [
    [0, "0"], [-0, "0"], [1, "1"], [-1.5, "-1.5"], [0.1 + 0.2, "0.3"], [1e15, "1E+15"],
    [123456789012345, "123456789012345"], [1e-9, "0.000000001"], [1.5e-10, "1.5E-10"], [1e100, "1E+100"],
    [-2.5e-300, "-2.5E-300"], [1e308, "1E+308"], [0.000123456789012345678, "0.000123456789012346"],
    [99999999999999.999, "100000000000000"], [5e-324, "5E-324"], [Infinity, "#ERR"], [NaN, "#ERR"],
  ];
  for (const [n, want] of cases) assert.equal(E.formatNumber(n), want, String(n));
});

test("inputs: keys, literals and refused values", () => {
  const got = E.evaluate({ b2: "=a1+1", A1: "1", Q0: "1", ZZ1: "", C1: "'", C2: "x".repeat(2001), C3: "a\u0000b", C4: "\ud800" });
  assert.deepEqual(got, { b2: "2", A1: "1", Q0: "#ERR", C2: "#ERR", C3: "#ERR", C4: "#ERR" });
});

test("a 1000-long chain in both directions", () => {
  const up: Record<string, string> = { A1: "1" };
  const down: Record<string, string> = { A1000: "1" };
  for (let r = 2; r <= 1000; r++) up[`A${r}`] = `=A${r - 1}+1`;
  for (let r = 999; r >= 1; r--) down[`A${r}`] = `=A${r + 1}+1`;
  assert.equal(E.evaluate(up).A1000, "1000");
  assert.equal(E.evaluate(down).A1, "1000");
});

test("a full 1000x52 sheet is fast", () => {
  const cells: Record<string, string> = {};
  for (let r = 1; r <= 1000; r++) {
    for (let c = 0; c < 52; c++) {
      const key = E.cellKey(r - 1, c);
      if (r === 1) cells[key] = c === 0 ? "1" : `=${E.colName(c - 1)}1+1`;
      else cells[key] = c % 13 === 0 ? `=SUM(${E.colName(c)}${r - 1}:${E.colName(c + 3)}${r - 1})/4+1` : `=${E.colName(c)}${r - 1}+1`;
    }
  }
  const t0 = performance.now();
  const got = E.evaluate(cells);
  const ms = performance.now() - t0;
  assert.equal(got.B1000, "1001");
  assert.equal(got.AZ1000, "1051");
  assert.ok(ms < 1000, `took ${ms.toFixed(0)} ms`);
});

test("too much range work makes every formula an error", () => {
  const cells: Record<string, string> = { A1: "1" };
  for (let r = 1; r <= 400; r++) cells[`B${r}`] = "=SUM(A1:AZ1000)";
  const got = E.evaluate(cells);
  assert.equal(got.A1, "1");
  assert.equal(got.B1, "#ERR");
});

test("the engine never evaluates code", () => {
  const src = readFileSync(new URL("./engine.ts", import.meta.url), "utf8");
  for (const banned of ["eval(", "Function(", "import(", "RegExp", "setTimeout", "document", "window"]) {
    assert.equal(src.includes(banned), false, banned);
  }
});
