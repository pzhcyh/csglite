export interface StewardJob {
  id: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled";
  sourceIds: string[];
  created: string;
  updated: string;
  model: string;
  modelSource: string;
  attempts: number;
  proposalId?: string;
  error?: string;
}
export interface StewardIssue {
  id: string;
  kind: "missing_source" | "stale_page" | "uncompiled_source" | "duplicate_source";
  severity: "warning" | "info";
  message: string;
  sourceIds: string[];
  pageIds: string[];
}
export interface KnowledgeConnection {
  id: string;
  name: string;
  allowDraft: boolean;
  created: string;
  expiresAt: string;
  revoked: boolean;
  lastUsed?: string;
}
