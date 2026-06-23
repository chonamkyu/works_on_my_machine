import { useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type { Credential, ConnectorInfo, Model } from "../lib/api";

interface OAuthProvider {
  id: string;
  configured: boolean;
  default_scopes: string[];
}

export function SettingsView() {
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [connectors, setConnectors] = useState<ConnectorInfo[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [oauthProviders, setOauthProviders] = useState<OAuthProvider[]>([]);
  const [form, setForm] = useState({ connector: "github", label: "default", secret: "" });
  const [busy, setBusy] = useState(false);

  const refresh = async () => {
    const [c, k, m, o] = await Promise.all([
      api.listCredentials(),
      api.listConnectors(),
      api.listModels(),
      api.listOAuthProviders(),
    ]);
    setCredentials(c.credentials ?? []);
    setConnectors(k.connectors);
    setModels(m.models);
    setOauthProviders(o.providers ?? []);
  };
  useEffect(() => {
    refresh().catch(console.error);
  }, []);

  // Listen for the success message from the OAuth callback popup.
  useEffect(() => {
    const onMsg = (e: MessageEvent) => {
      if (e.data && (e.data as { type?: string }).type === "platform-oauth-complete") {
        refresh().catch(console.error);
      }
    };
    window.addEventListener("message", onMsg);
    return () => window.removeEventListener("message", onMsg);
  }, []);

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

  const submit = async () => {
    if (!form.secret) return;
    setBusy(true);
    try {
      await api.createCredential(form);
      setForm({ ...form, secret: "" });
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const del = async (id: string) => {
    await api.deleteCredential(id);
    await refresh();
  };

  return (
    <section className="flex-1 flex flex-col min-w-0 overflow-y-auto">
      <header className="h-10 border-b border-surface-700 px-4 flex items-center text-text-muted shrink-0">
        <span className="font-mono text-xs uppercase tracking-wider">settings</span>
      </header>

      <div className="p-6 space-y-8 max-w-3xl">
        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-text-muted mb-2">
            Connect via OAuth
          </h2>
          <div className="flex flex-wrap gap-2">
            {oauthProviders.map((p) => {
              const connected = credentials.some((c) => c.connector === p.id && c.status === "active");
              return (
                <button
                  key={p.id}
                  onClick={() => void connectViaOAuth(p.id)}
                  disabled={!p.configured}
                  className={
                    "h-8 px-3 rounded border text-sm disabled:opacity-50 disabled:cursor-not-allowed " +
                    (connected
                      ? "border-status-running text-status-running hover:bg-status-running/10"
                      : "border-surface-700 hover:border-brand-600")
                  }
                  title={
                    p.configured
                      ? connected
                        ? "Already connected. Click to add another label."
                        : `Scopes: ${p.default_scopes.join(", ")}`
                      : `Set PLATFORM_${p.id.toUpperCase()}_CLIENT_ID/SECRET in .env`
                  }
                  data-testid={`oauth-${p.id}`}
                >
                  {connected ? "● " : ""}
                  {connected ? "Connected · " : "Connect with "}
                  <b>{p.id}</b>
                  {!p.configured && (
                    <span className="ml-2 text-[11px] text-text-dim">(client id not set)</span>
                  )}
                </button>
              );
            })}
          </div>
          <p className="mt-2 text-[11px] text-text-dim">
            Opens the provider's auth popup. Token is written straight into <code>credentials</code> on success.
            Configure providers in <code>.env</code> — see <code>PLATFORM_GITHUB_CLIENT_ID</code> etc.
          </p>
        </section>

        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-text-muted mb-2">
            Or paste a PAT
          </h2>
          <div className="flex items-end gap-2 flex-wrap">
            <Field label="Connector">
              <select
                value={form.connector}
                onChange={(e) => setForm({ ...form, connector: e.target.value })}
                className="bg-surface-850 border border-surface-700 rounded px-2 h-8 text-sm"
                data-testid="cred-connector"
              >
                {connectors.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Label">
              <input
                value={form.label}
                onChange={(e) => setForm({ ...form, label: e.target.value })}
                className="bg-surface-850 border border-surface-700 rounded px-2 h-8 text-sm w-32"
                data-testid="cred-label"
              />
            </Field>
            <Field label="Secret (token)">
              <input
                value={form.secret}
                onChange={(e) => setForm({ ...form, secret: e.target.value })}
                type="password"
                placeholder="ghp_… / xoxb-… / etc."
                className="bg-surface-850 border border-surface-700 rounded px-2 h-8 text-sm w-64"
                data-testid="cred-secret"
              />
            </Field>
            <button
              onClick={() => void submit()}
              disabled={busy || !form.secret}
              className="h-8 px-3 rounded bg-brand-600 text-white text-xs disabled:opacity-50"
              data-testid="cred-submit"
            >
              Save
            </button>
          </div>
          <p className="mt-1 text-[11px] text-text-dim">
            Stored AES-GCM encrypted. Injected into workflow runs as <code>PLATFORM_TOKEN_&lt;CONNECTOR&gt;</code>.
          </p>
        </section>

        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-text-muted mb-2">
            My credentials
          </h2>
          {credentials.length === 0 && (
            <p className="text-xs text-text-dim">
              None yet. Add one above to let workflows reach the corresponding service.
            </p>
          )}
          <div className="space-y-1">
            {credentials.map((c) => (
              <div
                key={c.id}
                className="flex items-center gap-3 border border-surface-700 rounded px-3 py-2"
                data-testid={`cred-row-${c.id}`}
              >
                <span className="font-mono text-xs text-text-secondary w-20">{c.connector}</span>
                <span className="text-sm">{c.label}</span>
                <span className="font-mono text-[11px] text-text-dim flex-1">{c.status}</span>
                <button
                  onClick={() => void del(c.id)}
                  className="p-1 rounded hover:bg-surface-700/40"
                  aria-label="Delete"
                >
                  <Trash2 className="w-3.5 h-3.5 text-text-muted" />
                </button>
              </div>
            ))}
          </div>
        </section>

        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-text-muted mb-2">
            Connectors catalog
          </h2>
          <div className="grid grid-cols-2 gap-2">
            {connectors.map((c) => (
              <div key={c.id} className="border border-surface-700 rounded p-3">
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-sm">{c.name}</span>
                  <span className="font-mono text-[10px] text-text-dim">{c.auth}</span>
                </div>
                <div className="font-mono text-[11px] text-text-muted">
                  fns: {c.functions.slice(0, 3).join(", ")}
                  {c.functions.length > 3 && "…"}
                </div>
                <div className="font-mono text-[11px] text-text-muted">
                  triggers: {c.triggers.join(", ") || "—"}
                </div>
              </div>
            ))}
          </div>
        </section>

        <section>
          <h2 className="font-mono text-[11px] uppercase tracking-wider text-text-muted mb-2">
            Models
          </h2>
          <div className="space-y-1">
            {models.map((m) => (
              <div key={m.id} className="flex items-center gap-3 border border-surface-700 rounded px-3 py-2">
                <span className="text-sm font-mono">{m.id}</span>
                <span className="font-mono text-[11px] text-text-dim">{m.tier}</span>
                {m.use && (
                  <span className="font-mono text-[11px] text-text-muted ml-auto">use: {m.use}</span>
                )}
              </div>
            ))}
          </div>
        </section>
      </div>
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="font-mono text-[10px] uppercase tracking-wider text-text-muted">
        {label}
      </span>
      {children}
    </label>
  );
}
