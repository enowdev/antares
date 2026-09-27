import { CheckCircle, Circle, CircleNotch, Play } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/primitives";
import type { Project, Stage } from "@/lib/creator";
import { cn } from "@/lib/utils";
import { tapIcon } from "./parts";

type Step = Exclude<Stage, "full">;

const STEPS: { stage: Step; label: string }[] = [
  { stage: "research", label: "Research" },
  { stage: "plan", label: "Plan" },
  { stage: "produce", label: "Produce" },
  { stage: "publish", label: "Publish" },
];

const plural = (n: number, one: string, many = `${one}s`) =>
  `${n} ${n === 1 ? one : many}`;

/** Short, factual status for one stage, read from fields the project already has. */
function describe(p: Project, stage: Step): { text: string; done: boolean } {
  switch (stage) {
    case "research":
      return p.research.length
        ? { text: plural(p.research.length, "trend"), done: true }
        : { text: "Not run yet", done: false };
    case "plan":
      return p.shots.length || p.ideas.length
        ? {
            text: `${plural(p.ideas.length, "idea")} · ${plural(p.shots.length, "shot")}`,
            done: p.shots.length > 0,
          }
        : { text: "No plan yet", done: false };
    case "produce": {
      if (p.final_path) return { text: "Final video assembled", done: true };
      if (!p.shots.length) return { text: "No shots", done: false };
      const ready = p.shots.filter((s) => s.status === "ready").length;
      return { text: `${ready}/${p.shots.length} clips ready`, done: false };
    }
    case "publish": {
      const status = p.publication.status;
      if (status === "published") return { text: "Published", done: true };
      if (status === "uploading") return { text: "Uploading", done: false };
      if (status === "blocked") return { text: "Blocked, check account", done: false };
      if (p.publish_mode !== "auto") return { text: "Draft only", done: false };
      return {
        text: p.final_path ? "Ready to publish" : "Needs final video",
        done: false,
      };
    }
  }
}

/**
 * Research → Plan → Produce → Publish, each with its state and its own run
 * button, plus one Run all. The enable rules are the page's; this only lays
 * them out.
 */
export function PipelineStrip({
  project,
  busy,
  isDisabled,
  onRun,
}: {
  project: Project;
  busy: string;
  isDisabled: (stage: Stage) => boolean;
  onRun: (stage: Stage) => void;
}) {
  const running = project.run_status === "running" ? project.run_stage : "";
  return (
    <div className="flex flex-col gap-2 lg:flex-row lg:items-stretch">
      <ol
        aria-label="Pipeline"
        className="-mx-4 flex min-w-0 flex-1 overflow-x-auto border-y border-border bg-card [scrollbar-width:none] sm:mx-0 sm:rounded-[var(--radius-md)] sm:border [&::-webkit-scrollbar]:hidden"
      >
        {STEPS.map(({ stage, label }, i) => {
          const { text, done } = describe(project, stage);
          const live = running === stage;
          const Icon = live ? CircleNotch : done ? CheckCircle : Circle;
          return (
            <li
              key={stage}
              className={cn(
                "flex min-w-[10.5rem] flex-1 items-center gap-2.5 py-1.5 pl-3 pr-1.5",
                i > 0 && "border-l border-border",
              )}
            >
              <Icon
                aria-hidden
                weight={done && !live ? "fill" : "regular"}
                className={cn(
                  "size-4 shrink-0",
                  live && "animate-spin text-primary",
                  done && !live && "text-primary",
                  !done && !live && "text-muted-foreground",
                )}
              />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium leading-5">{label}</p>
                <p className="truncate text-xs text-muted-foreground" title={text}>
                  {live ? "Running" : text}
                </p>
              </div>
              <Tooltip label={`Run ${stage}`}>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className={cn(tapIcon, "shrink-0 text-muted-foreground hover:text-foreground")}
                  aria-label={`Run ${stage}`}
                  disabled={isDisabled(stage)}
                  loading={busy === `run-${stage}`}
                  onClick={() => onRun(stage)}
                >
                  {busy === `run-${stage}` ? null : <Play weight="fill" />}
                </Button>
              </Tooltip>
            </li>
          );
        })}
      </ol>
      <Button
        className="h-11 lg:h-9 lg:self-center"
        disabled={isDisabled("full")}
        loading={busy === "run-full" || running === "full"}
        onClick={() => onRun("full")}
      >
        {running === "full" ? "Running all" : "Run all"}
      </Button>
    </div>
  );
}
