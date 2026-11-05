import type { ReactNode } from "react";
import { Badge } from "@/components/ui/primitives";
import { cn } from "@/lib/utils";

/** Native select styled like the Input primitive (same height and radius). */
export const control =
  "h-11 w-full rounded-full border border-input bg-transparent px-4 text-base sm:h-9 sm:text-sm disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring";

/** Inputs grow to a 44px tap target on phones and match `control` above. */
export const inputTap = "h-11 sm:h-9";

/** Small buttons keep a 44px tap target until the desktop breakpoint. */
export const tapSm = "h-11 lg:h-8";
export const tapIcon = "size-11 lg:size-8";

export function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("grid content-start gap-1.5 text-sm", className)}>
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}

export function Failure({ text }: { text?: string }) {
  return text ? (
    <pre
      role="alert"
      className="m-rise whitespace-pre-wrap break-words rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 font-mono text-xs text-destructive"
    >
      {JSON.stringify({ error: text }, null, 2)}
    </pre>
  ) : null;
}

/** Status strings come straight from the Go service (snake_case). */
export function StatusBadge({ status }: { status: string }) {
  const variant =
    status === "ready" || status === "published"
      ? "success"
      : status === "error" || status === "blocked"
        ? "destructive"
        : status === "submission_unknown" || status === "uploading"
          ? "warning"
          : status.startsWith("generating") || status === "polling"
            ? "default"
            : "outline";
  return <Badge variant={variant}>{status.replaceAll("_", " ")}</Badge>;
}

/**
 * In-pane tab strip. Same look as the hub tabs in the shell header so the two
 * levels read as one system; the pane tabs sit under the project title.
 */
export function PaneTabs<T extends string>({
  tabs,
  value,
  onChange,
}: {
  tabs: { id: T; label: string; count?: number }[];
  value: T;
  onChange: (id: T) => void;
}) {
  return (
    <div className="-mx-4 overflow-x-auto border-b border-border px-4 pb-2 [scrollbar-width:none] sm:mx-0 sm:px-0 [&::-webkit-scrollbar]:hidden">
      <div className="flex w-max items-center gap-1">
        {tabs.map((t) => {
          const active = t.id === value;
          return (
            <button
              key={t.id}
              type="button"
              aria-pressed={active}
              onClick={() => onChange(t.id)}
              className={cn(
                "inline-flex h-11 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-4 text-xs transition-colors lg:h-8 lg:px-3.5",
                active
                  ? "border-transparent bg-nav-active text-foreground"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
              {t.count ? (
                <span
                  className={cn(
                    "font-mono text-[11px] tabular-nums",
                    active ? "text-foreground" : "text-dim",
                  )}
                >
                  {t.count}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>
    </div>
  );
}
