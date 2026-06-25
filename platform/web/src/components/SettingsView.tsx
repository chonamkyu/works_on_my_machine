import { useEffect, useState } from "react";
import { Trash2, KeyRound, Plus, X } from "lucide-react";
import { api } from "../lib/api";
import type { Credential, Model } from "../lib/api";

interface OAuthProvider {
  id: string;
  configured: boolean;
  default_scopes: string[];
}

interface Preset {
  id: string;
  label: string;
  description: string;
  schema: Array<{ key: string; placeholder: string; required?: boolean; secret?: boolean }>;
  oauth?: boolean;
}

const PRESETS: Preset[] = [
  {
    id: "slack",
    label: "Slack",
    description: "Bot + user tokens via OAuth (recommended) or paste manually.",
    oauth: true,
    schema: [
      { key: "bot_token", placeholder: "xoxb-…", secret: true },
      { key: "user_token", placeholder: "xoxp-… (optional)", secret: true },
    ],
  },
  {
    id: "github",
    label: "GitHub",
    description: "Personal access token or OAuth.",
    oauth: true,
    schema: [{ key: "token", placeholder: "ghp_…", required: true, secret: true }],
  },
  {
    id: "openai",
    label: "OpenAI",
    description: "API key.",
    schema: [{ key: "api_key", placeholder: "sk-…", required: true, secret: true }],
  },
  {
    id: "aws",
    label: "AWS",
    description: "Access keys for SDK auth.",
    schema: [
      { key: "access_key_id", placeholder: "AKIA…", required: true },
      { key: "secret_access_key", placeholder: "…", required: true, secret: true },
      { key: "region", placeholder: "us-east-1" },
    ],
  },
  {
    id: "notion",
    label: "Notion",
    description: "Integration token.",
    schema: [{ key: "token", placeholder: "secret_…", required: true, secret: true }],
  },
  {
    id: "linear",
    label: "Linear",
    description: "API key.",
    schema: [{ key: "api_key", placeholder: "lin_api_…", required: true, secret: true }],
  },
  {
    id: "unsplash",
    label: "Unsplash",
    description: "Access key from https://unsplash.com/oauth/applications.",
    schema: [{ key: "access_key", placeholder: "…", required: true, secret: true }],
  },
  {
    id: "custom",
    label: "Custom (JSON)",
    description: "Any shape — raw JSON editor.",
    schema: [],
  },
];

export function SettingsView() {
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [oauthProviders, setOauthProviders] = useState<OAuthProvider[]>([]);
  const [modalPreset, setModalPreset] = useState<Preset | null>(null);

  const refresh = async () => {
    const [c, m, o] = await Promise.all([
      api.listCredentials(),
      api.listModels(),
      api.listOAuthProviders(),
    ]);
    setCredentials(c.credentials ?? []);
    setModels(m.models);
    setOauthProviders(o.providers ?? []);
  };

  useEffect(() => {
    refresh().catch(console.error);
  }, []);

  useEffect(() => {
    const onMsg = (e: MessageEvent) => {
      if (e.data && (e.data as { type?: string }).type === "platform-oauth-complete") {
        refresh().catch(console.error);
      }
    };
    window.addEventListener("message", onMsg);
    return () => window.removeEventListener("message", onMsg);
  }, []);

  const del = async (id: string) => {
    await api.deleteCredential(id);
    await refresh();
  };

  const connectViaOAuth = async (provider: string) => {
    const label = window.prompt(`Label for this ${provider} connection?`, "default");
    if (!label) return;
    try {
      const { url } = await api.startOAuth(provider, label);
      window.open(url, "_blank", "width=720,height=820");
    } catch (err) {
      alert((err as Error).message);
    }
  };

  return (
    <section className="flex-1 flex flex-col min-w-0 overflow-y-auto">
      <header className="h-10 border-b border-hairline px-4 flex items-center text-ink-subtle shrink-0">
        <span className="font-mono text-xs uppercase tracking-wider">credential store</span>
      </header>

      <div className="p-6 space-y-8 max-w-3xl">
        <section>
          <div className="flex items-center justify-between mb-2">
            <h2 className="font-mono text-[11px] uppercase tracking-wider text-ink-subtle">
              Add credential
            </h2>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
            {PRESETS.map((p) => {
              const oauth = oauthProviders.find((x) => x.id === p.id);
              const oauthConfigured = !!oauth?.configured;
              return (
                <button
                  key={p.id}
                  onClick={() => {
                    if (p.oauth && oauthConfigured) {
                      void connectViaOAuth(p.id);
                    } else {
                      setModalPreset(p);
                    }
                  }}
                  className="text-left h-20 px-3 py-2 rounded border border-hairline hover:border-accent transition"
                  data-testid={`preset-${p.id}`}
                >
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{p.label}</span>
                    {p.oauth && (
                      <span
                        className={
                          "font-mono text-[10px] " +
                          (oauthConfigured ? "text-status-running" : "text-ink-tertiary")
                        }
                      >
                        {oauthConfigured ? "OAuth" : "OAuth · n/a"}
                      </span>
                    )}
                  </div>
                  <p className="mt-1 text-[11px] text-ink-subtle line-clamp-2">{p.description}</p>
                </button>
              );
            })}
          </div>
        </section>

        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-ink-subtle mb-2">
            My credentials
          </h2>
          {credentials.length === 0 && (
            <p className="text-xs text-ink-tertiary">
              No credentials yet. Pick a preset above (or "Custom") to add one.
            </p>
          )}
          <div className="space-y-1">
            {credentials.map((c) => (
              <div
                key={c.id}
                className="flex items-center gap-3 border border-hairline rounded px-3 py-2"
                data-testid={`cred-row-${c.id}`}
              >
                <KeyRound className="w-3.5 h-3.5 text-ink-subtle" />
                <span className="font-mono text-xs text-ink-muted w-20 truncate">
                  {c.connector}
                </span>
                <span className="text-sm">{c.label}</span>
                <span className="font-mono text-[11px] text-ink-tertiary flex-1">{c.status}</span>
                <button
                  onClick={() => void del(c.id)}
                  className="p-1 rounded hover:bg-surface-2"
                  aria-label="Delete"
                >
                  <Trash2 className="w-3.5 h-3.5 text-ink-subtle" />
                </button>
              </div>
            ))}
          </div>
          <p className="mt-2 text-[11px] text-ink-tertiary">
            Stored AES-GCM encrypted. Injected into workflow runs as
            <code className="mx-1">PLATFORM_TOKEN_&lt;CONNECTOR&gt;</code>(primary value) +
            <code className="mx-1">PLATFORM_CREDENTIAL_&lt;CONNECTOR&gt;</code>(full JSON).
          </p>
        </section>

        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-ink-subtle mb-2">
            Models
          </h2>
          <div className="space-y-1">
            {models.map((m) => (
              <div key={m.id} className="flex items-center gap-3 border border-hairline rounded px-3 py-2">
                <span className="text-sm font-mono">{m.id}</span>
                <span className="font-mono text-[11px] text-ink-tertiary">{m.tier}</span>
                {m.use && (
                  <span className="font-mono text-[11px] text-ink-subtle ml-auto">use: {m.use}</span>
                )}
              </div>
            ))}
          </div>
        </section>
      </div>

      {modalPreset && (
        <CredentialModal
          preset={modalPreset}
          onClose={() => setModalPreset(null)}
          onSaved={async () => {
            await refresh();
            setModalPreset(null);
          }}
        />
      )}
    </section>
  );
}

