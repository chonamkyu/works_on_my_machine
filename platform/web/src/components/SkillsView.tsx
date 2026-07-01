/**
 * Two-pane browser for the global Skills registry.
 *
 * Left: list of skills (name, description, tags, latest version).
 * Right: selected skill's versions + aliases + the selected version's body.
 *
 * Admin users (isAdmin from /v1/me) get: publish-new-version form,
 * move-alias control, delete. Non-admins see read-only.
 */

import { useEffect, useState } from "react";
import { Trash2, Tag, Plus, ArrowRight } from "lucide-react";
import { clsx } from "clsx";
import { api } from "../lib/api";
import type { GlobalSkill, GlobalSkillDetail, GlobalSkillVersion, GlobalSkillAlias } from "../lib/api";

export function SkillsView() {
  const [skills, setSkills] = useState<GlobalSkill[]>([]);
  const [selectedName, setSelectedName] = useState<string | null>(null);
  const [detail, setDetail] = useState<GlobalSkillDetail | null>(null);
  const [selectedVersionNo, setSelectedVersionNo] = useState<number | null>(null);
  const [versionBody, setVersionBody] = useState<GlobalSkillVersion | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [versionLoading, setVersionLoading] = useState(false);

  // Admin action state
  const [showPublishForm, setShowPublishForm] = useState(false);
  const [publishForm, setPublishForm] = useState({ description: "", tags: "", body: "", comment: "" });
  const [publishLoading, setPublishLoading] = useState(false);
  const [showAliasForm, setShowAliasForm] = useState(false);
  const [aliasForm, setAliasForm] = useState({ alias: "", versionNo: "" });
  const [aliasLoading, setAliasLoading] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [createForm, setCreateForm] = useState({ name: "", description: "", tags: "", body: "", comment: "" });
  const [createLoading, setCreateLoading] = useState(false);

  const refresh = async () => {
    setLoading(true);
    try {
      const [{ skills: list }, me] = await Promise.all([
        api.listSkills(),
        api.getMe(),
      ]);
      setSkills(list ?? []);
      setIsAdmin(me.isAdmin);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  useEffect(() => {
    if (!selectedName) {
      setDetail(null);
      setSelectedVersionNo(null);
      setVersionBody(null);
      return;
    }
    setDetailLoading(true);
    api.getSkill(selectedName)
      .then((d) => {
        setDetail(d);
        // default: select latest version
        const latestNo = d.skill.latestVersion;
        setSelectedVersionNo(latestNo > 0 ? latestNo : null);
      })
      .catch(console.error)
      .finally(() => setDetailLoading(false));
  }, [selectedName]);

  useEffect(() => {
    if (!selectedName || selectedVersionNo === null) {
      setVersionBody(null);
      return;
    }
    setVersionLoading(true);
    api.getSkillVersion(selectedName, selectedVersionNo)
      .then(setVersionBody)
      .catch(console.error)
      .finally(() => setVersionLoading(false));
  }, [selectedName, selectedVersionNo]);

  const handlePublish = async () => {
    if (!selectedName) return;
    setPublishLoading(true);
    try {
      await api.publishSkillVersion(selectedName, {
        description: publishForm.description,
        tags: publishForm.tags.split(",").map((t) => t.trim()).filter(Boolean),
        body: publishForm.body,
        comment: publishForm.comment,
      });
      setShowPublishForm(false);
      setPublishForm({ description: "", tags: "", body: "", comment: "" });
      // reload detail
      const d = await api.getSkill(selectedName);
      setDetail(d);
      setSelectedVersionNo(d.skill.latestVersion);
      // refresh list to update latestVersion
      const { skills: list } = await api.listSkills();
      setSkills(list ?? []);
    } catch (err) {
      console.error(err);
    } finally {
      setPublishLoading(false);
    }
  };

  const handleMoveAlias = async () => {
    if (!selectedName) return;
    setAliasLoading(true);
    try {
      await api.moveSkillAlias(selectedName, aliasForm.alias, Number(aliasForm.versionNo));
      setShowAliasForm(false);
      setAliasForm({ alias: "", versionNo: "" });
      const d = await api.getSkill(selectedName);
      setDetail(d);
    } catch (err) {
      console.error(err);
    } finally {
      setAliasLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!selectedName) return;
    if (!confirm(`Delete skill "${selectedName}"? This cannot be undone.`)) return;
    setDeleteLoading(true);
    try {
      await api.deleteSkill(selectedName);
      setSelectedName(null);
      const { skills: list } = await api.listSkills();
      setSkills(list ?? []);
    } catch (err) {
      console.error(err);
    } finally {
      setDeleteLoading(false);
    }
  };

  const handleCreate = async () => {
    setCreateLoading(true);
    try {
      await api.createSkill({
        name: createForm.name,
        description: createForm.description,
        tags: createForm.tags.split(",").map((t) => t.trim()).filter(Boolean),
        body: createForm.body,
        comment: createForm.comment,
      });
      setShowCreateForm(false);
      setCreateForm({ name: "", description: "", tags: "", body: "", comment: "" });
      const { skills: list } = await api.listSkills();
      setSkills(list ?? []);
    } catch (err) {
      console.error(err);
    } finally {
      setCreateLoading(false);
    }
  };

  return (
    <section className="flex-1 flex min-w-0 h-full">
      {/* Left pane: skills list */}
      <div className="w-[280px] border-r border-hairline flex flex-col">
        <header className="h-10 border-b border-hairline px-3 flex items-center gap-2 flex-shrink-0">
          <span className="font-mono text-xs uppercase tracking-wider text-ink-subtle">skills</span>
          <span className="text-ink-tertiary text-xs">{skills.length}</span>
          {isAdmin && (
            <button
              onClick={() => setShowCreateForm((v) => !v)}
              className="ml-auto p-1 rounded hover:bg-surface-2"
              title="Create skill"
            >
              <Plus className="w-3.5 h-3.5 text-ink-subtle" />
            </button>
          )}
        </header>

        {isAdmin && showCreateForm && (
          <div className="border-b border-hairline p-3 space-y-2 bg-surface-2/40">
            <p className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">New skill</p>
            <input
              className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
              placeholder="name (slug)"
              value={createForm.name}
              onChange={(e) => setCreateForm((f) => ({ ...f, name: e.target.value }))}
            />
            <input
              className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
              placeholder="description"
              value={createForm.description}
              onChange={(e) => setCreateForm((f) => ({ ...f, description: e.target.value }))}
            />
            <input
              className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
              placeholder="tags (comma-separated)"
              value={createForm.tags}
              onChange={(e) => setCreateForm((f) => ({ ...f, tags: e.target.value }))}
            />
            <textarea
              className="w-full bg-canvas border border-hairline rounded px-2 py-1 text-xs text-ink placeholder:text-ink-tertiary font-mono resize-none"
              rows={4}
              placeholder="skill body (markdown)"
              value={createForm.body}
              onChange={(e) => setCreateForm((f) => ({ ...f, body: e.target.value }))}
            />
            <input
              className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
              placeholder="comment"
              value={createForm.comment}
              onChange={(e) => setCreateForm((f) => ({ ...f, comment: e.target.value }))}
            />
            <div className="flex gap-2">
              <button
                onClick={() => void handleCreate()}
                disabled={createLoading || !createForm.name || !createForm.body}
                className="h-7 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
              >
                {createLoading ? "creating…" : "Create"}
              </button>
              <button
                onClick={() => setShowCreateForm(false)}
                className="h-7 px-3 rounded text-xs text-ink-subtle hover:bg-surface-2"
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        <div className="flex-1 overflow-y-auto">
          {loading && <p className="p-3 text-xs text-ink-subtle">loading…</p>}
          {!loading && skills.length === 0 && (
            <p className="p-3 text-xs text-ink-tertiary">
              No skills in the registry yet. Admins can publish skills here.
            </p>
          )}
          {skills.map((s) => (
            <button
              key={s.id}
              onClick={() => {
                setSelectedName(s.name);
                setShowPublishForm(false);
                setShowAliasForm(false);
              }}
              className={clsx(
                "w-full text-left px-3 py-2.5 border-l-2 transition-colors",
                s.name === selectedName
                  ? "border-accent bg-surface-3"
                  : "border-transparent hover:bg-surface-2",
              )}
            >
              <div className="text-sm truncate text-ink">{s.name}</div>
              {s.description && (
                <div className="text-[11px] text-ink-muted truncate mt-0.5">{s.description}</div>
              )}
              <div className="flex items-center gap-2 mt-1">
                <span className="font-mono text-[10px] text-ink-tertiary">v{s.latestVersion}</span>
                {s.tags.slice(0, 3).map((tag) => (
                  <span key={tag} className="font-mono text-[9px] bg-surface-2 px-1 rounded text-ink-tertiary">
                    {tag}
                  </span>
                ))}
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* Right pane: detail */}
      <div className="flex-1 overflow-y-auto flex flex-col min-w-0">
        {!selectedName && (
          <div className="flex-1 flex items-center justify-center">
            <p className="text-xs text-ink-tertiary">Select a skill on the left.</p>
          </div>
        )}

        {selectedName && (
          <>
            <header className="h-10 border-b border-hairline px-4 flex items-center gap-3 flex-shrink-0">
              <span className="font-semibold text-sm text-ink">{selectedName}</span>
              {detail && (
                <span className="font-mono text-[10px] text-ink-tertiary">
                  {detail.versions.length} version{detail.versions.length !== 1 ? "s" : ""} ·{" "}
                  {detail.aliases.length} alias{detail.aliases.length !== 1 ? "es" : ""}
                </span>
              )}
              {isAdmin && (
                <div className="ml-auto flex items-center gap-2">
                  <button
                    onClick={() => { setShowPublishForm((v) => !v); setShowAliasForm(false); }}
                    className="h-7 px-2 rounded text-xs text-ink-subtle hover:bg-surface-2 flex items-center gap-1"
                  >
                    <Plus className="w-3 h-3" /> Publish version
                  </button>
                  <button
                    onClick={() => { setShowAliasForm((v) => !v); setShowPublishForm(false); }}
                    className="h-7 px-2 rounded text-xs text-ink-subtle hover:bg-surface-2 flex items-center gap-1"
                  >
                    <Tag className="w-3 h-3" /> Move alias
                  </button>
                  <button
                    onClick={() => void handleDelete()}
                    disabled={deleteLoading}
                    className="h-7 px-2 rounded text-xs text-status-failed/80 hover:bg-surface-2 flex items-center gap-1 disabled:opacity-50"
                  >
                    <Trash2 className="w-3 h-3" /> {deleteLoading ? "deleting…" : "Delete"}
                  </button>
                </div>
              )}
            </header>

            {detailLoading && (
              <p className="p-4 text-xs text-ink-subtle">loading…</p>
            )}

            {!detailLoading && detail && (
              <div className="p-4 space-y-5">

                {/* Admin: publish new version form */}
                {isAdmin && showPublishForm && (
                  <div className="border border-hairline rounded p-3 space-y-2 bg-surface-2/40">
                    <p className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">
                      Publish new version
                    </p>
                    <input
                      className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
                      placeholder="description"
                      value={publishForm.description}
                      onChange={(e) => setPublishForm((f) => ({ ...f, description: e.target.value }))}
                    />
                    <input
                      className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
                      placeholder="tags (comma-separated)"
                      value={publishForm.tags}
                      onChange={(e) => setPublishForm((f) => ({ ...f, tags: e.target.value }))}
                    />
                    <textarea
                      className="w-full bg-canvas border border-hairline rounded px-2 py-1 text-xs text-ink placeholder:text-ink-tertiary font-mono resize-none"
                      rows={6}
                      placeholder="skill body (markdown)"
                      value={publishForm.body}
                      onChange={(e) => setPublishForm((f) => ({ ...f, body: e.target.value }))}
                    />
                    <input
                      className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
                      placeholder="comment"
                      value={publishForm.comment}
                      onChange={(e) => setPublishForm((f) => ({ ...f, comment: e.target.value }))}
                    />
                    <div className="flex gap-2">
                      <button
                        onClick={() => void handlePublish()}
                        disabled={publishLoading || !publishForm.body}
                        className="h-7 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
                      >
                        {publishLoading ? "publishing…" : "Publish"}
                      </button>
                      <button
                        onClick={() => setShowPublishForm(false)}
                        className="h-7 px-3 rounded text-xs text-ink-subtle hover:bg-surface-2"
                      >
                        Cancel
                      </button>
                    </div>
                  </div>
                )}

                {/* Admin: move alias form */}
                {isAdmin && showAliasForm && (
                  <div className="border border-hairline rounded p-3 space-y-2 bg-surface-2/40">
                    <p className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">
                      Move alias
                    </p>
                    <div className="flex gap-2">
                      <input
                        className="flex-1 bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
                        placeholder="alias (e.g. stable)"
                        value={aliasForm.alias}
                        onChange={(e) => setAliasForm((f) => ({ ...f, alias: e.target.value }))}
                      />
                      <ArrowRight className="w-4 h-4 self-center text-ink-tertiary flex-shrink-0" />
                      <input
                        className="w-24 bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
                        placeholder="version #"
                        type="number"
                        min={1}
                        value={aliasForm.versionNo}
                        onChange={(e) => setAliasForm((f) => ({ ...f, versionNo: e.target.value }))}
                      />
                    </div>
                    <div className="flex gap-2">
                      <button
                        onClick={() => void handleMoveAlias()}
                        disabled={aliasLoading || !aliasForm.alias || !aliasForm.versionNo}
                        className="h-7 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
                      >
                        {aliasLoading ? "moving…" : "Move"}
                      </button>
                      <button
                        onClick={() => setShowAliasForm(false)}
                        className="h-7 px-3 rounded text-xs text-ink-subtle hover:bg-surface-2"
                      >
                        Cancel
                      </button>
                    </div>
                  </div>
                )}

                {/* Description + tags */}
                <div>
                  <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1">about</div>
                  <p className="text-sm text-ink">{detail.skill.description || <span className="text-ink-tertiary">(no description)</span>}</p>
                  {detail.skill.tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                      {detail.skill.tags.map((tag) => (
                        <span key={tag} className="font-mono text-[10px] bg-surface-2 border border-hairline px-1.5 py-0.5 rounded text-ink-subtle">
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}
                </div>

                {/* Aliases */}
                {detail.aliases.length > 0 && (
                  <div>
                    <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1">aliases</div>
                    <div className="flex flex-wrap gap-2">
                      {detail.aliases.map((a: GlobalSkillAlias) => (
                        <span
                          key={a.alias}
                          className="font-mono text-[11px] bg-canvas border border-hairline rounded px-2 py-0.5 text-ink-muted"
                        >
                          @{a.alias} <span className="text-ink-tertiary">→ v{a.versionNo}</span>
                        </span>
                      ))}
                    </div>
                  </div>
                )}

                {/* Version list */}
                {detail.versions.length > 0 && (
                  <div>
                    <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1">versions</div>
                    <div className="flex flex-wrap gap-1">
                      {detail.versions.map((v: GlobalSkillVersion) => (
                        <button
                          key={v.versionNo}
                          onClick={() => setSelectedVersionNo(v.versionNo)}
                          className={clsx(
                            "font-mono text-[11px] px-2 py-0.5 rounded border transition-colors",
                            v.versionNo === selectedVersionNo
                              ? "bg-accent/15 border-accent/40 text-ink"
                              : "bg-canvas border-hairline text-ink-muted hover:bg-surface-2",
                          )}
                        >
                          v{v.versionNo}
                          {v.versionNo === detail.skill.latestVersion && (
                            <span className="ml-1 text-ink-tertiary">@latest</span>
                          )}
                        </button>
                      ))}
                    </div>
                    {/* Version metadata */}
                    {selectedVersionNo !== null &&
                      detail.versions.find((v) => v.versionNo === selectedVersionNo) && (() => {
                        const v = detail.versions.find((vv) => vv.versionNo === selectedVersionNo)!;
                        return (
                          <p className="mt-1 font-mono text-[10px] text-ink-tertiary">
                            {v.comment && `${v.comment} · `}
                            {v.createdBy && `by ${v.createdBy} · `}
                            {new Date(v.createdAt).toLocaleString()}
                          </p>
                        );
                      })()}
                  </div>
                )}

                {/* Version body */}
                <div>
                  <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1">
                    body{selectedVersionNo !== null ? ` · v${selectedVersionNo}` : ""}
                  </div>
                  {versionLoading && <p className="text-xs text-ink-subtle">loading…</p>}
                  {!versionLoading && !versionBody && (
                    <p className="text-xs text-ink-tertiary">(no version selected)</p>
                  )}
                  {!versionLoading && versionBody && (
                    <pre className="m-0 p-3 font-mono text-[11px] bg-canvas text-ink rounded border border-hairline overflow-x-auto max-h-96 whitespace-pre-wrap leading-relaxed">
                      {versionBody.body || "(empty body)"}
                    </pre>
                  )}
                </div>

              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}
