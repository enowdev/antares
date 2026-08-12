import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  CaretDown,
  CircleNotch,
  Cpu,
  MagnifyingGlass,
} from "@phosphor-icons/react";
import { get, isDashboardPasswordRequired, post } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import type {
  ChatModelSelection,
  ReasoningCapability,
} from "@/lib/models";
import { cn } from "@/lib/utils";

interface AllModel {
  id: string;
  name: string;
  provider: string;
  provider_label: string;
  reasoning_capability?: ReasoningCapability;
}

interface ListAll {
  active: { model: string; provider: string };
  models: AllModel[];
}

interface ModelOptions {
  active: { model: string; provider: string };
  providers?: Array<{ id: string; label: string }>;
}

interface ModelInfo {
  found: boolean;
  id?: string;
  name?: string;
  reasoning_capability?: ReasoningCapability;
}

function modelSelection(model: AllModel): ChatModelSelection {
  return {
    provider: model.provider,
    model: model.id,
    name: model.name,
    providerLabel: model.provider_label,
    reasoningCapability: model.reasoning_capability,
  };
}

/**
 * Switch the active model straight from the composer, without leaving the chat.
 * Lists every connected provider's models (same source as the Models page) and
 * sets provider+model together, since a model always knows its provider.
 */
export function ModelPicker({
  onModelChange,
}: {
  onModelChange?: (selection: ChatModelSelection) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [data, setData] = useState<ListAll | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState("");
  const [pickError, setPickError] = useState<string>();
  const [pickGate, setPickGate] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const resolutionRef = useRef(0);

  const [activeConfig, setActiveConfig] = useState<{
    model: string;
    provider: string;
  } | null>(null);

  const load = () => {
    setLoading(true);
    return get<ListAll>("/model/list-all")
      .then((d) => {
        setData(d);
        const activeModel = d.models.find(
          (model) =>
            model.id === d.active?.model &&
            model.provider === d.active?.provider,
        );
        if (activeModel) onModelChange?.(modelSelection(activeModel));
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  // The cheap options call identifies the persisted active pair. Resolve that
  // one model through model-info so the composer has capability metadata before
  // it restores a model-scoped reasoning preference.
  useEffect(() => {
    let cancelled = false;
    const sequence = ++resolutionRef.current;
    get<ModelOptions>("/model/options")
      .then(async (options) => {
        if (cancelled || sequence !== resolutionRef.current) return;
        const active = options.active;
        setActiveConfig(active);
        if (!active?.model || !active?.provider) return;

        const providerLabel =
          options.providers?.find((provider) => provider.id === active.provider)
            ?.label ?? active.provider;
        const fallback: ChatModelSelection = {
          provider: active.provider,
          model: active.model,
          name: active.model,
          providerLabel,
        };

        try {
          const info = await get<ModelInfo>(
            `/providers/${encodeURIComponent(active.provider)}/model-info?model=${encodeURIComponent(active.model)}`,
          );
          if (cancelled || sequence !== resolutionRef.current) return;
          onModelChange?.({
            ...fallback,
            name: info.found ? info.name || active.model : active.model,
            reasoningCapability: info.found
              ? info.reasoning_capability
              : undefined,
          });
        } catch {
          if (!cancelled && sequence === resolutionRef.current) {
            onModelChange?.(fallback);
          }
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [onModelChange]);

  // The full model list (which probes every provider) is fetched lazily on open,
  // and refreshed each open so a newly connected provider's models appear.
  useEffect(() => {
    if (open) load();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node))
        setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  // Prefer the freshly-probed list's active; fall back to the cheap mount fetch.
  const active = data?.active ?? activeConfig;
  const activeLabel = active?.model || t("models.pickModel");

  const shown = useMemo(() => {
    const list = data?.models ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return list;
    return list.filter(
      (m) =>
        m.id.toLowerCase().includes(q) ||
        m.name.toLowerCase().includes(q) ||
        m.provider_label.toLowerCase().includes(q),
    );
  }, [data, query]);

  const pick = async (m: AllModel) => {
    ++resolutionRef.current;
    setSaving(`${m.provider}/${m.id}`);
    setPickError(undefined);
    try {
      await post("/model/set", { model: m.id, provider: m.provider });
      setActiveConfig({ model: m.id, provider: m.provider });
      setData((d) =>
        d ? { ...d, active: { model: m.id, provider: m.provider } } : d,
      );
      onModelChange?.(modelSelection(m));
      setOpen(false);
      setQuery("");
    } catch (e) {
      const gate = isDashboardPasswordRequired(e);
      setPickGate(gate);
      setPickError(
        gate
          ? t("sensitive.needPasswordDesc")
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setSaving("");
    }
  };

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => {
          setPickError(undefined);
          setOpen((v) => !v);
        }}
        className="flex h-8 items-center gap-1.5 rounded-[var(--radius-md)] border border-border bg-card px-2.5 text-xs transition-colors hover:border-primary/40"
      >
        <Cpu className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="hidden max-w-32 truncate sm:inline">
          {activeLabel}
        </span>
        <CaretDown className="size-3 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute bottom-full left-0 z-30 mb-2 flex max-h-80 w-72 flex-col rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg">
          <div className="relative p-1">
            <MagnifyingGlass className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("models.searchAll")}
              className="h-8 w-full rounded-[var(--radius-sm)] border border-border bg-background pl-8 pr-2 text-xs outline-none focus:border-ring"
            />
          </div>
          {pickError ? (
            <p
              role="alert"
              className="mx-1 mb-1 rounded-sm border border-destructive/30 bg-destructive/5 px-2.5 py-2 text-[11px] leading-snug text-destructive"
            >
              {pickError}{" "}
              {pickGate ? (
                <Link
                  to="/config"
                  className="font-medium underline underline-offset-2"
                >
                  {t("sensitive.setPassword")}
                </Link>
              ) : null}
            </p>
          ) : null}
          <div className="min-h-0 flex-1 overflow-y-auto">
            {shown.length === 0 && loading ? (
              <div className="flex items-center justify-center gap-2 px-2.5 py-4 text-xs text-muted-foreground">
                <CircleNotch className="size-3.5 animate-spin" />
                {t("models.loading")}
              </div>
            ) : shown.length === 0 ? (
              <p className="px-2.5 py-4 text-center text-xs text-muted-foreground">
                {t("models.none")}
              </p>
            ) : (
              shown.map((m) => {
                const isActive =
                  m.id === active?.model && m.provider === active?.provider;
                return (
                  <button
                    key={`${m.provider}/${m.id}`}
                    onClick={() => pick(m)}
                    disabled={!!saving}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-sm px-2.5 py-1.5 text-left transition-colors hover:bg-muted",
                      isActive && "bg-primary/5",
                    )}
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">
                        {m.name}
                      </span>
                      <span className="block truncate font-mono text-[10px] text-muted-foreground">
                        {m.id} · {m.provider_label}
                      </span>
                    </span>
                    {isActive ? (
                      <span className="shrink-0 text-[10px] font-medium text-primary">
                        {t("common.active")}
                      </span>
                    ) : null}
                  </button>
                );
              })
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
