// Tests for reading dropped and chosen folders: `npm test`. Like
// engine.test.ts it loads the module with a dynamic import, because Node
// needs the ".ts" extension that the app's type check doesn't allow.
import type * as Tree from "./uploadTree";

type Assert = {
  equal(actual: unknown, expected: unknown, message?: string): void;
  deepEqual(actual: unknown, expected: unknown, message?: string): void;
  ok(value: unknown, message?: string): void;
  throws(fn: () => unknown, expected?: object): void;
  rejects(promise: Promise<unknown>, expected?: object): Promise<void>;
};

const load = (spec: string): Promise<unknown> => import(spec);
const { test } = (await load("node:test")) as { test: (name: string, fn: () => Promise<void> | void) => void };
const assert = ((await load("node:assert/strict")) as { default: Assert }).default;
const T = (await load("./uploadTree.ts")) as typeof Tree;

// The browser's entries, as far as the reader uses them.
function file(name: string): FileSystemEntry {
  const f = { name } as File;
  return { isFile: true, isDirectory: false, name, file: (ok: (f: File) => void) => ok(f) } as unknown as FileSystemEntry;
}

// dir answers its children in parts of two, then an empty part, like Chrome
// answers in parts of 100.
function dir(name: string, ...children: FileSystemEntry[]): FileSystemEntry {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => {
      let at = 0;
      return {
        readEntries: (ok: (e: FileSystemEntry[]) => void) => {
          const part = children.slice(at, at + 2);
          at += 2;
          ok(part);
        },
      };
    },
  } as unknown as FileSystemEntry;
}

const paths = (picked: Tree.Picked[]) => picked.map((p) => [...p.dir, p.file.name].join("/"));

test("a dropped folder keeps its subfolders", async () => {
  const picked = await T.readEntries([
    file("loose.txt"),
    dir("Photos", file("a.jpg"), dir("2024", file("b.jpg"), file("c.jpg"), file("d.jpg")), file("e.jpg"), dir("empty")),
  ]);
  assert.deepEqual(paths(picked), ["loose.txt", "Photos/a.jpg", "Photos/2024/b.jpg", "Photos/2024/c.jpg", "Photos/2024/d.jpg", "Photos/e.jpg"]);
  assert.deepEqual(picked[0].dir, []);
  assert.deepEqual(picked[3].dir, ["Photos", "2024"]);
});

test("folders may go as deep as the limit and no deeper", async () => {
  const nest = (levels: number): FileSystemEntry => {
    let e = file("deep.txt");
    for (let i = levels; i > 0; i--) e = dir(`d${i}`, e);
    return e;
  };
  assert.equal((await T.readEntries([nest(T.maxDepth)])).length, 1);
  await assert.rejects(T.readEntries([nest(T.maxDepth + 1)]), { kind: "depth" });
});

test("too many files are refused", async () => {
  const many = Array.from({ length: T.maxFiles + 1 }, (_, i) => file(`f${i}`));
  await assert.rejects(T.readEntries([dir("big", ...many)]), { kind: "files" });
  assert.equal((await T.readEntries([dir("ok", ...many.slice(1))])).length, T.maxFiles);
});

test("the folder picker's relative paths become folders", () => {
  const f = (name: string, rel: string) => ({ name, webkitRelativePath: rel }) as File;
  const picked = T.fromFolderInput([f("a.txt", "Docs/a.txt"), f("b.txt", "Docs/Sub/b.txt"), f("c.txt", "")]);
  assert.deepEqual(picked.map((p) => p.dir), [["Docs"], ["Docs", "Sub"], []]);
  assert.throws(() => T.fromFolderInput([f("x", Array.from({ length: T.maxDepth + 2 }, (_, i) => `d${i}`).join("/") + "/x")]), { kind: "depth" });
});
