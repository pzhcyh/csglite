import { describe, expect, it } from "vitest";
import {
  answerToKnowledgeChange,
  citedKnowledgeSources,
  createChatNavigationGuard,
  KnowledgeChatError,
  knowledgeChatURL,
  prepareKnowledgeContext,
} from "./knowledgeChat";
import type { KnowledgeLibrary, KnowledgePage } from "./api/knowledge";
const page = (
  id: string,
  content = "Fictional source fact [source:s1]",
): KnowledgePage => ({
  id,
  title: `Page ${id}`,
  content,
  sourceIds: ["s1"],
  version: 3,
  stale: false,
  history: [],
  updated: "2026-10-24",
});
const library: KnowledgeLibrary = {
  id: "l1",
  name: "Fictional library",
  description: "",
  config: {
    model: "",
    modelSource: "local",
    clawRead: false,
    clawDraft: false,
  },
  sources: [
    {
      id: "s1",
      title: "Fictional source",
      filename: "s1.md",
      content: "Facts",
      sha256: "hash1",
      status: "active",
      created: "2026-10-24",
    },
  ],
  pages: [page("p1"), page("p2", "Planning activity [source:s1]")],
  proposals: [],
  logs: [],
};
const selection = { library_id: "l1" };
describe("knowledge to native chat", () => {
  it("prepares only selected valid Wiki with provenance and no raw source text", () => {
    const result = prepareKnowledgeContext(
      library,
      { ...selection, page_ids: ["p2"] },
      "activity",
    );
    expect(result.snapshot.pages).toEqual([
      { id: "p2", title: "Page p2", version: 3, source_ids: ["s1"] },
    ]);
    expect(result.snapshot.sources).toEqual([
      { id: "s1", title: "Fictional source", sha256: "hash1" },
    ]);
    expect(result.system).toContain("untrusted data");
    expect(result.system).toContain("Planning activity");
    expect(result.system).not.toContain('"content":"Facts"');
    expect(library.config.clawRead).toBe(false);
  });
  it("rejects foreign libraries, missing pages, stale pages and withdrawn sources", () => {
    expect(() =>
      prepareKnowledgeContext(library, { library_id: "other" }, ""),
    ).toThrow(KnowledgeChatError);
    expect(() =>
      prepareKnowledgeContext(
        library,
        { ...selection, page_ids: ["foreign"] },
        "",
      ),
    ).toThrow("unavailablePage");
    expect(() =>
      prepareKnowledgeContext(
        { ...library, pages: [{ ...page("p1"), stale: true }] },
        { ...selection, page_ids: ["p1"] },
        "",
      ),
    ).toThrow("unavailablePage");
    expect(() =>
      prepareKnowledgeContext(
        {
          ...library,
          sources: library.sources.map((s) => ({
            ...s,
            status: "withdrawn" as const,
          })),
        },
        selection,
        "",
      ),
    ).toThrow("emptyContext");
  });
  it("bounds context to five pages and 24000 body characters, retaining exact snapshot versions", () => {
    const huge = {
      ...library,
      pages: Array.from({ length: 8 }, (_, i) =>
        page(`p${i}`, "x".repeat(10000)),
      ),
    };
    const result = prepareKnowledgeContext(huge, selection, "");
    expect(result.snapshot.pages.length).toBeLessThanOrEqual(5);
    expect(result.excerptCharacters).toBe(24000);
    expect(result.truncated).toBe(true);
    expect(result.snapshot.pages.every((p) => p.version === 3)).toBe(true);
    const many = prepareKnowledgeContext(
      {
        ...library,
        pages: Array.from({ length: 8 }, (_, i) => page(`p${i}`, "short")),
      },
      selection,
      "",
    );
    expect(many.snapshot.pages).toHaveLength(5);
  });
  it("ranks matching Wiki and interprets omitted or empty page list as library selection", () => {
    expect(
      prepareKnowledgeContext(library, selection, "activity").snapshot.pages[0]
        .id,
    ).toBe("p2");
    expect(
      prepareKnowledgeContext(library, { ...selection, page_ids: [] }, "")
        .snapshot.pages,
    ).toHaveLength(2);
  });
  it("turns cited real answer into a new pending-compatible change without inventing citations", () => {
    const { snapshot } = prepareKnowledgeContext(library, selection, "");
    const content = "Answer [source:s1]";
    expect(
      answerToKnowledgeChange(content, "My answer", snapshot, library),
    ).toEqual({
      pageId: "",
      title: "My answer",
      content,
      sourceIds: ["s1"],
      baseVersion: 0,
    });
    expect(citedKnowledgeSources("[source:s1] and [source:s1]")).toEqual([
      "s1",
    ]);
  });
  it("blocks no/foreign citations and changed or withdrawn sources before proposal creation", () => {
    const { snapshot } = prepareKnowledgeContext(library, selection, "");
    expect(() =>
      answerToKnowledgeChange("Uncited answer", "Title", snapshot, library),
    ).toThrow("noCitations");
    expect(() =>
      answerToKnowledgeChange("[source:foreign]", "Title", snapshot, library),
    ).toThrow("invalidCitation");
    expect(() =>
      answerToKnowledgeChange("[source:s1]", "Title", snapshot, {
        ...library,
        sources: library.sources.map((s) => ({ ...s, sha256: "changed" })),
      }),
    ).toThrow("changedSource");
    expect(() =>
      answerToKnowledgeChange("[source:s1]", "Title", snapshot, {
        ...library,
        sources: library.sources.map((s) => ({ ...s, status: "withdrawn" })),
      }),
    ).toThrow("changedSource");
  });
  it("builds encoded links without a prompt or automatic-send parameter", () => {
    const url = knowledgeChatURL("a&b", "p/x");
    expect(url).toBe("/chat?knowledge=a%26b&page=p%2Fx");
    expect(url).not.toMatch(/prompt|send/);
  });
});

