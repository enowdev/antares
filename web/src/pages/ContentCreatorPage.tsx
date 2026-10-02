import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowClockwise, CaretDown, FilmStrip, Plus } from "@phosphor-icons/react";
import { PageLayout } from "@/components/layout/PageLayout";
import { usePageActions } from "@/components/layout/PageChrome";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge, Input, Textarea, Tooltip } from "@/components/ui/primitives";
import { ActionMenu } from "@/components/creator/ActionMenu";
import { PipelineStrip } from "@/components/creator/PipelineStrip";
import { ProjectList } from "@/components/creator/ProjectList";
import { SetupNotice } from "@/components/creator/SetupNotice";
import {
  Failure,
  Field,
  PaneTabs,
  StatusBadge,
  control,
  inputTap,
  tapIcon,
  tapSm,
} from "@/components/creator/parts";
import { del, post } from "@/lib/api";
import {
  artifactUrl,
  createCronJob,
  createProject,
  getProject,
  getSettings,
  listCronJobs,
  listProjects,
  listSocialAccounts,
  runAction,
  runStage,
  updateProject,
} from "@/lib/creator";
import type {
  ActionName,
  CreatorSettings,
  CronJob,
  Project,
  PublishMode,
  Reference,
  Shot,
  SocialAccount,
  Stage,
} from "@/lib/creator";
import { cn } from "@/lib/utils";

const stages: Stage[] = ["research", "plan", "produce", "publish", "full"];
const platforms = ["youtube", "tiktok", "instagram", "facebook", "x", "threads"];
type Tab = "brief" | "references" | "shots" | "research" | "publish" | "schedule";
const uid = () => crypto.randomUUID().replaceAll("-", "").slice(0, 12);