function CredentialModal({
  preset,
  onClose,
  onSaved,
}: {
  preset: Preset;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const isCustom = preset.id === "custom";
  const [label, setLabel] = useState("default");
  const [connectorName, setConnectorName] = useState(isCustom ? "" : preset.id);
  const [fields, setFields] = useState<Record<string, string>>(
    Object.fromEntries(preset.schema.map((s) => [s.key, ""])),
  );
  const [rawJson, setRawJson] = useState<string>("{\n  \n}");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const save = async () => {
    setErr(null);
    setBusy(true);
    try {
      let value: Record<string, unknown>;
      if (isCustom) {
        try {
          value = JSON.parse(rawJson);
        } catch (e) {
          throw new Error(`invalid JSON: ${(e as Error).message}`);
        }
      } else {
        value = {};
        for (const s of preset.schema) {
          const v = fields[s.key]?.trim();
          if (v) value[s.key] = v;
          if (s.required && !v) throw new Error(`${s.key} required`);
        }
      }
      await api.createCredential({
        connector: connectorName || preset.id,
        label: label || "default",
        value,
      });
      await onSaved();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-[min(560px,92vw)] rounded-lg border border-hairline bg-surface-3 shadow-2xl">
        <div className="h-11 px-4 border-b border-hairline flex items-center gap-3">
          <KeyRound className="w-3.5 h-3.5 text-ink-subtle" />
          <span className="text-sm font-medium">Add {preset.label} credential</span>
          <div className="flex-1" />
          <button onClick={onClose} className="p-1 rounded hover:bg-surface-3/60">
            <X className="w-4 h-4 text-ink-subtle" />
          </button>
        </div>
        <div className="p-4 space-y-3">
          <p className="text-xs text-ink-subtle">{preset.description}</p>

          <div className="flex gap-2">
            {isCustom && (
              <label className="flex flex-col gap-1 flex-1">
                <span className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
                  Connector name
                </span>
                <input
                  value={connectorName}
                  onChange={(e) => setConnectorName(e.target.value)}
                  placeholder="my-service"
                  className="bg-surface-1 border border-hairline rounded px-2 h-8 text-sm"
                />
              </label>
            )}
            <label className="flex flex-col gap-1 flex-1">
              <span className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
                Label
              </span>
              <input
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                className="bg-surface-1 border border-hairline rounded px-2 h-8 text-sm"
              />
            </label>
          </div>

          {isCustom ? (
            <label className="flex flex-col gap-1">
              <span className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
                JSON value
              </span>
              <textarea
                value={rawJson}
                onChange={(e) => setRawJson(e.target.value)}
                rows={10}
                spellCheck={false}
                className="font-mono text-[12px] bg-surface-1 border border-hairline rounded p-2"
                placeholder={`{\n  "endpoint": "https://...",\n  "api_key": "..."\n}`}
              />
            </label>
          ) : (
            <div className="space-y-2">
              {preset.schema.map((s) => (
                <label key={s.key} className="flex flex-col gap-1">
                  <span className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
                    {s.key}
                    {s.required && <span className="text-status-failed ml-1">*</span>}
                  </span>
                  <input
                    type={s.secret ? "password" : "text"}
                    value={fields[s.key] ?? ""}
                    onChange={(e) => setFields({ ...fields, [s.key]: e.target.value })}
                    placeholder={s.placeholder}
                    className="bg-surface-1 border border-hairline rounded px-2 h-8 text-sm"
                  />
                </label>
              ))}
            </div>
          )}

          {err && <p className="text-xs text-status-failed">{err}</p>}

          <div className="flex gap-2 pt-2">
            <button
              onClick={() => void save()}
              disabled={busy}
              className="h-8 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
            >
              <Plus className="w-3 h-3 inline mr-1" />
              {busy ? "saving…" : "Save"}
            </button>
            <button
              onClick={onClose}
              className="h-8 px-3 rounded border border-hairline text-xs"
            >
              Cancel
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
