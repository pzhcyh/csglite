import { describe, expect, it } from "vitest";
import type { KnowledgeLibrary, KnowledgeProposal } from "./api/knowledge";
import { openFreshKnowledgeProposal, selectKnowledgeProposal, backupFileLimit, serializeKnowledgeBackup, connectorConfiguration, createKnowledgeScope, parseKnowledgeBackup, stewardSelectionError } from "./knowledgeWorkflows";

const library: KnowledgeLibrary = {
  id: "fictional", name: "Fictional library", description: "",
  config: {model: "fictional-model", modelSource: "local", clawRead: false, clawDraft: false},
  sources: [{id: "source-1", title: "Fictional source", filename: "sample.md", content: "Fictional facts", sha256: "hash", status: "active", created: "2026-10-24"}],
  pages: [], proposals: [], logs: [],
};
describe("knowledge workflow lifecycle", () => {
  it("aborts requests and discards late responses when a library view unmounts", async () => {
    const oldScope = createKnowledgeScope();
    const oldRequest = oldScope.start();
    let resolve!: (value: string) => void;
    const delayed = new Promise<string>((done) => { resolve = done; });
    const values: string[] = [];
    const received = delayed.then((value) => { if (oldRequest.current()) values.push(value); });
    oldScope.dispose();
    const newScope = createKnowledgeScope();
    const newRequest = newScope.start();
    resolve("old-library-credential");
    await received;
    expect(oldRequest.signal.aborted).toBe(true);
    expect(values).toEqual([]);
    expect(newRequest.current()).toBe(true);
    expect(oldScope.start().current()).toBe(false);
    newScope.dispose();
  });
  it("invalidates completed callbacks as well as pending requests", () => {
    const scope = createKnowledgeScope();
    const request = scope.start();
    request.done();
    scope.dispose();
    expect(request.current()).toBe(false);
  });
});
describe("knowledge task scope", () => {
  it("requires explicit active source selection", () => {
    expect(stewardSelectionError(library, [])).toBe("selectionEmpty");
    expect(stewardSelectionError(library, ["foreign"])).toBe("selectionUnavailable");
    expect(stewardSelectionError({...library, sources: [{...library.sources[0], status: "withdrawn"}]}, ["source-1"])).toBe("selectionUnavailable");
    expect(stewardSelectionError(library, ["source-1"])).toBeNull();
  });
  it("enforces UTF-8 byte limits rather than string length", () => {
    const oversized = {...library, sources: [{...library.sources[0], content: "文".repeat(23000)}]};
    expect(stewardSelectionError(oversized, ["source-1"])).toBe("selectionSize");
    expect(stewardSelectionError(library, Array.from({length: 9}, (_, i) => `s${i}`))).toBe("selectionLimit");
  });
});
describe("connector and backup boundaries", () => {
  it("targets the selected preview port and puts the credential only in a header", () => {
    const config = JSON.parse(connectorConfiguration("http://localhost:11437", "fictional-test-token"));
    const server = config.mcpServers["csglite-knowledge"];
    expect(server.url).toBe("http://localhost:11437/api/knowledge/mcp");
    expect(server.url).not.toContain("token");
    expect(server.headers.Authorization).toBe("Bearer fictional-test-token");
    expect(() => connectorConfiguration("file:///tmp", "x")).toThrow();
  });
  it("rejects sharing snapshots and unknown versions before uploading a restore", () => {
    expect(() => parseKnowledgeBackup(JSON.stringify({library}))).toThrow();
    expect(() => parseKnowledgeBackup(JSON.stringify({format: "csglite-knowledge-backup", version: 2, sha256: "hash", library}))).toThrow();
    const backup = {format: "csglite-knowledge-backup", version: 1, sha256: "hash", library};
    expect(parseKnowledgeBackup(JSON.stringify(backup))).toEqual(backup);
  });
});


describe("backup download and restore size consistency", () => {
  it("uses compact JSON that can be read back without formatting expansion", () => {
    const backup = {format: "csglite-knowledge-backup", version: 1, sha256: "hash", library};
    const compact = serializeKnowledgeBackup(backup);
    expect(compact.length).toBeLessThan(JSON.stringify(backup, null, 2).length);
    expect(parseKnowledgeBackup(compact)).toEqual(backup);
  });
  it("measures the entire JSON envelope in UTF-8 bytes before permitting a download", () => {
    const exact = {text: "x".repeat(backupFileLimit - 11)};
    expect(new TextEncoder().encode(serializeKnowledgeBackup(exact)).byteLength).toBe(backupFileLimit);
    expect(() => serializeKnowledgeBackup({...exact, metadata: "over limit"})).toThrow("backupTooLarge");
    expect(() => serializeKnowledgeBackup({text: "文".repeat(Math.floor(backupFileLimit / 3))})).toThrow("backupTooLarge");
  });
});


describe("reviewing drafts created outside the current library view", () => {
  const otherDraft: KnowledgeProposal = { id: "other-pending", title: "Another draft", kind: "claw", status: "pending", changes: [], created: "2026-10-24" };
  const requestedDraft: KnowledgeProposal = { ...otherDraft, id: "external-new-draft", title: "New externally generated draft" };
  const oldLibrary = { ...library, proposals: [otherDraft] };
  it("refreshes the library and opens exactly the requested external draft", async () => {
    const latestLibrary = {...oldLibrary, proposals: [otherDraft, requestedDraft]};
    let displayed: KnowledgeProposal | undefined;
    expect(selectKnowledgeProposal(oldLibrary, requestedDraft.id)).toBeUndefined();
    await openFreshKnowledgeProposal(library.id, requestedDraft.id, async () => latestLibrary, () => true, (fresh, id) => {
      displayed = selectKnowledgeProposal(fresh, id);
    });
    expect(displayed?.id).toBe(requestedDraft.id);
  });
  it("reports a missing target rather than displaying another pending draft", async () => {
    const displayed: string[] = [];
    await expect(openFreshKnowledgeProposal(library.id, requestedDraft.id, async () => oldLibrary, () => true, (_, id) => { displayed.push(id); })).rejects.toThrow("proposalUnavailable");
    expect(displayed).toEqual([]);
    expect(selectKnowledgeProposal(oldLibrary, "")).toBe(otherDraft);
  });
  it("does not navigate after switching away while the refresh is pending", async () => {
    const scope = createKnowledgeScope();
    const request = scope.start();
    const displayed: string[] = [];
    let resolve!: (value: KnowledgeLibrary) => void;
    const pending = new Promise<KnowledgeLibrary>((done) => { resolve = done; });
    const review = openFreshKnowledgeProposal(library.id, requestedDraft.id, () => pending, request.current, (_, id) => { displayed.push(id); });
    scope.dispose();
    resolve({...oldLibrary, proposals: [requestedDraft]});
    await review;
    expect(displayed).toEqual([]);
  });
});
