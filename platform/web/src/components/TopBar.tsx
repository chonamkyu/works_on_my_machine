import { Cog, Search } from "lucide-react";

interface Props {
  onOpenPalette: () => void;
  onOpenSettings: () => void;
}

/**
 * Minimal top bar — the previous tab strip is gone; navigation lives in the
 * ⌘K command palette and the task sidebar. We keep three things here only:
 *   - wordmark anchor on the left
 *   - the ⌘K affordance so first-time users discover the palette
 *   - status (activity counter) + settings (cog) on the right
 */
export function TopBar({ onOpenPalette, onOpenSettings }: Props) {
  return (
    <header className="h-11 border-b border-hairline bg-canvas flex items-center px-3 gap-3 text-ink-muted">
      <span className="font-mono text-[11px] tracking-[0.14em] text-ink-subtle select-none">
        works_on_my_machine
      </span>
      <div className="flex-1" />
      <button
        onClick={onOpenPalette}
        className="group h-7 px-2.5 rounded-md border border-hairline bg-surface-1 hover:border-hairline-strong flex items-center gap-2 text-[12px] text-ink-subtle"
        data-testid="open-palette"
      >
        <Search className="w-3 h-3" />
        <span>Search or jump…</span>
        <kbd className="font-mono text-[10px] text-ink-tertiary border border-hairline rounded px-1 py-px ml-3">⌘K</kbd>
      </button>
      <button
        onClick={onOpenSettings}
        className="h-7 w-7 rounded-md hover:bg-surface-2 flex items-center justify-center text-ink-subtle hover:text-ink"
        title="Settings · ⌘,"
        data-testid="open-settings"
      >
        <Cog className="w-3.5 h-3.5" />
      </button>
      <span className="font-mono text-[11px] text-ink-tertiary pl-1">@nk</span>
    </header>
  );
}
