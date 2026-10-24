import { describe, expect, it } from "vitest";
import {
  backlinks,
  decodeKnowledgeFile,
  graphData,
  guardKnowledgeHistory,
  parseModelProposal,
  renderKnowledgeMarkdown,
  searchKnowledge,
  wikiLinks,
} from "./knowledge";
import type {
  KnowledgeLibrary,
  KnowledgePage,
  KnowledgeSource,
} from "./api/knowledge";
const source: KnowledgeSource = {
  id: "s1",
  title: "Fictional source",
  filename: "sample.md",
  content: "Fictional notes",
  sha256: "hash",
  status: "active",
  created: "2026-01-01",
};
const page = (id: string, title: string, content: string): KnowledgePage => ({
  id,
  title,
  content,
  sourceIds: ["s1"],
  version: 2,
  updated: "2026-01-01",
  history: [],
  stale: false,
});
const library: KnowledgeLibrary = {
  id: "lib1",
  name: "Fictional library",
  description: "",
  config: {
    model: "",
    modelSource: "local",
    clawRead: false,
    clawDraft: false,
  },
  sources: [source],
  pages: [
    page("p1", "Alpha", "[[Beta#Heading|Read Beta]] [source:s1]"),
    page("p2", "Beta", "Fictional content"),
  ],
  proposals: [],
  logs: [],
};
describe("native knowledge workspace", () => {
  it("preserves UTF-8 BOM, CRLF, trailing spaces and rejects invalid bytes", () => {
    const raw = "\uFEFFOriginal\r\n  Keep spaces  \r\n";
    const bytes = new TextEncoder().encode(raw);
    expect(decodeKnowledgeFile(bytes.buffer)).toBe(raw);
    expect(() =>
      decodeKnowledgeFile(new Uint8Array([0xff, 0xfe]).buffer),
    ).toThrow();
    expect(() => decodeKnowledgeFile(new Uint8Array([65, 0]).buffer)).toThrow();
  });
  it("links headings and aliases, but not code, escaped brackets or nested Markdown links", () => {
    const text =
      "[[Beta#Heading|Read]] \\[[Alpha]] `[[Alpha]]`\n\n~~~md\n[[Alpha]]\n~~~\n\n[Link [[Alpha]]](https://example.com)";
    expect(wikiLinks(text)).toEqual(["Beta"]);
    const html = renderKnowledgeMarkdown(text, library);
    expect((html.match(/data-page=/g) || []).length).toBe(1);
    expect(html).toContain('data-page="p2">Read</button>');
    expect(html).toContain("[[Alpha]]");
  });
  it("renders safe titles, source citations and untrusted Markdown without executable HTML", () => {
    const hostile = {
      ...library,
      sources: [{ ...source, title: '<img src=x onerror="alert(1)">' }],
    };
    const html = renderKnowledgeMarkdown(
      "[source:s1] <script>alert(1)</script> [[Beta|<img src=x onerror=alert(2)>]] [x](javascript:alert(3))",
      hostile,
    );
    expect(html).toContain('data-source="s1"');
    expect(html).not.toContain("<script>");
    expect(html).not.toContain("<img");
    expect(html).not.toContain('href="javascript:');
  });
  it("derives graph and backlinks from current library, excluding stale/withdrawn items", () => {
    expect(backlinks(library.pages[1], library.pages).map((p) => p.id)).toEqual(
      ["p1"],
    );
    expect(graphData(library, false).edges).toEqual([
      { from: "page:p1", to: "page:p2" },
    ]);
    expect(graphData(library, true).edges).toHaveLength(3);
    const stale = {
      ...library,
      pages: library.pages.map((p) => ({ ...p, stale: true })),
      sources: [{ ...source, status: "withdrawn" as const }],
    };
    expect(graphData(stale, true)).toEqual({ nodes: [], edges: [] });
    expect(searchKnowledge(stale, "")).toEqual([]);
  });
  it("searches content case-insensitively without accessing other libraries", () => {
    expect(searchKnowledge(library, "FICTIONAL").map((r) => r.key)).toEqual([
      "page:p2",
      "source:s1",
    ]);
    expect(
      searchKnowledge({ ...library, pages: [], sources: [] }, "Alpha"),
    ).toEqual([]);
  });
  it("accepts model JSON only with supported citations and captures actual base versions", () => {
    const output =
      '```json\n{"changes":[{"pageId":"p1","title":"Alpha","content":"Updated fact [source:s1]"}]}\n```';
    expect(parseModelProposal(output, library, source)).toEqual([
      {
        pageId: "p1",
        title: "Alpha",
        content: "Updated fact [source:s1]",
        sourceIds: ["s1"],
        baseVersion: 2,
      },
    ]);
  });
  it("rejects malformed model output, invented targets, duplicate pages and foreign/missing citations", () => {
    for (const item of [
      "prose",
      "{}",
      JSON.stringify({ title: "X", content: "No citation" }),
      JSON.stringify({ pageId: "foreign", title: "X", content: "[source:s1]" }),
      JSON.stringify({ title: "X", content: "[source:s1] [source:foreign]" }),
      JSON.stringify([
        { title: "X", content: "[source:s1]" },
        { title: "X", content: "[source:s1]" },
      ]),
    ])
      expect(() => parseModelProposal(item, library, source)).toThrow();
  });
  it("rejects model citations to withdrawn source", () => {
    expect(() =>
      parseModelProposal(
        JSON.stringify({ title: "X", content: "[source:s1]" }),
        { ...library, sources: [{ ...source, status: "withdrawn" }] },
        source,
      ),
    ).toThrow();
  });
});

describe("browser history draft protection", () => {
  it("declining Back preserves the editor by blocking the router and restoring its URL", () => {
    const actions: string[] = [];
    const event = {
      stopImmediatePropagation: () => actions.push("router blocked"),
    };
    expect(
      guardKnowledgeHistory(event, {
        dirty: true,
        pending: false,
        confirmLeave: () => {
          actions.push("confirm");
          return false;
        },
        restoreLocation: () => actions.push("knowledge URL restored"),
      }),
    ).toBe(true);
    expect(actions).toEqual([
      "confirm",
      "router blocked",
      "knowledge URL restored",
    ]);
  });
  it("allows confirmed or clean navigation, but never interrupts pending writes", () => {
    let blocked = 0,
      restored = 0,
      confirmed = 0;
    const event = {
      stopImmediatePropagation: () => {
        blocked++;
      },
    };
    const options = {
      dirty: true,
      pending: false,
      confirmLeave: () => {
        confirmed++;
        return true;
      },
      restoreLocation: () => {
        restored++;
      },
    };
    expect(guardKnowledgeHistory(event, options)).toBe(false);
    expect(guardKnowledgeHistory(event, { ...options, dirty: false })).toBe(
      false,
    );
    expect(guardKnowledgeHistory(event, { ...options, pending: true })).toBe(
      true,
    );
    expect({ blocked, restored, confirmed }).toEqual({
      blocked: 1,
      restored: 1,
      confirmed: 1,
    });
  });
});