export default function ContentCreatorPage() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [project, setProject] = useState<Project | null>(null);
  const [accounts, setAccounts] = useState<SocialAccount[]>([]);
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [settings, setSettings] = useState<CreatorSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const busyRef = useRef(false);
  const [error, setError] = useState("");
  const [dirty, setDirty] = useState(false);
  const [tab, setTab] = useState<Tab>("brief");
  const [creating, setCreating] = useState(false);
  // Which shot / reference has its editor open. One at a time keeps the list short.
  const [openShot, setOpenShot] = useState<string | null>(null);
  const [openRef, setOpenRef] = useState<string | null>(null);
  const [draft, setDraft] = useState({
    title: "",
    brief: "",
    platform: "youtube",
    size: "720x1280",
    target_seconds: 24,
    style: "",
    publish_mode: "draft" as PublishMode,
  });
  const [schedule, setSchedule] = useState({
    name: "",
    schedule: "0 8 * * *",
    content_stage: "research" as Stage,
    publish_mode: "draft" as PublishMode,
  });
  const [proof, setProof] = useState("");
  const selected = useRef("");
  const dirtyRef = useRef(false);
  dirtyRef.current = dirty;
  usePageActions(
    <Button size="sm" className="gap-1.5" onClick={() => setCreating(true)}>
      <Plus className="size-4" />
      New video
    </Button>,
    [],
  );
  useEffect(() => {
    let active = true;
    Promise.all([
      listProjects(),
      getSettings(),
      listSocialAccounts(),
      listCronJobs(),
    ])
      .then(([ps, st, as, js]) => {
        if (!active) return;
        setProjects(ps);
        setSettings(st);
        setAccounts(as);
        setJobs(js);
        if (ps[0]) {
          setProject(ps[0]);
          selected.current = ps[0].id;
        }
      })
      .catch((e) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);
  useEffect(() => {
    if (!project?.id) return;
    const id = project.id;
    const timer = window.setInterval(() => {
      if (busyRef.current || dirtyRef.current) return;
      getProject(id)
        .then((p) => {
          if (selected.current === id && !dirtyRef.current && !busyRef.current) setProject(p);
        })
        .catch(() => {});
    }, 5000);
    return () => clearInterval(timer);
  }, [project?.id]);
  const accept = (p: Project) => {
    if (selected.current === p.id) {
      setProject(p);
      setDirty(false);
    }
    setProjects((ps) => [p, ...ps.filter((v) => v.id !== p.id)]);
  };
  const act = async (name: string, fn: () => Promise<void>) => {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(name);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      if (selected.current && !dirtyRef.current) {
        try {
          const p = await getProject(selected.current);
          setProject(p);
        } catch {}
      }
    } finally {
      busyRef.current = false;
      setBusy("");
    }
  };
  const edit = (patch: Partial<Project>) => {
    if (!project) return;
    setProject({ ...project, ...patch });
    setDirty(true);
  };
  const save = async () => {
    if (!project) return;
    accept(await updateProject(project.id, project));
  };
  const media = async (action: ActionName, target_id?: string) => {
    if (!project) return;
    if (dirtyRef.current)
      throw new Error("Save your plan before generating media.");
    accept(await runAction(project.id, { action, target_id }));
  };
  const reset = async (kind: string, target: string) => {
    if (
      !project ||
      !window.confirm(
        "Discard this generated asset and dependent clips? Regeneration may incur provider charges. For an uncertain submission, first verify the provider did not already accept a job.",
      )
    )
      return;
    accept(
      await post<Project>(`/content-creator/projects/${project.id}/action`, {
        action: kind,
        target_id: target,
        confirmed: true,
      }),
    );
  };
  const run = async (stage: Stage) => {
    if (!project) return;
    if (dirtyRef.current) throw new Error("Save your plan first.");
    if (
      (stage === "publish" || stage === "full") &&
      project.publish_mode === "auto" &&
      !window.confirm(
        "Allow this run to publish the final video to the selected social account?",
      )
    )
      return;
    const result = await runStage(project.id, {
      stage,
      publish_mode: project.publish_mode,
    });
    const p = await getProject(project.id);
    accept({ ...p, run_session_id: result.session_id });
  };
  const choose = (id: string) =>
    void act("load", async () => {
      if (dirty && !window.confirm("Discard unsaved changes?")) return;
      selected.current = id;
      setProject(await getProject(id));
      setDirty(false);
    });
  const locked = !!busy || project?.run_status === "running";
  const mediaLocked = locked || dirty;
  const patchRef = (index: number, patch: Partial<Reference>) => {
    if (project)
      edit({
        references: project.references.map((r, i) =>
          i === index ? { ...r, ...patch } : r,
        ),
      });
  };
  const patchShot = (index: number, patch: Partial<Shot>) => {
    if (project)
      edit({
        shots: project.shots.map((r, i) =>
          i === index ? { ...r, ...patch } : r,
        ),
      });
  };
  const moveShot = (index: number, delta: number) => {
    if (!project) return;
    const next = [...project.shots];
    const to = index + delta;
    if (to < 0 || to >= next.length) return;
    [next[index], next[to]] = [next[to], next[index]];
    edit({ shots: next });
  };

  const createDialog = (
    <Dialog open={creating} onOpenChange={setCreating}>
      <DialogContent className="sm:max-w-xl">
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(e) => {
            e.preventDefault();
            void act("create", async () => {
              const p = await createProject(draft);
              selected.current = p.id;
              accept(p);
              setCreating(false);
              setDraft({ ...draft, title: "", brief: "" });
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>New video</DialogTitle>
            <DialogDescription>
              A video project keeps its references, shots, and publishing
              record together.
            </DialogDescription>
          </DialogHeader>
          <DialogBody className="grid gap-4 space-y-0 sm:grid-cols-2">
            <Field label="Video title">
              <Input
                required
                className={inputTap}
                value={draft.title}
                onChange={(e) => setDraft({ ...draft, title: e.target.value })}
              />
            </Field>
            <Field label="Platform">
              <select
                className={control}
                value={draft.platform}
                onChange={(e) => setDraft({ ...draft, platform: e.target.value })}
              >
                {platforms.map((p) => (
                  <option key={p}>{p}</option>
                ))}
              </select>
            </Field>
            <Field label="Brief / niche" className="sm:col-span-2">
              <Textarea
                required
                rows={4}
                value={draft.brief}
                onChange={(e) => setDraft({ ...draft, brief: e.target.value })}
                placeholder="Describe the story, audience, and visual direction."
              />
            </Field>
            <Field label="Target duration (seconds)">
              <Input
                required
                type="number"
                min={1}
                max={3600}
                className={inputTap}
                value={draft.target_seconds}
                onChange={(e) =>
                  setDraft({ ...draft, target_seconds: Number(e.target.value) })
                }
              />
            </Field>
            <Field label="Resolution">
              <select
                className={control}
                value={draft.size}
                onChange={(e) => setDraft({ ...draft, size: e.target.value })}
              >
                {["720x1280", "1280x720", "1024x1792", "1792x1024"].map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </select>
            </Field>
            {creating && error ? (
              <div className="sm:col-span-2">
                <Failure text={error} />
              </div>
            ) : null}
          </DialogBody>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              className={tapSm}
              onClick={() => setCreating(false)}
            >
              Cancel
            </Button>
            <Button
              disabled={!!busy}
              loading={busy === "create"}
              className={tapSm}
              type="submit"
            >
              Create video project
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );

  if (loading)
    return (
      <PageLayout>
        <p role="status" className="text-sm text-muted-foreground">
          Loading video projects…
        </p>
        {createDialog}
      </PageLayout>
    );

  // On desktop the project list and the open project each scroll on their
  // own, so working down a long project never scrolls the list away.
  return (
    <PageLayout bodyClassName="lg:flex lg:flex-col lg:overflow-hidden lg:pr-0">
      {!creating && <Failure text={error} />}
      <div className="grid min-w-0 gap-4 lg:min-h-0 lg:flex-1 lg:grid-cols-[240px_minmax(0,1fr)] lg:gap-6">
        <ProjectList
          projects={projects}
          activeId={project?.id}
          disabled={!!busy}
          onChoose={choose}
        />
        <section aria-label="Video project" className="min-w-0 space-y-4 lg:min-h-0 lg:overflow-y-auto lg:pb-6 lg:pr-1">
          {!project ? (
            <div data-reveal className="tp-panel rounded-[var(--radius-lg)] border border-border bg-card px-6 py-12 text-center">
              <FilmStrip className="mx-auto mb-3 size-7 text-muted-foreground" />
              <h2 className="font-medium">Create your first video</h2>
              <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">
                Start with a brief, then plan references and shots. Configure
                image and video models before production.
              </p>
              <Button className="mt-5 gap-1.5 h-11 lg:h-9" onClick={() => setCreating(true)}>
                <Plus className="size-4" />
                New video
              </Button>
              <div className="mt-6 flex justify-center">
                <SetupNotice settings={settings} />
              </div>
            </div>
          ) : (
            <>
              <header className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="eyebrow mb-1">Video project</p>
                  <h2 className="truncate text-lg font-medium leading-6">
                    {project.title}
                  </h2>
                  <p className="flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
                    <span>
                      {project.run_status === "running"
                        ? `Running ${project.run_stage}`
                        : project.status}{" "}
                      · revision {project.revision}
                    </span>
                    {dirty ? (
                      <span className="font-medium text-foreground">
                        · unsaved changes
                      </span>
                    ) : null}
                    {project.run_session_id && (
                      <>
                        <span aria-hidden>·</span>
                        <Link
                          className="text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground"
                          to={`/c/${project.run_session_id}`}
                        >
                          Open agent run
                        </Link>
                      </>
                    )}
                  </p>
                </div>
                <div className="flex shrink-0 gap-2">
                  <Tooltip label="Refresh project">
                    <Button
                      variant="outline"
                      size="icon-sm"
                      className={tapIcon}
                      disabled={!!busy || dirty}
                      onClick={() =>
                        void act("refresh", async () =>
                          accept(await getProject(project.id)),
                        )
                      }
                      aria-label="Refresh project"
                    >
                      <ArrowClockwise />
                    </Button>
                  </Tooltip>
                  <Button
                    size="sm"
                    variant={dirty ? "default" : "outline"}
                    className={tapSm}
                    disabled={locked || !dirty}
                    loading={busy === "save"}
                    onClick={() => void act("save", save)}
                  >
                    Save plan
                  </Button>
                </div>
              </header>
              <Failure text={project.error || project.last_run_error} />
              <PipelineStrip
                project={project}
                busy={busy}
                isDisabled={(stage) =>
                  mediaLocked ||
                  (stage === "publish" && project.publish_mode !== "auto")
                }
                onRun={(stage) => void act(`run-${stage}`, () => run(stage))}
              />
              <SetupNotice settings={settings} />
              {busy && (
                <p role="status" className="text-sm text-muted-foreground">
                  Working: {busy}. Media generation may take several minutes.
                </p>
              )}
              <PaneTabs<Tab>
                value={tab}
                onChange={setTab}
                tabs={[
                  { id: "brief", label: "Brief" },
                  {
                    id: "references",
                    label: "References",
                    count: project.references.length,
                  },
                  { id: "shots", label: "Shots", count: project.shots.length },
                  { id: "research", label: "Research" },
                  { id: "publish", label: "Publish" },
                  { id: "schedule", label: "Schedule" },
                ]}
              />
              {tab === "brief" && (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="Title">
                    <Input
                      disabled={locked}
                      className={inputTap}
                      value={project.title}
                      onChange={(e) => edit({ title: e.target.value })}
                    />
                  </Field>
                  <Field label="Target duration (seconds)">
                    <Input
                      disabled={locked}
                      type="number"
                      min={1}
                      max={3600}
                      className={inputTap}
                      value={project.target_seconds}
                      onChange={(e) =>
                        edit({ target_seconds: Number(e.target.value) })
                      }
                    />
                  </Field>
                  <Field label="Visual style">
                    <Input
                      disabled={locked}
                      className={inputTap}
                      value={project.style}
                      onChange={(e) => edit({ style: e.target.value })}
                    />
                  </Field>
                  <Field label="Resolution">
                    <Input
                      disabled={locked}
                      className={inputTap}
                      value={project.size}
                      onChange={(e) => edit({ size: e.target.value })}
                    />
                  </Field>
                  <Field label="Creative brief" className="sm:col-span-2">
                    <Textarea
                      disabled={locked}
                      rows={6}
                      value={project.brief}
                      onChange={(e) => edit({ brief: e.target.value })}
                    />
                  </Field>
                </div>
              )}
              {tab === "references" && (
                <div className="space-y-3">
                  <p className="text-sm text-muted-foreground">
                    Generate each character, setting, prop, or style once.
                    Shots reuse these IDs for continuity.
                  </p>
                  <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-4">
                    {project.references.map((ref) => {
                      const open = openRef === ref.id;
                      return (
                        <li key={ref.id} data-reveal className="min-w-0">
                          <button
                            type="button"
                            aria-expanded={open}
                            aria-controls="ref-editor"
                            onClick={() => setOpenRef(open ? null : ref.id)}
                            className={cn(
                              "flex w-full flex-col overflow-hidden rounded-[var(--radius-md)] border bg-card text-left transition-[border-color,background-color] duration-200",
                              open
                                ? "border-foreground bg-raised"
                                : "border-border hover:border-line hover:bg-raised",
                            )}
                          >
                            {ref.path ? (
                              <img
                                className="aspect-video w-full bg-raised object-cover"
                                src={artifactUrl(project.id, ref.path)}
                                alt={ref.name}
                              />
                            ) : (
                              <span className="flex aspect-video w-full items-center justify-center bg-raised text-xs text-muted-foreground">
                                Not generated
                              </span>
                            )}
                            <span className="block w-full space-y-1.5 p-2.5">
                              <span className="block truncate text-sm font-medium">
                                {ref.name || "Unnamed reference"}
                              </span>
                              <span className="flex flex-wrap gap-1">
                                <Badge variant="outline">{ref.kind}</Badge>
                                <StatusBadge status={ref.status} />
                              </span>
                            </span>
                          </button>
                        </li>
                      );
                    })}
                    <li className="min-w-0">
                      <button
                        type="button"
                        disabled={locked}
                        onClick={() => {
                          const id = `ref_${uid()}`;
                          edit({
                            references: [
                              ...project.references,
                              {
                                id,
                                kind: "character",
                                name: "",
                                prompt: "",
                                path: "",
                                status: "draft",
                                error: "",
                              },
                            ],
                          });
                          setOpenRef(id);
                        }}
                        className="flex h-full min-h-24 w-full flex-col items-center justify-center gap-1.5 rounded-[var(--radius-md)] border border-dashed border-border text-sm text-muted-foreground transition-colors hover:border-line hover:bg-raised hover:text-foreground disabled:pointer-events-none disabled:opacity-50"
                      >
                        <Plus className="size-4" />
                        Add reference
                      </button>
                    </li>
                  </ul>
                  {project.references.map((ref, index) =>
                    openRef === ref.id ? (
                      <div
                        key={ref.id}
                        id="ref-editor"
                        className="m-open tp-panel rounded-[var(--radius-lg)] border border-border bg-card p-3 sm:p-4"
                      >
                        <div className="mb-3 flex flex-wrap items-center gap-2">
                          <h3 className="text-sm font-medium">
                            {ref.name || "Unnamed reference"}
                          </h3>
                          <StatusBadge status={ref.status} />
                          <span className="break-all font-mono text-xs text-muted-foreground">
                            {ref.id}
                          </span>
                        </div>
                        <div className="grid gap-4 sm:grid-cols-[180px_minmax(0,1fr)]">
                          {ref.path ? (
                            <img
                              className="w-full rounded-[var(--radius-md)] bg-raised"
                              src={artifactUrl(project.id, ref.path)}
                              alt={ref.name}
                            />
                          ) : (
                            <div className="hidden aspect-video items-center justify-center rounded-[var(--radius-md)] bg-raised text-xs text-muted-foreground sm:flex">
                              Not generated
                            </div>
                          )}
                          <div className="min-w-0 space-y-3">
                            <div className="grid gap-3 sm:grid-cols-2">
                              <Field label="Name">
                                <Input
                                  disabled={locked}
                                  className={inputTap}
                                  value={ref.name}
                                  onChange={(e) =>
                                    patchRef(index, { name: e.target.value })
                                  }
                                />
                              </Field>
                              <Field label="Reference kind">
                                <select
                                  disabled={locked}
                                  className={control}
                                  value={ref.kind}
                                  onChange={(e) =>
                                    patchRef(index, {
                                      kind: e.target.value as Reference["kind"],
                                    })
                                  }
                                >
                                  {["character", "setting", "style", "prop"].map(
                                    (k) => (
                                      <option key={k}>{k}</option>
                                    ),
                                  )}
                                </select>
                              </Field>
                            </div>
                            <Field label="Reference prompt">
                              <Textarea
                                disabled={locked}
                                rows={3}
                                value={ref.prompt}
                                onChange={(e) =>
                                  patchRef(index, { prompt: e.target.value })
                                }
                              />
                            </Field>
                            <Failure text={ref.error} />
                            <div className="flex flex-wrap items-center gap-2">
                              <Button
                                size="sm"
                                disabled={mediaLocked || ref.status === "ready"}
                                loading={busy === "generate reference"}
                                className={tapSm}
                                onClick={() =>
                                  void act("generate reference", () =>
                                    media("generate_reference", ref.id),
                                  )
                                }
                              >
                                Generate reference
                              </Button>
                              <Button
                                size="sm"
                                variant="outline"
                                disabled={mediaLocked}
                                className={tapSm}
                                onClick={() =>
                                  void act("reset reference", () =>
                                    reset("reset_reference", ref.id),
                                  )
                                }
                              >
                                Reset
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                disabled={locked}
                                className={cn(tapSm, "text-destructive hover:text-destructive")}
                                onClick={() =>
                                  edit({
                                    references: project.references.filter(
                                      (_, i) => i !== index,
                                    ),
                                  })
                                }
                              >
                                Remove
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                className={cn(tapSm, "ml-auto")}
                                onClick={() => setOpenRef(null)}
                              >
                                Close
                              </Button>
                            </div>
                          </div>
                        </div>
                      </div>
                    ) : null,
                  )}
                </div>
              )}
              {tab === "shots" && (
                <div className="space-y-3">
                  <p className="text-sm text-muted-foreground">
                    {project.shots.reduce((n, s) => n + s.duration_seconds, 0)}{" "}
                    planned seconds / {project.target_seconds} target. A
                    continuation starts from the previous clip’s last frame.
                  </p>
                  {project.shots.length > 0 && (
                    <ol className="divide-y divide-border overflow-hidden rounded-[var(--radius-lg)] border border-border bg-card">
                      {project.shots.map((shot, index) => {
                        const open = openShot === shot.id;
                        const keyframe = {
                          label: "Create keyframe",
                          disabled: mediaLocked || !!shot.keyframe_path,
                          onSelect: () =>
                            void act("generate keyframe", () =>
                              media("generate_keyframe", shot.id),
                            ),
                        };
                        const clip = {
                          label: "Generate clip",
                          disabled:
                            mediaLocked ||
                            !!shot.video_job_id ||
                            shot.status === "submission_unknown",
                          onSelect: () =>
                            void act("generate clip", () =>
                              media("generate_video", shot.id),
                            ),
                        };
                        const check = {
                          label: "Check render",
                          disabled:
                            mediaLocked ||
                            !shot.video_job_id ||
                            shot.status === "ready",
                          onSelect: () =>
                            void act("poll clip", () =>
                              media("poll_video", shot.id),
                            ),
                        };
                        // The one action this shot most likely needs next.
                        const quick = !shot.keyframe_path
                          ? keyframe
                          : shot.video_job_id && shot.status !== "ready"
                            ? check
                            : clip;
                        const rest = [keyframe, clip, check].filter(
                          (a) => a !== quick,
                        );
                        const meta = `${shot.duration_seconds}s · ${shot.continuity === "continue" ? "continues" : "cut"}`;
                        const progress = shot.video_job_id
                          ? `${shot.progress}%`
                          : "";
                        return (
                          <li key={shot.id} data-reveal className={cn("transition-colors duration-200", open ? "bg-raised" : "hover:bg-raised")}>
                            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-1 px-2 py-1 sm:flex">
                              <button
                                type="button"
                                aria-expanded={open}
                                aria-controls={`shot-${shot.id}`}
                                onClick={() => setOpenShot(open ? null : shot.id)}
                                className="flex min-h-11 min-w-0 flex-1 items-center gap-2 px-1.5 text-left"
                              >
                                <span className="w-5 shrink-0 text-right font-mono text-[11px] tabular-nums text-dim">
                                  {String(index + 1).padStart(2, "0")}
                                </span>
                                <span
                                  className={cn(
                                    "min-w-0 flex-1 truncate text-sm",
                                    !shot.prompt && "text-muted-foreground",
                                  )}
                                >
                                  {shot.prompt || "No prompt yet"}
                                </span>
                                <span className="hidden shrink-0 text-xs tabular-nums text-muted-foreground sm:inline">
                                  {meta}
                                </span>
                                <span className="hidden shrink-0 items-center gap-1.5 sm:inline-flex">
                                  <StatusBadge status={shot.status} />
                                  {progress ? (
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                      {progress}
                                    </span>
                                  ) : null}
                                </span>
                                <CaretDown
                                  aria-hidden
                                  className={cn(
                                    "size-3.5 shrink-0 text-muted-foreground transition-transform",
                                    open && "rotate-180",
                                  )}
                                />
                              </button>
                              <div className="sm:order-last">
                                <ActionMenu
                                  label={`More actions for shot ${index + 1}`}
                                  actions={[
                                    ...rest,
                                    "separator",
                                    {
                                      label: "Move up",
                                      disabled: locked || index === 0,
                                      onSelect: () => moveShot(index, -1),
                                    },
                                    {
                                      label: "Move down",
                                      disabled:
                                        locked ||
                                        index === project.shots.length - 1,
                                      onSelect: () => moveShot(index, 1),
                                    },
                                    "separator",
                                    {
                                      label: "Reset shot",
                                      disabled: mediaLocked,
                                      onSelect: () =>
                                        void act("reset shot", () =>
                                          reset("reset_shot", shot.id),
                                        ),
                                    },
                                    {
                                      label: "Remove",
                                      destructive: true,
                                      disabled: locked,
                                      onSelect: () =>
                                        edit({
                                          shots: project.shots.filter(
                                            (_, i) => i !== index,
                                          ),
                                        }),
                                    },
                                  ]}
                                />
                              </div>
                              <div className="col-span-2 flex items-center gap-2 pb-1 pl-9 sm:contents">
                                <span className="text-xs tabular-nums text-muted-foreground sm:hidden">
                                  {meta}
                                </span>
                                <span className="inline-flex items-center gap-1.5 sm:hidden">
                                  <StatusBadge status={shot.status} />
                                  {progress ? (
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                      {progress}
                                    </span>
                                  ) : null}
                                </span>
                                <Button
                                  size="sm"
                                  variant="outline"
                                  className={cn(tapSm, "ml-auto shrink-0 sm:ml-1")}
                                  disabled={quick.disabled}
                                  onClick={quick.onSelect}
                                >
                                  {quick.label}
                                </Button>
                              </div>
                            </div>
                            {open && (
                              <div
                                id={`shot-${shot.id}`}
                                className="m-open space-y-3 border-t border-border px-3 pb-3 pt-3 sm:pl-10"
                              >
                                <Field label="Shot prompt">
                                  <Textarea
                                    disabled={locked}
                                    rows={3}
                                    value={shot.prompt}
                                    onChange={(e) =>
                                      patchShot(index, { prompt: e.target.value })
                                    }
                                  />
                                </Field>
                                <div className="grid gap-3 sm:grid-cols-[8rem_minmax(0,1fr)_minmax(0,1.5fr)]">
                                  <Field label="Clip seconds">
                                    <Input
                                      disabled={locked}
                                      type="number"
                                      min={1}
                                      max={120}
                                      className={inputTap}
                                      value={shot.duration_seconds}
                                      onChange={(e) =>
                                        patchShot(index, {
                                          duration_seconds: Number(e.target.value),
                                        })
                                      }
                                    />
                                  </Field>
                                  <Field label="Continuity">
                                    <select
                                      disabled={locked}
                                      className={control}
                                      value={shot.continuity}
                                      onChange={(e) =>
                                        patchShot(index, {
                                          continuity: e.target
                                            .value as Shot["continuity"],
                                        })
                                      }
                                    >
                                      <option value="cut">New composition</option>
                                      <option value="continue" disabled={index === 0}>
                                        Continue previous clip
                                      </option>
                                    </select>
                                  </Field>
                                  <Field label="Continuity notes">
                                    <Input
                                      disabled={locked}
                                      className={inputTap}
                                      value={shot.notes}
                                      onChange={(e) =>
                                        patchShot(index, { notes: e.target.value })
                                      }
                                    />
                                  </Field>
                                </div>
                                {project.references.length > 0 && (
                                  <fieldset>
                                    <legend className="mb-1 text-xs text-muted-foreground">
                                      Visual references
                                    </legend>
                                    <div className="flex flex-wrap gap-x-4">
                                      {project.references.map((ref) => (
                                        <label
                                          key={ref.id}
                                          className="flex min-h-11 items-center gap-2 text-sm lg:min-h-8"
                                        >
                                          <input
                                            type="checkbox"
                                            disabled={locked}
                                            checked={shot.reference_ids?.includes(ref.id)}
                                            onChange={(e) =>
                                              patchShot(index, {
                                                reference_ids: e.target.checked
                                                  ? [...(shot.reference_ids ?? []), ref.id]
                                                  : (shot.reference_ids ?? []).filter(
                                                      (id) => id !== ref.id,
                                                    ),
                                              })
                                            }
                                          />
                                          {ref.name || ref.id}
                                        </label>
                                      ))}
                                    </div>
                                  </fieldset>
                                )}
                                {(shot.keyframe_path ||
                                  shot.video_path ||
                                  shot.last_frame_path) && (
                                  <div className="grid gap-3 sm:grid-cols-3">
                                    {shot.keyframe_path && (
                                      <figure>
                                        <img
                                          className="max-h-48 w-full rounded-[var(--radius-md)] bg-raised object-contain"
                                          src={artifactUrl(
                                            project.id,
                                            shot.keyframe_path,
                                          )}
                                          alt={`Shot ${index + 1} keyframe`}
                                        />
                                        <figcaption className="mt-1 text-xs text-muted-foreground">
                                          Keyframe
                                        </figcaption>
                                      </figure>
                                    )}
                                    {shot.video_path && (
                                      <video
                                        controls
                                        preload="metadata"
                                        className="max-h-48 w-full rounded-[var(--radius-md)] bg-raised"
                                        src={artifactUrl(project.id, shot.video_path)}
                                      />
                                    )}
                                    {shot.last_frame_path && (
                                      <figure>
                                        <img
                                          className="max-h-48 w-full rounded-[var(--radius-md)] bg-raised object-contain"
                                          src={artifactUrl(
                                            project.id,
                                            shot.last_frame_path,
                                          )}
                                          alt={`Shot ${index + 1} final frame`}
                                        />
                                        <figcaption className="mt-1 text-xs text-muted-foreground">
                                          Last frame
                                        </figcaption>
                                      </figure>
                                    )}
                                  </div>
                                )}
                                <Failure text={shot.error} />
                              </div>
                            )}
                          </li>
                        );
                      })}
                    </ol>
                  )}
                  <div className="flex flex-wrap gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      className={cn(tapSm, "gap-1.5")}
                      disabled={locked}
                      onClick={() => {
                        const id = `shot_${uid()}`;
                        edit({
                          shots: [
                            ...project.shots,
                            {
                              id,
                              prompt: "",
                              reference_ids: project.references.map((r) => r.id),
                              duration_seconds: settings?.video.seconds ?? 8,
                              continuity: project.shots.length ? "continue" : "cut",
                              notes: "",
                              keyframe_path: "",
                              video_job_id: "",
                              video_path: "",
                              last_frame_path: "",
                              status: "draft",
                              error: "",
                              progress: 0,
                            },
                          ],
                        });
                        setOpenShot(id);
                      }}
                    >
                      <Plus className="size-4" />
                      Add shot
                    </Button>
                    <Button
                      size="sm"
                      className={tapSm}
                      disabled={
                        mediaLocked ||
                        !project.shots.length ||
                        project.shots.some((s) => s.status !== "ready")
                      }
                      loading={busy === "assemble"}
                      onClick={() => void act("assemble", () => media("assemble"))}
                    >
                      Assemble final video
                    </Button>
                  </div>
                  {project.final_path && (
                    <video
                      aria-label="Final video"
                      controls
                      preload="metadata"
                      className="max-h-[32rem] w-full rounded-[var(--radius-md)] bg-raised"
                      src={artifactUrl(project.id, project.final_path)}
                    />
                  )}
                </div>
              )}
              {tab === "research" && (
                <div className="grid gap-6 xl:grid-cols-2">
                  <section className="min-w-0">
                    <h3 className="eyebrow mb-3">Observed trends</h3>
                    {project.research.length === 0 ? (
                      <p className="text-sm text-muted-foreground">
                        Run research to collect source URLs, observation times,
                        and available metrics. No trends have been recorded yet.
                      </p>
                    ) : (
                      <ul className="space-y-2">
                        {project.research.map((t, i) => (
                          <li
                            key={i}
                            data-reveal
                            className="rounded-[var(--radius-md)] border border-border bg-transparent p-3 transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
                          >
                            <a
                              className="break-words text-sm font-medium text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground"
                              target="_blank"
                              rel="noreferrer"
                              href={t.url}
                            >
                              {t.title}
                            </a>
                            <p className="text-xs text-muted-foreground">
                              Observed {t.observed_at}
                            </p>
                            {t.notes ? (
                              <p className="mt-2 whitespace-pre-wrap text-sm">
                                {t.notes}
                              </p>
                            ) : null}
                            {Object.keys(t.metrics ?? {}).length ? (
                              <p className="mt-1 text-xs tabular-nums text-muted-foreground">
                                {Object.entries(t.metrics ?? {})
                                  .map(([k, v]) => `${k}: ${v}`)
                                  .join(" · ")}
                              </p>
                            ) : null}
                          </li>
                        ))}
                      </ul>
                    )}
                  </section>
                  <section className="min-w-0">
                    <h3 className="eyebrow mb-3">Original ideas</h3>
                    {project.ideas.length === 0 ? (
                      <p className="text-sm text-muted-foreground">
                        Run plan after research to propose ideas and build the
                        shot list.
                      </p>
                    ) : (
                      <div className="space-y-2">
                        {project.ideas.map((idea) => (
                          <label
                            key={idea.id}
                            data-reveal
                            className={cn(
                              "flex gap-3 rounded-[var(--radius-md)] border p-3 transition-[border-color,background-color] duration-200",
                              idea.selected
                                ? "border-foreground bg-raised"
                                : "border-border hover:border-line hover:bg-raised",
                            )}
                          >
                            <input
                              type="radio"
                              name="idea"
                              className="mt-1"
                              disabled={locked}
                              checked={idea.selected}
                              onChange={() =>
                                edit({
                                  ideas: project.ideas.map((i) => ({
                                    ...i,
                                    selected: i.id === idea.id,
                                  })),
                                })
                              }
                            />
                            <span className="min-w-0">
                              <strong className="text-sm font-medium">
                                {idea.title}
                              </strong>
                              <p className="text-sm">{idea.hook}</p>
                              <p className="text-xs text-muted-foreground">
                                {idea.rationale}
                              </p>
                            </span>
                          </label>
                        ))}
                      </div>
                    )}
                  </section>
                </div>
              )}
              {tab === "publish" && (
                <div className="grid gap-6 lg:grid-cols-2">
                  <div className="min-w-0 space-y-4">
                    <div className="grid gap-4 sm:grid-cols-2">
                      <Field label="Platform">
                        <select
                          disabled={locked}
                          className={control}
                          value={project.platform}
                          onChange={(e) =>
                            edit({ platform: e.target.value, account_id: "" })
                          }
                        >
                          {platforms.map((p) => (
                            <option key={p}>{p}</option>
                          ))}
                        </select>
                      </Field>
                      <Field label="Existing social account">
                        <select
                          disabled={locked}
                          className={control}
                          value={project.account_id}
                          onChange={(e) => edit({ account_id: e.target.value })}
                        >
                          <option value="">Select a connected account</option>
                          {accounts
                            .filter((a) => a.platform === project.platform)
                            .map((a) => (
                              <option key={a.id} value={a.id}>
                                {a.display_name || a.username} ({a.status})
                              </option>
                            ))}
                        </select>
                      </Field>
                    </div>
                    <Link
                      className="inline-flex min-h-11 items-center text-sm text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground lg:min-h-0"
                      to="/studio/social"
                    >
                      Manage accounts and browser in Social Media
                    </Link>
                    <Field label="Caption">
                      <Textarea
                        disabled={locked}
                        rows={4}
                        value={project.caption}
                        onChange={(e) => edit({ caption: e.target.value })}
                      />
                    </Field>
                    <Field label="Publishing permission">
                      <select
                        disabled={locked}
                        className={control}
                        value={project.publish_mode}
                        onChange={(e) =>
                          edit({ publish_mode: e.target.value as PublishMode })
                        }
                      >
                        <option value="draft">Draft only</option>
                        <option value="auto">
                          Allow publishing to selected account
                        </option>
                      </select>
                    </Field>
                  </div>
                  <div className="min-w-0 space-y-3">
                    <p className="eyebrow flex items-center gap-2">
                      Publication
                      <StatusBadge status={project.publication.status} />
                    </p>
                    <Failure text={project.publication.error} />
                    {project.publication.post_url && (
                      <a
                        className="inline-flex min-h-11 items-center text-sm text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground lg:min-h-0"
                        target="_blank"
                        rel="noreferrer"
                        href={project.publication.post_url}
                      >
                        Open published video
                      </a>
                    )}
                    {project.final_path ? (
                      <>
                        <video
                          controls
                          preload="metadata"
                          className="max-h-96 w-full rounded-[var(--radius-md)] bg-raised"
                          src={artifactUrl(project.id, project.final_path)}
                        />
                        <a
                          className="inline-flex min-h-11 items-center text-sm text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground"
                          href={artifactUrl(project.id, project.final_path)}
                          download
                        >
                          Download final MP4
                        </a>
                      </>
                    ) : (
                      <p className="rounded-[var(--radius-lg)] border border-dashed border-line px-4 py-8 text-center text-sm text-muted-foreground">
                        Assemble the shots before uploading.
                      </p>
                    )}
                    {["blocked", "uploading"].includes(
                      project.publication.status,
                    ) && (
                      <div className="m-rise space-y-2 rounded-[var(--radius-lg)] border border-border bg-card px-4 py-3">
                        <p className="text-sm">
                          An uncertain upload must be checked on the account
                          before allowing another attempt.
                        </p>
                        <Field label="How did you verify it was not published?">
                          <Textarea
                            value={proof}
                            onChange={(e) => setProof(e.target.value)}
                          />
                        </Field>
                        <Button
                          size="sm"
                          variant="outline"
                          className={tapSm}
                          disabled={locked || !proof.trim()}
                          onClick={() =>
                            void act("reconcile publication", async () => {
                              if (
                                !window.confirm(
                                  "I checked the account and verified this video was not published. Allow another attempt?",
                                )
                              )
                                return;
                              accept(
                                await post<Project>(
                                  `/content-creator/projects/${project.id}/reconcile`,
                                  { proof, not_published: true },
                                ),
                              );
                              setProof("");
                            })
                          }
                        >
                          Confirm not published
                        </Button>
                      </div>
                    )}
                  </div>
                </div>
              )}
              {tab === "schedule" && (
                <div className="space-y-4">
                  <form
                    className="tp-panel grid gap-4 rounded-[var(--radius-lg)] border border-border bg-card p-4 sm:grid-cols-2 sm:p-5"
                    onSubmit={(e) => {
                      e.preventDefault();
                      void act("schedule", async () => {
                        if (
                          schedule.publish_mode === "auto" &&
                          !window.confirm(
                            "Authorize this recurring job to publish to the project account?",
                          )
                        )
                          return;
                        await createCronJob({
                          ...schedule,
                          role: "content-creator",
                          content_project_id: project.id,
                        });
                        setJobs(await listCronJobs());
                        setSchedule({ ...schedule, name: "" });
                      });
                    }}
                  >
                    <Field label="Schedule name">
                      <Input
                        required
                        className={inputTap}
                        value={schedule.name}
                        onChange={(e) =>
                          setSchedule({ ...schedule, name: e.target.value })
                        }
                      />
                    </Field>
                    <Field label="Cron expression">
                      <Input
                        required
                        className={inputTap}
                        value={schedule.schedule}
                        onChange={(e) =>
                          setSchedule({
                            ...schedule,
                            schedule: e.target.value,
                          })
                        }
                      />
                    </Field>
                    <Field label="Stage">
                      <select
                        className={control}
                        value={schedule.content_stage}
                        onChange={(e) =>
                          setSchedule({
                            ...schedule,
                            content_stage: e.target.value as Stage,
                          })
                        }
                      >
                        {stages.map((s) => (
                          <option key={s}>{s}</option>
                        ))}
                      </select>
                    </Field>
                    <Field label="Publishing mode">
                      <select
                        className={control}
                        value={schedule.publish_mode}
                        onChange={(e) =>
                          setSchedule({
                            ...schedule,
                            publish_mode: e.target.value as PublishMode,
                          })
                        }
                      >
                        <option value="draft">Draft only</option>
                        <option value="auto">Allow publishing</option>
                      </select>
                    </Field>
                    <div className="flex flex-col gap-3 sm:col-span-2 sm:flex-row sm:items-center sm:justify-between">
                      <p className="text-xs text-muted-foreground">
                        Uses the existing scheduler timezone. Jobs needing
                        interactive login or approval stop with a blocked
                        reason.
                      </p>
                      <Button
                        type="submit"
                        size="sm"
                        className={cn(tapSm, "shrink-0")}
                        disabled={
                          !!busy ||
                          (schedule.content_stage === "publish" &&
                            schedule.publish_mode === "draft")
                        }
                      >
                        Create schedule
                      </Button>
                    </div>
                  </form>
                  {jobs.some((j) => j.meta?.content_project_id === project.id) && (
                    <ul className="overflow-hidden rounded-[var(--radius-lg)] border border-border bg-card">
                      {jobs
                        .filter((j) => j.meta?.content_project_id === project.id)
                        .map((j) => (
                          <li
                            key={j.id}
                            data-reveal
                            className="flex items-center justify-between gap-2 border-t border-border px-4 py-3 transition-colors duration-200 first:border-t-0 hover:bg-raised"
                          >
                            <div className="min-w-0">
                              <p className="truncate text-sm font-medium">{j.name}</p>
                              <p className="truncate text-xs text-muted-foreground">
                                <span className="font-mono">{j.schedule}</span> · {j.meta?.content_stage} ·{" "}
                                {j.meta?.publish_mode} ·{" "}
                                {j.enabled ? "enabled" : "disabled"}
                              </p>
                            </div>
                            <Button
                              size="sm"
                              variant="outline"
                              className={cn(tapSm, "shrink-0")}
                              disabled={!!busy}
                              onClick={() =>
                                void act("remove schedule", async () => {
                                  await del(`/cron/jobs/${j.id}`);
                                  setJobs(await listCronJobs());
                                })
                              }
                            >
                              Remove
                            </Button>
                          </li>
                        ))}
                    </ul>
                  )}
                  <Link
                    to="/automation/schedules"
                    className="inline-flex min-h-11 items-center text-sm text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground lg:min-h-0"
                  >
                    Open scheduler and run history
                  </Link>
                </div>
              )}
            </>
          )}
        </section>
      </div>
      {createDialog}
    </PageLayout>
  );
}
