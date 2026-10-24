import MarkdownIt from "markdown-it";
import type {
  KnowledgeChange,
  KnowledgeLibrary,
  KnowledgePage,
  KnowledgeSource,
} from "./api/knowledge";

const markdown = new MarkdownIt({ html: false, linkify: true, breaks: false });
const escapeHTML = (value: string) => markdown.utils.escapeHtml(value);
// Parse references as Markdown tokens, after escape/code rules. Escaped brackets,
// code spans, fenced code and existing links therefore stay literal.
markdown.inline.ruler.before("link", "knowledge_reference", (state, silent) => {
  if (
    silent ||
    state.src[state.pos] !== "[" ||
    (state as typeof state & { linkLevel?: number }).linkLevel! > 0
  )
    return false;
  const match = /^(?:\[\[([^\]\n]+)\]\]|\[source:([^\]\n]+)\])/.exec(
    state.src.slice(state.pos),
  );
  if (!match) return false;
  if (!silent) {
    const token = state.push("knowledge_reference", "", 0);
    token.content = match[0];
    const [target, alias] = (match[1] || "").split("|");
    token.meta = match[1]
      ? {
          kind: "page",
          target: target.split("#")[0].trim(),
          label: alias || target,
        }
      : { kind: "source", target: match[2], label: match[0] };
  }
  state.pos += match[0].length;
  return true;
});
markdown.renderer.rules.knowledge_reference = (
  tokens,
  index,
  _options,
  env: { library?: KnowledgeLibrary },
) => {
  const token = tokens[index],
    ref = token.meta as { kind: string; target: string; label: string };
  if (ref.kind === "page") {
    const page = env.library?.pages.find((p) => p.title === ref.target);
    return page
      ? `<button class="kw-wikilink" data-page="${escapeHTML(page.id)}">${escapeHTML(ref.label)}</button>`
      : `<span class="kw-missing-link">${escapeHTML(ref.label)}</span>`;
  }
  const source = env.library?.sources.find((s) => s.id === ref.target);
  return source
    ? `<button class="kw-citation" data-source="${escapeHTML(source.id)}">↗ ${escapeHTML(source.title)}</button>`
    : `<span class="kw-missing-link">${escapeHTML(token.content)}</span>`;
};
export function wikiLinks(content: string): string[] {
  const links = markdown
    .parse(content, {})
    .flatMap((block) => block.children || [])
    .filter(
      (token) =>
        token.type === "knowledge_reference" && token.meta.kind === "page",
    )
    .map((token) => token.meta.target as string);
  return [...new Set(links)];
}
export function backlinks(
  page: KnowledgePage,
  pages: KnowledgePage[],
): KnowledgePage[] {
  return pages.filter(
    (other) =>
      other.id !== page.id && wikiLinks(other.content).includes(page.title),
  );
}
export function renderKnowledgeMarkdown(
  content: string,
  library: KnowledgeLibrary,
): string {
  return markdown.render(content, { library });
}
export function decodeKnowledgeFile(bytes: ArrayBuffer): string {
  const text = new TextDecoder("utf-8", {
    fatal: true,
    ignoreBOM: true,
  }).decode(bytes);
  if (text.includes("\0")) throw new Error("Invalid text file");
  return text;
}
export function searchKnowledge(library: KnowledgeLibrary, query: string) {
  const q = query.trim().toLocaleLowerCase();
  return [
    ...library.pages
      .filter((p) => !p.stale)
      .map((p) => ({
        key: `page:${p.id}`,
        title: p.title,
        content: p.content,
        kind: "page",
      })),
    ...library.sources
      .filter((s) => s.status === "active")
      .map((s) => ({
        key: `source:${s.id}`,
        title: s.title,
        content: s.content,
        kind: "source",
      })),
  ].filter(
    (item) =>
      !q || `${item.title}\n${item.content}`.toLocaleLowerCase().includes(q),
  );
}
export function parseModelProposal(
  output: string,
  library: KnowledgeLibrary,
  source: KnowledgeSource,
): KnowledgeChange[] {
  const cleaned = output
    .trim()
    .replace(/^```(?:json)?\s*/i, "")
    .replace(/\s*```$/, "");
  const parsed: unknown = JSON.parse(cleaned);
  const rows = Array.isArray(parsed)
    ? parsed
    : parsed && typeof parsed === "object" && "changes" in parsed
      ? (parsed as { changes: unknown }).changes
      : [parsed];
  if (!Array.isArray(rows) || rows.length < 1 || rows.length > 8)
    throw new Error("Invalid model proposal");
  const targets = new Set<string>();
  return rows.map((row) => {
    if (!row || typeof row !== "object")
      throw new Error("Invalid model proposal");
    const value = row as Record<string, unknown>;
    if (
      typeof value.title !== "string" ||
      !value.title.trim() ||
      typeof value.content !== "string" ||
      !value.content.trim()
    )
      throw new Error("Invalid model proposal");
    const pageId = typeof value.pageId === "string" ? value.pageId : "";
    const page = pageId
      ? library.pages.find((p) => p.id === pageId)
      : undefined;
    if (pageId && !page) throw new Error("Unknown page id");
    const target = pageId || value.title.trim();
    if (targets.has(target)) throw new Error("Duplicate page");
    targets.add(target);
    const allowed = new Set([source.id, ...(page?.sourceIds || [])]);
    const cited = [...value.content.matchAll(/\[source:([^\]]+)\]/g)].map(
      (m) => m[1],
    );
    if (
      !cited.includes(source.id) ||
      cited.some(
        (id) =>
          !allowed.has(id) ||
          !library.sources.some((s) => s.id === id && s.status === "active"),
      )
    )
      throw new Error("Invalid citations");
    return {
      pageId,
      title: value.title.trim(),
      content: value.content,
      sourceIds: [...new Set(cited)],
      baseVersion: page?.version || 0,
    };
  });
}
export function graphData(library: KnowledgeLibrary, includeSources: boolean) {
  const nodes = [
    ...library.pages
      .filter((p) => !p.stale)
      .map((p) => ({ id: `page:${p.id}`, title: p.title, kind: "page" })),
    ...(includeSources
      ? library.sources
          .filter((s) => s.status === "active")
          .map((s) => ({
            id: `source:${s.id}`,
            title: s.title,
            kind: "source",
          }))
      : []),
  ];
  const edges: { from: string; to: string }[] = [];
  for (const page of library.pages.filter((p) => !p.stale)) {
    for (const title of wikiLinks(page.content)) {
      const target = library.pages.find((p) => !p.stale && p.title === title);
      if (target && target.id !== page.id)
        edges.push({ from: `page:${page.id}`, to: `page:${target.id}` });
    }
    if (includeSources)
      for (const id of page.sourceIds)
        if (nodes.some((n) => n.id === `source:${id}`))
          edges.push({ from: `page:${page.id}`, to: `source:${id}` });
  }
  return { nodes, edges };
}

/** Keep the active editor mounted when a browser-history navigation is declined. */
export function guardKnowledgeHistory(
  event: Pick<Event, "stopImmediatePropagation">,
  options: {
    dirty: boolean;
    pending: boolean;
    confirmLeave: () => boolean;
    restoreLocation: () => void;
  },
): boolean {
  if (!options.pending && (!options.dirty || options.confirmLeave()))
    return false;
  event.stopImmediatePropagation();
  options.restoreLocation();
  return true;
}
