import type { KnowledgeLibrary, KnowledgeProposal } from "./api/knowledge";

// A scope belongs to one mounted library view. Late responses must never update
// another library, reveal an old credential, or navigate after the user leaves.
export function createKnowledgeScope() {
  let alive = true;
  const controllers = new Set<AbortController>();
  return {
    start() {
      const controller = new AbortController();
      if (!alive) controller.abort();
      controllers.add(controller);
      return {
        signal: controller.signal,
        current: () => alive && !controller.signal.aborted,
        done: () => controllers.delete(controller),
      };
    },
    dispose() {
      alive = false;
      controllers.forEach((controller) => controller.abort());
      controllers.clear();
    },
  };
}
export const backupFileLimit = 20 * 1024 * 1024;
export function serializeKnowledgeBackup(backup: unknown): string {
  const text = JSON.stringify(backup);
  if (new TextEncoder().encode(text).byteLength > backupFileLimit) throw new Error("backupTooLarge");
  return text;
}
export function parseKnowledgeBackup(text: string): unknown {
  const data = JSON.parse(text);
  if (!data || data.format !== "csglite-knowledge-backup" || data.version !== 1 || !data.library || typeof data.sha256 !== "string") {
    throw new Error("invalidBackup");
  }
  return data;
}
export function connectorConfiguration(origin: string, token: string) {
  const url = new URL("/api/knowledge/mcp", origin);
  if (!["http:", "https:"].includes(url.protocol)) throw new Error("invalidOrigin");
  return JSON.stringify({ mcpServers: { "csglite-knowledge": { transport: "streamable-http", url: url.toString(), headers: { Authorization: `Bearer ${token}` } } } }, null, 2);
}
export function stewardSelectionError(library: KnowledgeLibrary, selected: string[]): "selectionEmpty" | "selectionLimit" | "selectionUnavailable" | "selectionSize" | null {
  if (!selected.length) return "selectionEmpty";
  if (selected.length > 8) return "selectionLimit";
  const sources = selected.map((id) => library.sources.find((source) => source.id === id && source.status === "active"));
  if (sources.some((source) => !source)) return "selectionUnavailable";
  if (sources.reduce((sum, source) => sum + new TextEncoder().encode(source!.content).length, 0) > 64 * 1024) return "selectionSize";
  return null;
}


// An explicit review target must never silently fall back to another proposal.
export function selectKnowledgeProposal(library: KnowledgeLibrary | undefined, proposalId: string): KnowledgeProposal | undefined {
  if (proposalId) return library?.proposals.find((proposal) => proposal.id === proposalId);
  return library?.proposals.find((proposal) => proposal.status === "pending") || library?.proposals[0];
}
export async function openFreshKnowledgeProposal(
  libraryId: string,
  proposalId: string,
  load: () => Promise<KnowledgeLibrary>,
  current: () => boolean,
  show: (library: KnowledgeLibrary, proposalId: string) => void,
): Promise<void> {
  const library = await load();
  if (!current()) return;
  if (library.id !== libraryId || !proposalId || !selectKnowledgeProposal(library, proposalId)) {
    throw new Error("proposalUnavailable");
  }
  show(library, proposalId);
}
