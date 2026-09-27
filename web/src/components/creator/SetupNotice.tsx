import { Link } from "react-router-dom";
import { CheckCircle, WarningCircle } from "@phosphor-icons/react";
import type { CreatorSettings } from "@/lib/creator";
import { cn } from "@/lib/utils";

/**
 * One line on media readiness. Model settings live in the shell's gear sheet
 * (`?settings=1`), so this only reports and links there.
 */
export function SetupNotice({ settings }: { settings: CreatorSettings | null }) {
  if (!settings) return null;
  const issues: string[] = [];
  for (const [kind, label] of [
    ["image", "Image"],
    ["video", "Video"],
  ] as const) {
    const s = settings[kind];
    if (!s.enabled) issues.push(`${label} generation is off`);
    else if (!s.has_key) issues.push(`${label} generation has no credential`);
  }
  if (!settings.ffmpeg) issues.push("FFmpeg not installed");
  if (!settings.ffprobe) issues.push("FFprobe not installed");
  const ok = issues.length === 0;
  const text = ok
    ? "Image and video generation ready · FFmpeg and FFprobe available"
    : issues.join(" · ");
  const Icon = ok ? CheckCircle : WarningCircle;
  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-x-2 text-xs",
        ok ? "text-muted-foreground" : "text-foreground",
      )}
    >
      <Icon
        aria-hidden
        weight={ok ? "regular" : "fill"}
        className={cn(
          "size-4 shrink-0",
          ok ? "text-muted-foreground" : "text-[var(--warning)]",
        )}
      />
      <p className="min-w-0 flex-1 py-1 lg:flex-initial">
        {text}
        {settings.ffmpeg && settings.ffprobe && !ok ? (
          <span className="text-muted-foreground"> · FFmpeg and FFprobe available</span>
        ) : null}
      </p>
      <Link
        to="?settings=1"
        className="inline-flex min-h-11 items-center font-medium text-primary underline-offset-4 hover:underline lg:min-h-0"
      >
        Configure
      </Link>
    </div>
  );
}