it("retrieves Chinese relevant knowledge beyond the default first five pages", () => {
  const chinese = {
    ...library,
    pages: [
      ...Array.from({ length: 6 }, (_, index) => ({
        ...page(`other${index}`),
        title: "材料清单",
        content: "虚构材料记录 [source:s1]",
      })),
      {
        ...page("target"),
        title: "青松读书会",
        content: "青松读书会在西楼二层举行 [source:s1]",
      },
    ],
  };
  const result = prepareKnowledgeContext(
    chinese,
    selection,
    "请问青松读书会在哪里举行？",
  );
  expect(result.snapshot.pages[0].id).toBe("target");
});

it("does not activate a late conversation load after leaving Chat or selecting another conversation", async () => {
  const navigation = createChatNavigationGuard();
  let active = "initial";
  let completeOld!: () => void;
  const oldRequest = new Promise<void>((resolve) => {
    completeOld = resolve;
  });
  const oldIsCurrent = navigation.begin();
  const oldLoad = oldRequest.then(() => {
    if (oldIsCurrent()) active = "old history";
  });
  navigation.invalidate(); // Chat unmount
  navigation.invalidate(); // A later Chat mount
  const linkedIsCurrent = navigation.begin();
  if (linkedIsCurrent()) active = "new linked conversation";
  completeOld();
  await oldLoad;
  expect(active).toBe("new linked conversation");

  let completeCreate!: () => void;
  const createRequest = new Promise<void>((resolve) => {
    completeCreate = resolve;
  });
  const createIsCurrent = navigation.begin();
  const create = createRequest.then(() => {
    if (createIsCurrent()) active = "new ordinary conversation";
  });
  const selectedIsCurrent = navigation.begin();
  if (selectedIsCurrent()) active = "explicitly selected history";
  completeCreate();
  await create;
  expect(active).toBe("explicitly selected history");

  const pendingHistory = navigation.begin();
  navigation.invalidate(); // Explicit Send owns the currently active conversation.
  expect(pendingHistory()).toBe(false);
});
