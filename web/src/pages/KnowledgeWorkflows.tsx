import { useEffect, useRef, useState } from "preact/hooks";
import { locale, t } from "../i18n";
import { knowledgeRequest, libraryPath, type KnowledgeLibrary } from "../api/knowledge";
import type { KnowledgeConnection, StewardIssue, StewardJob } from "../api/knowledgeWorkflows";
import { backupFileLimit, connectorConfiguration, createKnowledgeScope, openFreshKnowledgeProposal, parseKnowledgeBackup, serializeKnowledgeBackup, stewardSelectionError } from "../knowledgeWorkflows";
import { knowledgeChatURL } from "../knowledgeChat";

type LibraryProps = { library: KnowledgeLibrary; open: (key: string) => void };
function date(value: string) { return new Date(value).toLocaleString(locale.value === "zh" ? "zh-CN" : "en-US", {month: "short", day: "numeric", hour: "2-digit", minute: "2-digit"}); }
function failure(error: unknown) { return error instanceof Error ? error.message : String(error); }
function useScope() {
  const scope = useRef(createKnowledgeScope());
  useEffect(() => () => scope.current.dispose(), []);
  return scope.current;
}
function Feedback({error, notice}: {error: string; notice?: string}) {
  return <>{error && <div class="kw-flow-error" role="alert">{error}</div>}{notice && <div class="kw-flow-notice" role="status">{notice}</div>}</>;
}
export function KnowledgeSteward({library, open, update, review}: LibraryProps & { update: (library: KnowledgeLibrary) => void; review: (id: string) => void }) {
  void locale.value;
  const scope = useScope();
  const [jobs, setJobs] = useState<StewardJob[]>([]), [issues, setIssues] = useState<StewardIssue[]>([]);
  const [selected, setSelected] = useState<string[]>([]), [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(true), [loaded, setLoaded] = useState(false), [creating, setCreating] = useState(false), [reviewing, setReviewing] = useState(false), [working, setWorking] = useState<string[]>([]);
  const refreshSequence = useRef(0);
  async function refresh() {
    const request = scope.start(), sequence = ++refreshSequence.current;
    try {
      const data = await knowledgeRequest<{jobs: StewardJob[]; issues: StewardIssue[]}>(libraryPath(library.id, "/steward"), "GET", undefined, request.signal);
      if (request.current() && sequence === refreshSequence.current) { setJobs(data.jobs || []); setIssues(data.issues || []); setLoaded(true); }
    } catch (e) { if (request.current()) setError(failure(e)); }
    finally { if (request.current()) setLoading(false); request.done(); }
  }
  useEffect(() => { void refresh(); }, [library]);
  const selectionError = stewardSelectionError(library, selected);
  async function create() {
    if (creating || selectionError || !library.config.model) return;
    const request = scope.start(); setCreating(true); setError(""); setNotice("");
    try {
      await knowledgeRequest<StewardJob>(libraryPath(library.id, "/steward/jobs"), "POST", {sourceIds: selected}, request.signal);
      if (request.current()) { setSelected([]); setNotice(t("knowledge.steward.queuedHint")); await refresh(); }
    } catch (e) { if (request.current()) setError(failure(e)); }
    finally { if (request.current()) setCreating(false); request.done(); }
  }
  async function jobAction(job: StewardJob, action: "run" | "cancel" | "retry") {
    if (action !== "cancel" && working.includes(job.id)) return;
    const request = scope.start(); setWorking((ids) => [...ids, job.id]); setError(""); setNotice("");
    if (action === "run") setJobs((items) => items.map((item) => item.id === job.id ? {...item, status: "running"} : item));
    try {
      await knowledgeRequest(libraryPath(library.id, `/steward/jobs/${encodeURIComponent(job.id)}/${action}`), "POST", {}, request.signal);
      if (request.current()) {
        await refresh();
        const next = await knowledgeRequest<KnowledgeLibrary>(libraryPath(library.id), "GET", undefined, request.signal);
        if (request.current()) update(next);
      }
    } catch (e) { if (request.current()) { setError(failure(e)); await refresh(); } }
    finally { if (request.current()) setWorking((ids) => ids.filter((id) => id !== job.id)); request.done(); }
  }
  async function reviewProposal(proposalId: string) {
    if (reviewing) return;
    const request = scope.start(); setReviewing(true); setError("");
    try {
      await openFreshKnowledgeProposal(
        library.id,
        proposalId,
        () => knowledgeRequest<KnowledgeLibrary>(libraryPath(library.id), "GET", undefined, request.signal),
        request.current,
        (next, id) => { update(next); review(id); },
      );
    } catch (e) {
      if (request.current()) setError(e instanceof Error && e.message === "proposalUnavailable" ? t("knowledge.proposalUnavailableHint") : failure(e));
    } finally { if (request.current()) setReviewing(false); request.done(); }
  }
  const activeSources = library.sources.filter((source) => source.status === "active");
  return <div class="kw-flow-page">
    <div class="kw-page-heading"><div><p class="kw-eyebrow">{t("knowledge.steward.eyebrow")}</p><h1>{t("knowledge.steward")}</h1><p>{t("knowledge.steward.description")}</p></div><a class="kw-button" href={knowledgeChatURL(library.id)}>{t("knowledge.steward.ask")}</a></div>
    <div class="kw-flow-steps"><span><b>01</b>{t("knowledge.steward.stepSources")}</span><span><b>02</b>{t("knowledge.steward.stepDraft")}</span><span><b>03</b>{t("knowledge.steward.stepReview")}</span></div>
    <Feedback error={error} notice={notice}/>
    <div class="kw-steward-grid">
      <section class="kw-flow-card"><div class="kw-flow-heading"><h2>{t("knowledge.steward.organize")}</h2><span class="kw-tag">{t("knowledge.steward.selected", selected.length)}</span></div>
        <p class="kw-muted">{t("knowledge.steward.scopeHint")}</p>
        <div class="kw-source-selection">{activeSources.map((source) => <label key={source.id}><input type="checkbox" checked={selected.includes(source.id)} disabled={creating} onChange={(e) => { const checked = e.currentTarget.checked; setSelected((ids) => checked ? [...ids, source.id] : ids.filter((id) => id !== source.id)); }}/><span><strong>{source.title}</strong><small>{source.filename}</small></span></label>)}{!activeSources.length && <p class="kw-muted">{t("knowledge.steward.emptySources")}</p>}</div>
        <div class="kw-model-summary"><span>{t("knowledge.model")}</span><strong>{library.config.model || t("knowledge.steward.noModel")}</strong><p>{t(library.config.modelSource === "local" ? "knowledge.steward.localScope" : library.config.modelSource === "cloud" ? "knowledge.steward.cloudScope" : "knowledge.steward.providerScope")}</p><button class="kw-text-button" onClick={() => open("settings")}>{t("knowledge.steward.configure")}</button></div>
        {selectionError && selected.length > 0 && <p class="kw-flow-error">{t(`knowledge.steward.${selectionError}`)}</p>}
        <button class="kw-button kw-primary" disabled={creating || !!selectionError || !library.config.model} onClick={() => void create()}>{t(creating ? "knowledge.steward.creating" : "knowledge.steward.queue")}</button>
      </section>
      <section class="kw-flow-card"><div class="kw-flow-heading"><h2>{t("knowledge.steward.health")}</h2><button class="kw-text-button" onClick={() => void refresh()}>{t("knowledge.steward.refresh")}</button></div><p class="kw-muted">{t("knowledge.steward.healthHint")}</p>
        {loading ? <p class="kw-muted">{t("knowledge.loading")}</p> : !loaded ? <button class="kw-button" onClick={() => void refresh()}>{t("knowledge.retry")}</button> : !issues.length ? <div class="kw-health-clear"><span>✓</span><strong>{t("knowledge.steward.healthy")}</strong><p>{t("knowledge.steward.healthyHint")}</p></div> : <div class="kw-issues">{issues.map((issue) => <article key={issue.id}><span class={`kw-tag ${issue.severity === "warning" ? "amber" : ""}`}>{t(`knowledge.steward.issue.${issue.kind}`)}</span><p>{t(`knowledge.steward.issueHint.${issue.kind}`)}</p><div>{issue.pageIds.map((id) => <button class="kw-text-button" key={id} onClick={() => open(`page:${id}`)}>{library.pages.find((page) => page.id === id)?.title || t("knowledge.pageLabel")}</button>)}{issue.sourceIds.map((id) => <button class="kw-text-button" key={id} onClick={() => open(`source:${id}`)}>{library.sources.find((source) => source.id === id)?.title || t("knowledge.sourceLabel")}</button>)}</div></article>)}</div>}
      </section>
    </div>
    <section class="kw-flow-card kw-jobs"><div class="kw-flow-heading"><h2>{t("knowledge.steward.jobs")}</h2><span class="kw-muted">{t("knowledge.steward.manualRun")}</span></div>
      {loaded && !jobs.length && <div class="kw-flow-empty">{t("knowledge.steward.emptyJobs")}</div>}
      {jobs.map((job) => <article class="kw-job" key={job.id}><div class="kw-job-main"><span class={`kw-tag ${job.status === "completed" ? "green" : job.status === "failed" ? "amber" : ""}`}>{t(`knowledge.steward.status.${job.status}`)}</span><h3>{job.sourceIds.map((id) => library.sources.find((source) => source.id === id)?.title || id).join(" · ")}</h3><p>{job.model} · {date(job.created)} · {t("knowledge.steward.attempts", job.attempts || 0)}</p>{job.status === "queued" && <p>{t(job.modelSource === "local" ? "knowledge.steward.localScope" : job.modelSource === "cloud" ? "knowledge.steward.cloudScope" : "knowledge.steward.providerScope")}</p>}{job.error && <p class="kw-job-error" title={job.error}>{locale.value === "en" ? t("knowledge.steward.executionError") : job.error}</p>}</div><div class="kw-job-actions">
        {job.status === "queued" && <button class="kw-button kw-primary kw-small" disabled={working.includes(job.id)} onClick={() => void jobAction(job, "run")}>{t("knowledge.steward.run")}</button>}
        {["queued", "running"].includes(job.status) && <button class="kw-button kw-small" onClick={() => void jobAction(job, "cancel")}>{t("knowledge.steward.cancel")}</button>}
        {["failed", "cancelled"].includes(job.status) && <button class="kw-button kw-small" disabled={working.includes(job.id) || job.attempts >= 3} onClick={() => void jobAction(job, "retry")}>{t("knowledge.steward.retry")}</button>}
        {job.proposalId && <button class="kw-button kw-small" disabled={reviewing} onClick={() => void reviewProposal(job.proposalId!)}>{t("knowledge.steward.openProposal")}</button>}
      </div></article>)}
    </section>
  </div>;
}

export function KnowledgeConnections({library, open}: LibraryProps) {
  void locale.value;
  const scope = useScope();
  const [connections, setConnections] = useState<KnowledgeConnection[]>([]), [loading, setLoading] = useState(true), [loaded, setLoaded] = useState(false);
  const [name, setName] = useState(""), [days, setDays] = useState(30), [allowDraft, setAllowDraft] = useState(false);
  const [secret, setSecret] = useState<{id: string; token: string} | null>(null), [reveal, setReveal] = useState(false);
  const [error, setError] = useState(""), [notice, setNotice] = useState(""), [busy, setBusy] = useState(false);
  async function refresh() {
    const request = scope.start();
    try { const data = await knowledgeRequest<{connections: KnowledgeConnection[]}>(libraryPath(library.id, "/connections"), "GET", undefined, request.signal); if (request.current()) { setConnections(data.connections || []); setLoaded(true); } }
    catch (e) { if (request.current()) setError(failure(e)); }
    finally { if (request.current()) setLoading(false); request.done(); }
  }
  useEffect(() => { void refresh(); }, []);
  async function create(event: Event) {
    event.preventDefault(); if (busy || !name.trim() || !library.config.clawRead || secret) return;
    const request = scope.start(); setBusy(true); setError(""); setNotice("");
    try {
      const data = await knowledgeRequest<{connection: KnowledgeConnection; token: string}>(libraryPath(library.id, "/connections"), "POST", {name: name.trim(), allowDraft: allowDraft && library.config.clawDraft, expiresInDays: days}, request.signal);
      if (request.current()) { setSecret({id: data.connection.id, token: data.token}); setReveal(false); setName(""); await refresh(); }
    } catch (e) { if (request.current()) setError(failure(e)); }
    finally { if (request.current()) setBusy(false); request.done(); }
  }
  async function revoke(connection: KnowledgeConnection) {
    if (busy || !window.confirm(t("knowledge.connections.revokeConfirm", connection.name))) return;
    const request = scope.start(); setBusy(true); setError("");
    try { await knowledgeRequest(libraryPath(library.id, `/connections/${encodeURIComponent(connection.id)}/revoke`), "POST", {}, request.signal); if (request.current()) { if (secret?.id === connection.id) { setSecret(null); setReveal(false); } await refresh(); setNotice(t("knowledge.connections.revokedHint")); } }
    catch (e) { if (request.current()) setError(failure(e)); }
    finally { if (request.current()) setBusy(false); request.done(); }
  }
  async function copy(value: string) {
    const request = scope.start();
    try { await navigator.clipboard.writeText(value); if (request.current()) setNotice(t("knowledge.copied")); }
    catch { if (request.current()) setError(t("knowledge.connections.copyFailed")); }
    finally { request.done(); }
  }
  const endpoint = `${window.location.origin}/api/knowledge/mcp`;
  return <div class="kw-flow-page">
    <div class="kw-page-heading"><div><p class="kw-eyebrow">{t("knowledge.connections.eyebrow")}</p><h1>{t("knowledge.claw")}</h1><p>{t("knowledge.connections.description")}</p></div><a class="kw-button" href="/ai-apps?app=csgclaw">{t("knowledge.openApps")}</a></div>
    <Feedback error={error} notice={notice}/>
    <div class="kw-flow-steps"><span><b>01</b>{t("knowledge.connections.stepAuthorize")}</span><span><b>02</b>{t("knowledge.connections.stepConfigure")}</span><span><b>03</b>{t("knowledge.connections.stepBind")}</span></div>
    <div class="kw-connection-policy"><div><strong>{t("knowledge.connections.libraryScope", library.name)}</strong><p>{t("knowledge.connections.policyHint")}</p></div><button class="kw-button" onClick={() => open("settings")}>{t("knowledge.settings")}</button></div>
    {!library.config.clawRead && <p class="kw-flow-notice">{t("knowledge.connections.enableHint")}</p>}
    <div class="kw-steward-grid">
      <section class="kw-flow-card"><h2>{t("knowledge.connections.create")}</h2><p class="kw-muted">{t("knowledge.connections.createHint")}</p>
        <form class="kw-connection-form" onSubmit={(event) => void create(event)}>
          <label class="kw-form-label">{t("knowledge.connections.name")}<input value={name} required maxLength={80} placeholder={t("knowledge.connections.namePlaceholder")} disabled={busy || !!secret} onInput={(event) => setName(event.currentTarget.value)}/></label>
          <label class="kw-form-label">{t("knowledge.connections.expiry")}<select value={days} disabled={busy || !!secret} onChange={(event) => setDays(Number(event.currentTarget.value))}>{[7, 30, 90, 365].map((value) => <option key={value} value={value}>{t("knowledge.connections.days", value)}</option>)}</select></label>
          <label class="kw-check"><input type="checkbox" checked={allowDraft && library.config.clawDraft} disabled={busy || !!secret || !library.config.clawDraft} onChange={(event) => setAllowDraft(event.currentTarget.checked)}/>{t("knowledge.connections.allowDraft")}</label>
          <p class="kw-muted">{t("knowledge.connections.draftHint")}</p>
          <button class="kw-button kw-primary" disabled={busy || !!secret || !name.trim() || !library.config.clawRead}>{t(busy ? "knowledge.loading" : "knowledge.connections.generate")}</button>
        </form>
      </section>
      <section class="kw-flow-card"><h2>{t("knowledge.connections.setup")}</h2><ol class="kw-setup-list"><li>{t("knowledge.connections.instruction1")}</li><li>{t("knowledge.connections.instruction2")}</li><li>{t("knowledge.connections.instruction3")}</li></ol><label class="kw-form-label">{t("knowledge.connections.endpoint")}<code class="kw-endpoint">{endpoint}</code></label><p class="kw-muted">{t("knowledge.connections.machineHint")}</p><p class="kw-muted">{t("knowledge.connections.stateHint")}</p></section>
    </div>
    {secret && <section class="kw-flow-card kw-secret-card"><div class="kw-flow-heading"><h2>{t("knowledge.connections.secretTitle")}</h2><span class="kw-tag amber">{t("knowledge.connections.once")}</span></div><p>{t("knowledge.connections.secretHint")}</p><code class="kw-secret-value">{reveal ? secret.token : "••••••••••••••••••••••••••••••••"}</code><div class="kw-claw-actions"><button class="kw-button kw-small" onClick={() => setReveal(!reveal)}>{t(reveal ? "knowledge.connections.hide" : "knowledge.connections.reveal")}</button><button class="kw-button kw-small" onClick={() => void copy(secret.token)}>{t("knowledge.connections.copyToken")}</button><button class="kw-button kw-small" onClick={() => void copy(connectorConfiguration(window.location.origin, secret.token))}>{t("knowledge.connections.copyConfig")}</button><button class="kw-text-button" onClick={() => { setSecret(null); setReveal(false); }}>{t("knowledge.connections.dismiss")}</button></div>{reveal && <pre class="kw-config-code">{connectorConfiguration(window.location.origin, secret.token)}</pre>}</section>}
    <section class="kw-flow-card"><div class="kw-flow-heading"><h2>{t("knowledge.connections.authorized")}</h2><button class="kw-text-button" onClick={() => void refresh()}>{t("knowledge.steward.refresh")}</button></div>
      {loading ? <p>{t("knowledge.loading")}</p> : !loaded ? <button class="kw-button" onClick={() => void refresh()}>{t("knowledge.retry")}</button> : !connections.length ? <div class="kw-flow-empty">{t("knowledge.connections.empty")}</div> : connections.map((connection) => {
        const expired = Date.parse(connection.expiresAt) <= Date.now();
        const status = connection.revoked ? "revoked" : expired ? "expired" : !library.config.clawRead ? "blocked" : connection.lastUsed ? "used" : "unused";
        return <article class="kw-job" key={connection.id}><div class="kw-job-main"><span class={`kw-tag ${status === "used" ? "green" : ""}`}>{t(`knowledge.connections.status.${status}`)}</span><h3>{connection.name}</h3><p>{t(connection.allowDraft ? "knowledge.connections.readDraft" : "knowledge.connections.readOnly")} · {t("knowledge.connections.expires", date(connection.expiresAt))}</p><p>{connection.lastUsed ? t("knowledge.connections.lastUsed", date(connection.lastUsed)) : t("knowledge.connections.neverUsed")}</p></div>{!connection.revoked && <button class="kw-button kw-small kw-danger" disabled={busy} onClick={() => void revoke(connection)}>{t("knowledge.connections.revoke")}</button>}</article>;
      })}
    </section>
  </div>;
}

export function KnowledgeBackup({library, restored}: {library?: KnowledgeLibrary; restored: (library: KnowledgeLibrary) => void}) {
  void locale.value;
  const scope = useScope();
  const [backup, setBackup] = useState<unknown>(null), [fileName, setFileName] = useState("");
  const [error, setError] = useState(""), [notice, setNotice] = useState(""), [busy, setBusy] = useState(false);
  const fileSequence = useRef(0);
  async function choose(file?: File) {
    const sequence = ++fileSequence.current;
    setBackup(null); setFileName(""); setError(""); setNotice("");
    if (!file) return;
    if (file.size > backupFileLimit) { setError(t("knowledge.backup.tooLarge")); return; }
    const request = scope.start();
    try { const value = parseKnowledgeBackup(await file.text()); if (request.current() && sequence === fileSequence.current) { setBackup(value); setFileName(file.name); } }
    catch { if (request.current() && sequence === fileSequence.current) setError(t("knowledge.backup.invalid")); }
    finally { request.done(); }
  }
  async function download() {
    if (!library || busy) return;
    const request = scope.start(); setBusy(true); setError("");
    try {
      const result = await knowledgeRequest(libraryPath(library.id, "/backup"), "GET", undefined, request.signal);
      if (request.current()) {
        const contents = serializeKnowledgeBackup(result);
        const href = URL.createObjectURL(new Blob([contents], {type: "application/json"}));
        const anchor = document.createElement("a"); anchor.href = href; anchor.download = `csglite-backup-${library.id}.json`; anchor.click(); window.setTimeout(() => URL.revokeObjectURL(href), 1000); setNotice(t("knowledge.downloaded"));
      }
    } catch (e) { if (request.current()) setError(e instanceof Error && e.message === "backupTooLarge" ? t("knowledge.backup.exportTooLarge") : failure(e)); }
    finally { if (request.current()) setBusy(false); request.done(); }
  }
  async function restore() {
    if (!backup || busy) return;
    const request = scope.start(); setBusy(true); setError("");
    try { const next = await knowledgeRequest<KnowledgeLibrary>("/restore", "POST", {backup}, request.signal); if (request.current()) restored(next); }
    catch (e) { if (request.current()) setError(failure(e)); }
    finally { if (request.current()) setBusy(false); request.done(); }
  }
  return <section class="kw-backup-section"><h3>{t("knowledge.backup.title")}</h3><p class="kw-muted">{t("knowledge.backup.hint")}</p><Feedback error={error} notice={notice}/><div class="kw-claw-actions">{library && <button class="kw-button" disabled={busy} onClick={() => void download()}>{t("knowledge.backup.download")}</button>}<label class={`kw-button kw-file-label ${busy ? "disabled" : ""}`}>{t("knowledge.backup.choose")}<input type="file" accept=".json,application/json" disabled={busy} onChange={(event) => { void choose(event.currentTarget.files?.[0]); event.currentTarget.value = ""; }}/></label></div>{backup != null && <div class="kw-restore-confirm"><strong>{fileName}</strong><p>{t("knowledge.backup.restoreHint")}</p><button class="kw-button kw-primary" disabled={busy} onClick={() => void restore()}>{t(busy ? "knowledge.loading" : "knowledge.backup.restore")}</button></div>}</section>;
}
