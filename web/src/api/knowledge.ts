import { locale, t } from "../i18n";

export interface KnowledgeConfig {
  model: string;
  modelSource: string;
  clawRead: boolean;
  clawDraft: boolean;
}
export interface KnowledgeSource {
  id: string;
  title: string;
  filename: string;
  content: string;
  sha256: string;
  status: "active" | "withdrawn";
  created: string;
}
export interface KnowledgeRevision {
  version: number;
  title: string;
  content: string;
  sourceIds: string[];
  updated: string;
}
export interface KnowledgePage extends KnowledgeRevision {
  id: string;
  history: KnowledgeRevision[];
  stale: boolean;
}
export interface KnowledgeChange {
  pageId: string;
  title: string;
  content: string;
  sourceIds: string[];
  baseVersion: number;
}
export interface KnowledgeProposal {
  id: string;
  title: string;
  kind: "manual" | "model" | "demo" | "claw";
  status: "pending" | "accepted" | "rejected";
  changes: KnowledgeChange[];
  created: string;
}
export interface KnowledgeLibrary {
  id: string;
  name: string;
  description: string;
  config: KnowledgeConfig;
  sources: KnowledgeSource[];
  pages: KnowledgePage[];
  proposals: KnowledgeProposal[];
  logs: { id: string; time: string; message: string }[];
}

export async function knowledgeRequest<T>(
  path = "",
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(`/api/knowledge${path}`, {
    method,
    signal,
    headers: {
      "Accept-Language": locale.value === "zh" ? "zh-CN" : "en-US",
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  const text = await response.text();
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    throw new Error(t("knowledge.apiError", response.status));
  }
  if (!response.ok)
    throw new Error(
      (data as { error?: string }).error ||
        t("knowledge.apiError", response.status),
    );
  return data as T;
}
export function libraryPath(id: string, suffix = "") {
  return `/libraries/${encodeURIComponent(id)}${suffix}`;
}
export async function downloadKnowledge(
  id: string,
  kind: "export" | "context",
  includeRaw = false,
): Promise<void> {
  const url = `/api/knowledge${libraryPath(id)}/${kind}${kind === "export" ? `?includeRaw=${includeRaw}` : ""}`;
  const response = await fetch(url, {
    headers: { "Accept-Language": locale.value },
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error || t("knowledge.apiError", response.status));
  }
  const blob = await response.blob();
  const href = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = href;
  anchor.download = `knowledge-${id}.${kind === "export" ? "zip" : "json"}`;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(href), 1000);
}
