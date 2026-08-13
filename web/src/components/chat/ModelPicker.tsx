import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  CaretDown,
  CircleNotch,
  Cloud,
  Cpu,
  MagnifyingGlass,
} from "@phosphor-icons/react";
import { get, isDashboardPasswordRequired, post } from "@/lib/api";
import {
  chatTargetFromModel,
  composerTargetKey,
  composerTargetLabel,
  cursorCatalogueState,
  searchComposerTargets,
  type ChatCatalogueModel,
  type ChatTarget,
  type ComposerTarget,
  type CursorTarget,
} from "@/lib/composerTargets";
import type { CursorModel } from "@/lib/cursorModels";
import { cursorVariantSummary, defaultCursorVariant } from "@/lib/cursorModels";
import { useI18n } from "@/lib/i18n";
import type { ReasoningCapability } from "@/lib/models";
import { cn } from "@/lib/utils";

interface ListAll {
  active: { model: string; provider: string };
  models: ChatCatalogueModel[];
}

interface CursorCatalogue {
  models: CursorModel[];
  needs_key?: boolean;
  error?: string;
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

/**
 * Pick where the next message runs, without leaving the chat. Chat models and
 * Cursor Cloud Agents are searched together but stay separate targets: picking
 * a chat model sets the active Antares model, while picking a Cursor model only
 * routes this conversation's turns to Cursor and never touches `/model/set`.
 *
 * `onChange` reports how the target was chosen. The mount-time active-model
 * lookup is a `default`, not an edit: only the composer knows whether a session
 * being restored has a better claim on the target, so it decides what to do
 * with it.
 */
export function ModelPicker({
  value,
  onChange,
  disabled,
}: {
  value: ComposerTarget | null;
  onChange: (target: ComposerTarget, origin: "user" | "default") => void;
  /** Locked while a turn streams: the running stream owns the target. */
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [data, setData] = useState<ListAll | null>(null);
  const [cursorData, setCursorData] = useState<CursorCatalogue>();
  const [cursorError, setCursorError] = useState<Error>();
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

  // Read the callback through a ref so the mount resolution below does not
  // re-run (and re-fetch) every time the composer hands down a new target.
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const adoptChatDefault = useCallback((target: ChatTarget) => {
    onChangeRef.current(target, "default");
  }, []);

  const load = () => {
    setLoading(true);
    const chat = get<ListAll>("/model/list-all")
      .then((d) => {
        setData(d);
        const activeModel = d.models.find(
          (model) =>
            model.id === d.active?.model &&
            model.provider === d.active?.provider,
        );
        if (activeModel) adoptChatDefault(chatTargetFromModel(activeModel));
      })
      .catch(() => {});
    // Cursor's catalogue is a separate call on purpose: it is never merged into
    // the chat model list, and a Cursor failure must not hide chat models.
    const cursor = get<CursorCatalogue>("/providers/cursor/models")
      .then((d) => {
        setCursorData(d);
        setCursorError(undefined);
      })
      .catch((e: Error) => {
        setCursorData(undefined);
        setCursorError(e);
      });
    return Promise.all([chat, cursor]).finally(() => setLoading(false));
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
        const fallback: ChatTarget = {
          kind: "chat",
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
          adoptChatDefault({
            ...fallback,
            name: info.found ? info.name || active.model : active.model,
            reasoningCapability: info.found
              ? info.reasoning_capability
              : undefined,
          });
        } catch {
          if (!cancelled && sequence === resolutionRef.current) {
            adoptChatDefault(fallback);
          }
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [adoptChatDefault]);

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
  const chipLabel =
    composerTargetLabel(value) || active?.model || t("models.pickModel");
  const cursorState = cursorCatalogueState(cursorData, cursorError);
  const cursorMessage = cursorData?.error ?? cursorError?.message;

  const shown = useMemo(
    () =>
      searchComposerTargets({
        chatModels: data?.models ?? [],
        cursorModels: cursorData?.models ?? [],
        query,
      }),
    [data, cursorData, query],
  );
  const selectedKey = value ? composerTargetKey(value) : "";

  const pickChat = async (target: ChatTarget) => {
    ++resolutionRef.current;
    setSaving(composerTargetKey(target));
    setPickError(undefined);
    try {
      await post("/model/set", {
        model: target.model,
        provider: target.provider,
      });
      setActiveConfig({ model: target.model, provider: target.provider });
      setData((d) =>
        d
          ? { ...d, active: { model: target.model, provider: target.provider } }
          : d,
      );
      onChange(target, "user");
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

  // Cursor is an execution target, not a chat provider: selecting one changes
  // only this composer, so there is nothing to save and nothing to fail.
  const pickCursor = (target: CursorTarget) => {
    setPickError(undefined);
    onChange(target, "user");
    setOpen(false);
    setQuery("");
  };

  const empty =
    shown.chat.length === 0 && shown.cursor.length === 0 && cursorState !== "connect";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => {
          setPickError(undefined);
          setOpen((v) => !v);
        }}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={disabled ? t("target.lockedWhileStreaming") : chipLabel}
        className="flex h-8 items-center gap-1.5 rounded-[var(--radius-md)] border border-border bg-card px-2.5 text-xs transition-colors hover:border-primary/40 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {value?.kind === "cursor" ? (
          <Cloud className="size-3.5 shrink-0 text-primary" />
        ) : (
          <Cpu className="size-3.5 shrink-0 text-muted-foreground" />
        )}
        <span className="hidden max-w-32 truncate sm:inline">{chipLabel}</span>
        <CaretDown className="size-3 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div
          aria-label={t("models.pickModel")}
          className="absolute bottom-full left-0 z-30 mb-2 flex max-h-96 w-80 flex-col rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg"
        >
          <div className="relative p-1">
            <MagnifyingGlass className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("models.searchAll")}
              aria-label={t("models.searchAll")}
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
            {empty && loading ? (
              <div className="flex items-center justify-center gap-2 px-2.5 py-4 text-xs text-muted-foreground">
                <CircleNotch className="size-3.5 animate-spin" />
                {t("models.loading")}
              </div>
            ) : empty ? (
              <p className="px-2.5 py-4 text-center text-xs text-muted-foreground">
                {t("models.none")}
              </p>
            ) : null}

            {shown.chat.length > 0 ? (
              <GroupHeading
                icon={<Cpu className="size-3" />}
                label={t("target.chatGroup")}
              />
            ) : null}
            {shown.chat.map((target) => {
              const key = composerTargetKey(target);
              const isActive =
                target.model === active?.model &&
                target.provider === active?.provider;
              return (
                <button
                  key={key}
                  aria-current={key === selectedKey}
                  onClick={() => pickChat(target)}
                  disabled={!!saving}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-sm px-2.5 py-1.5 text-left transition-colors hover:bg-muted",
                    key === selectedKey && "bg-primary/5",
                  )}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-xs font-medium">
                      {target.name}
                    </span>
                    <span className="block truncate font-mono text-[10px] text-muted-foreground">
                      {target.model} · {target.providerLabel}
                    </span>
                  </span>
                  {isActive ? (
                    <span className="shrink-0 text-[10px] font-medium text-primary">
                      {t("common.active")}
                    </span>
                  ) : null}
                </button>
              );
            })}

            {cursorState === "connect" || shown.cursor.length > 0 ? (
              <GroupHeading
                icon={<Cloud className="size-3" />}
                label={t("target.cursorGroup")}
              />
            ) : null}
            {cursorState === "connect" ? (
              <div className="px-2.5 py-2 text-[11px] leading-snug text-muted-foreground">
                <p>{t("target.cursorNeedsKey")}</p>
                <Link
                  to="/providers"
                  onClick={() => setOpen(false)}
                  className="mt-1 inline-block font-medium text-primary underline underline-offset-2"
                >
                  {t("target.cursorConnect")}
                </Link>
              </div>
            ) : null}
            {cursorState === "error" && cursorMessage ? (
              <p
                role="alert"
                className="mx-1 my-1 rounded-sm border border-destructive/30 bg-destructive/5 px-2.5 py-2 text-[11px] leading-snug text-destructive"
              >
                {cursorMessage}
              </p>
            ) : null}
            {shown.cursor.map(({ model, target }) => {
              const key = `cursor:${model.id}`;
              const variant = defaultCursorVariant(model);
              const summary = variant ? cursorVariantSummary(model, variant) : "";
              return (
                <button
                  key={key}
                  // A model the catalogue returned no variant for cannot be run
                  // at all: it is listed so its absence is explained, but there
                  // is no selection to make.
                  disabled={!target}
                  aria-current={key === selectedKey}
                  onClick={() => target && pickCursor(target)}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-sm px-2.5 py-1.5 text-left transition-colors hover:bg-muted",
                    key === selectedKey && "bg-primary/5",
                    !target && "cursor-not-allowed opacity-60 hover:bg-transparent",
                  )}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-xs font-medium">
                      {model.name}
                    </span>
                    <span className="block truncate font-mono text-[10px] text-muted-foreground">
                      {model.id} · {t("target.cursorRow")}
                    </span>
                    {target ? (
                      summary ? (
                        <span className="block truncate text-[10px] text-muted-foreground">
                          {summary}
                        </span>
                      ) : null
                    ) : (
                      <span className="block text-[10px] leading-snug text-[var(--warning)]">
                        {t("target.cursorNoVariant")}
                      </span>
                    )}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function GroupHeading({
  icon,
  label,
}: {
  icon: React.ReactNode;
  label: string;
}) {
  return (
    <div className="mt-1 flex items-center gap-1.5 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
      {icon}
      {label}
    </div>
  );
}
