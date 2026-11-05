import { useEffect, useRef } from "react";
import type { Project } from "@/lib/creator";
import { cn } from "@/lib/utils";

/**
 * Project picker. A vertical list styled like the sidebar on desktop; on a
 * phone it becomes a horizontal scroller above the project pane so the pane
 * stays the first thing on screen.
 */
export function ProjectList({
  projects,
  activeId,
  disabled,
  onChoose,
}: {
  projects: Project[];
  activeId?: string;
  disabled: boolean;
  onChoose: (id: string) => void;
}) {
  const listRef = useRef<HTMLUListElement>(null);
  // On a phone the list scrolls sideways; keep the open project in view.
  // Scroll the strip itself, not scrollIntoView, so the page never jumps.
  useEffect(() => {
    const list = listRef.current;
    const active = list?.querySelector<HTMLElement>('[aria-current="true"]');
    if (!list || !active || list.scrollWidth <= list.clientWidth) return;
    const item = active.parentElement ?? active;
    const start = item.offsetLeft;
    const end = start + item.offsetWidth;
    if (start < list.scrollLeft || end > list.scrollLeft + list.clientWidth) {
      list.scrollLeft = start - 16;
    }
  }, [activeId, projects.length]);
  if (projects.length === 0) {
    return (
      <aside className="hidden lg:block">
        <h2 className="eyebrow mb-3 px-3">
          Video projects
        </h2>
        <p className="px-3 text-sm text-muted-foreground">No projects yet.</p>
      </aside>
    );
  }
  return (
    <nav
      aria-label="Video projects"
      className="min-w-0 lg:min-h-0 lg:overflow-y-auto"
    >
      <h2 className="eyebrow mb-3 hidden px-3 lg:block">
        Video projects
      </h2>
      <ul
        ref={listRef}
        className="relative -mx-4 flex gap-2 overflow-x-auto px-4 [scrollbar-width:none] sm:-mx-6 sm:px-6 lg:mx-0 lg:flex-col lg:gap-0.5 lg:overflow-visible lg:px-0 [&::-webkit-scrollbar]:hidden">
        {projects.map((p) => {
          const active = p.id === activeId;
          return (
            <li key={p.id} data-reveal className="w-44 shrink-0 lg:w-auto">
              <button
                type="button"
                disabled={disabled}
                onClick={() => onChoose(p.id)}
                aria-current={active ? "true" : undefined}
                className={cn(
                  "flex min-h-11 w-full flex-col items-start rounded-[var(--radius-md)] border px-3 py-2 text-left transition-[border-color,background-color,color] duration-200 disabled:cursor-wait lg:border-transparent",
                  active
                    ? "border-foreground bg-nav-active text-foreground lg:border-transparent"
                    : "border-border hover:border-line hover:bg-raised hover:text-foreground",
                )}
              >
                <span
                  className={cn(
                    "w-full truncate text-sm",
                    active && "font-medium",
                  )}
                  title={p.title}
                >
                  {p.title || "Untitled video"}
                </span>
                <span
                  className={cn(
                    "w-full truncate text-xs",
                    "font-mono text-muted-foreground",
                  )}
                >
                  {p.platform} · {p.status}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
