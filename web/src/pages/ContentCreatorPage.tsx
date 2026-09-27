import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { FilmStrip, Plus, ArrowClockwise } from "@phosphor-icons/react";
import { PageLayout } from "@/components/layout/PageLayout";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/primitives";
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
  saveSettings,
  updateProject,
} from "@/lib/creator";
import type {
  ActionName,
  CreatorSettings,
  CronJob,
  Project,
  ProviderSettingsPatch,
  PublishMode,
  Reference,
  Shot,
  SocialAccount,
  Stage,
} from "@/lib/creator";

const stages: Stage[] = ["research", "plan", "produce", "publish", "full"];
const control =
  "min-h-11 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring";
const actionClass = "min-h-11";
const uid = () => crypto.randomUUID().replaceAll("-", "").slice(0, 12);
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}
function Failure({ text }: { text?: string }) {
  return text ? (
    <pre
      role="alert"
      className="whitespace-pre-wrap break-words rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs text-foreground"
    >
      {JSON.stringify({ error: text }, null, 2)}
    </pre>
  ) : null;
}

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
  const [tab, setTab] = useState("brief");
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState({
    title: "",
    brief: "",
    platform: "youtube",
    size: "720x1280",
    target_seconds: 24,
    style: "",
    publish_mode: "draft" as PublishMode,
  });
  const [image, setImage] = useState<ProviderSettingsPatch>({});
  const [video, setVideo] = useState<ProviderSettingsPatch>({});
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
        const { has_key: _, ...im } = st.image;
        const { has_key: __, ...vi } = st.video;
        setImage(im);
        setVideo(vi);
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

  return (
    <PageLayout>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          A video project keeps its references, shots, and publishing record
          together.
        </p>
        <Button className={actionClass} onClick={() => setCreating((v) => !v)}>
          <Plus className="size-4" />
          New video
        </Button>
      </div>
      <Failure text={error} />
      {creating && (
        <form
          className="grid gap-3 rounded-lg border border-border bg-card p-4 sm:grid-cols-2"
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
          <Field label="Video title">
            <Input
              required
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
              {[
                "youtube",
                "tiktok",
                "instagram",
                "facebook",
                "x",
                "threads",
              ].map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
          </Field>
          <div className="sm:col-span-2">
            <Field label="Brief / niche">
              <Textarea
                required
                value={draft.brief}
                onChange={(e) => setDraft({ ...draft, brief: e.target.value })}
                placeholder="Describe the story, audience, and visual direction."
              />
            </Field>
          </div>
          <Field label="Target duration (seconds)">
            <Input
              required
              type="number"
              min={1}
              max={3600}
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
          <Button disabled={!!busy} className={actionClass} type="submit">
            Create video project
          </Button>
        </form>
      )}
      <div className="flex flex-wrap gap-2 border-b border-border pb-3">
        {[
          "brief",
          "references",
          "shots",
          "research",
          "publish",
          "schedule",
          "models",
        ].map((t) => (
          <Button
            key={t}
            variant={tab === t ? "secondary" : "ghost"}
            className={actionClass}
            aria-pressed={tab === t}
            onClick={() => setTab(t)}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </Button>
        ))}
      </div>
      {loading ? (
        <p role="status">Loading video projects…</p>
      ) : (
        <div className="grid min-w-0 gap-5 lg:grid-cols-[220px_minmax(0,1fr)]">
          <aside className="min-w-0 space-y-2">
            <h2 className="text-sm font-medium">Video projects</h2>
            {projects.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No projects yet. Create a video to start.
              </p>
            ) : (
              projects.map((p) => (
                <button
                  key={p.id}
                  disabled={!!busy}
                  onClick={() => choose(p.id)}
                  aria-current={project?.id === p.id ? "true" : undefined}
                  className={`min-h-11 w-full rounded-md border p-3 text-left ${project?.id === p.id ? "border-primary bg-primary/10" : "border-border bg-card"}`}
                >
                  <span className="block break-words text-sm font-medium">
                    {p.title}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {p.platform} · {p.status}
                  </span>
                </button>
              ))
            )}
          </aside>
          <main className="min-w-0 space-y-4">
            {tab === "models" ? (
              <form
                className="space-y-5"
                onSubmit={(e) => {
                  e.preventDefault();
                  void act("settings", async () => {
                    const saved = await saveSettings({ image, video });
                    setSettings(saved);
                    setImage({ ...image, api_key: "" });
                    setVideo({ ...video, api_key: "" });
                  });
                }}
              >
                <p className="text-sm text-muted-foreground">
                  OpenAI-compatible image generation/edits and asynchronous
                  video endpoints. Changing a provider does not migrate
                  in-flight video jobs.
                </p>
                {(["image", "video"] as const).map((kind) => {
                  const values = kind === "image" ? image : video;
                  const change = kind === "image" ? setImage : setVideo;
                  return (
                    <fieldset
                      key={kind}
                      className="grid gap-3 rounded-lg border border-border bg-card p-4 sm:grid-cols-2"
                    >
                      <legend className="px-2 text-sm font-semibold">
                        {kind === "image" ? "Image model" : "Video model"}
                      </legend>
                      <label className="flex min-h-11 items-center gap-2">
                        <input
                          type="checkbox"
                          checked={!!values.enabled}
                          onChange={(e) =>
                            change({ ...values, enabled: e.target.checked })
                          }
                        />
                        Enable {kind} generation
                      </label>
                      <p className="self-center text-xs text-muted-foreground">
                        {settings?.[kind].has_key
                          ? "Credential configured"
                          : "No credential configured"}
                      </p>
                      <Field label="Provider ID (optional)">
                        <Input
                          value={values.provider ?? ""}
                          onChange={(e) =>
                            change({ ...values, provider: e.target.value })
                          }
                          placeholder="openai"
                        />
                      </Field>
                      <Field label="Model ID">
                        <Input
                          value={values.model ?? ""}
                          onChange={(e) =>
                            change({ ...values, model: e.target.value })
                          }
                        />
                      </Field>
                      <Field label="Base URL">
                        <Input
                          value={values.base_url ?? ""}
                          onChange={(e) =>
                            change({ ...values, base_url: e.target.value })
                          }
                          placeholder="https://api.openai.com/v1"
                        />
                      </Field>
                      <Field label="API key (blank keeps current)">
                        <Input
                          type="password"
                          autoComplete="new-password"
                          value={values.api_key ?? ""}
                          onChange={(e) =>
                            change({ ...values, api_key: e.target.value })
                          }
                        />
                      </Field>
                      <Field label="Default resolution">
                        <Input
                          value={values.size ?? ""}
                          onChange={(e) =>
                            change({ ...values, size: e.target.value })
                          }
                        />
                      </Field>
                      {kind === "video" && (
                        <Field label="Default clip seconds">
                          <Input
                            type="number"
                            min={1}
                            max={120}
                            value={values.seconds ?? 8}
                            onChange={(e) =>
                              change({
                                ...values,
                                seconds: Number(e.target.value),
                              })
                            }
                          />
                        </Field>
                      )}
                    </fieldset>
                  );
                })}
                <p className="text-sm">
                  FFmpeg: {settings?.ffmpeg ? "available" : "not installed"} ·
                  FFprobe: {settings?.ffprobe ? "available" : "not installed"}
                </p>
                <Button type="submit" className={actionClass} disabled={!!busy}>
                  Save media settings
                </Button>
              </form>
            ) : !project ? (
              <div className="rounded-lg border border-dashed border-border p-8 text-center">
                <FilmStrip className="mx-auto mb-3 size-7 text-muted-foreground" />
                <h2>Create your first video</h2>
                <p className="mt-2 text-sm text-muted-foreground">
                  Start with a brief, then plan references and shots. Configure
                  image and video models before production.
                </p>
              </div>
            ) : (
              <>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <h2 className="text-lg font-semibold">{project.title}</h2>
                    <p className="text-xs text-muted-foreground">
                      {project.run_status === "running"
                        ? `Running ${project.run_stage}`
                        : project.status}{" "}
                      · revision {project.revision}
                      {dirty ? " · unsaved changes" : ""}
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      className={actionClass}
                      disabled={!!busy || dirty}
                      onClick={() =>
                        void act("refresh", async () =>
                          accept(await getProject(project.id)),
                        )
                      }
                      aria-label="Refresh project"
                    >
                      <ArrowClockwise className="size-4" />
                    </Button>
                    <Button
                      className={actionClass}
                      disabled={locked || !dirty}
                      onClick={() => void act("save", save)}
                    >
                      Save plan
                    </Button>
                  </div>
                </div>
                <Failure text={project.error || project.last_run_error} />
                <div className="flex flex-wrap gap-2">
                  {stages.map((stage) => (
                    <Button
                      key={stage}
                      variant="outline"
                      className={actionClass}
                      disabled={
                        mediaLocked ||
                        (stage === "publish" && project.publish_mode !== "auto")
                      }
                      onClick={() => void act(`run-${stage}`, () => run(stage))}
                    >
                      Run {stage}
                    </Button>
                  ))}
                  {project.run_session_id && (
                    <Link
                      className="inline-flex min-h-11 items-center px-3 text-sm text-primary underline"
                      to={`/c/${project.run_session_id}`}
                    >
                      Open agent run
                    </Link>
                  )}
                </div>
                {busy && (
                  <p role="status" className="text-sm text-muted-foreground">
                    Working: {busy}. Media generation may take several minutes.
                  </p>
                )}
                {tab === "brief" && (
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Title">
                      <Input
                        disabled={locked}
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
                        value={project.target_seconds}
                        onChange={(e) =>
                          edit({ target_seconds: Number(e.target.value) })
                        }
                      />
                    </Field>
                    <div className="sm:col-span-2">
                      <Field label="Creative brief">
                        <Textarea
                          disabled={locked}
                          rows={5}
                          value={project.brief}
                          onChange={(e) => edit({ brief: e.target.value })}
                        />
                      </Field>
                    </div>
                    <Field label="Visual style">
                      <Textarea
                        disabled={locked}
                        value={project.style}
                        onChange={(e) => edit({ style: e.target.value })}
                      />
                    </Field>
                    <Field label="Resolution">
                      <Input
                        disabled={locked}
                        value={project.size}
                        onChange={(e) => edit({ size: e.target.value })}
                      />
                    </Field>
                  </div>
                )}
                {tab === "references" && (
                  <div className="space-y-4">
                    <p className="text-sm text-muted-foreground">
                      Generate each character, setting, prop, or style once.
                      Shots reuse these IDs for continuity.
                    </p>
                    {project.references.map((ref, index) => (
                      <section
                        key={ref.id}
                        className="grid gap-3 rounded-lg border border-border p-4 sm:grid-cols-[150px_minmax(0,1fr)]"
                      >
                        <div>
                          {ref.path ? (
                            <img
                              className="w-full rounded-md"
                              src={artifactUrl(project.id, ref.path)}
                              alt={ref.name}
                            />
                          ) : (
                            <div className="flex h-28 items-center justify-center rounded-md bg-muted text-xs text-muted-foreground">
                              Not generated
                            </div>
                          )}
                          <p className="mt-2 break-all text-xs">
                            {ref.id} · {ref.status}
                          </p>
                        </div>
                        <div className="space-y-3">
                          <div className="grid gap-3 sm:grid-cols-2">
                            <Field label="Name">
                              <Input
                                disabled={locked}
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
                              value={ref.prompt}
                              onChange={(e) =>
                                patchRef(index, { prompt: e.target.value })
                              }
                            />
                          </Field>
                          <Failure text={ref.error} />
                          <div className="flex flex-wrap gap-2">
                            <Button
                              disabled={mediaLocked || ref.status === "ready"}
                              className={actionClass}
                              onClick={() =>
                                void act("generate reference", () =>
                                  media("generate_reference", ref.id),
                                )
                              }
                            >
                              Generate reference
                            </Button>
                            <Button
                              variant="outline"
                              disabled={mediaLocked}
                              className={actionClass}
                              onClick={() =>
                                void act("reset reference", () =>
                                  reset("reset_reference", ref.id),
                                )
                              }
                            >
                              Reset
                            </Button>
                            <Button
                              variant="ghost"
                              disabled={locked}
                              className={actionClass}
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
                          </div>
                        </div>
                      </section>
                    ))}
                    <Button
                      variant="outline"
                      className={actionClass}
                      disabled={locked}
                      onClick={() =>
                        edit({
                          references: [
                            ...project.references,
                            {
                              id: `ref_${uid()}`,
                              kind: "character",
                              name: "",
                              prompt: "",
                              path: "",
                              status: "draft",
                              error: "",
                            },
                          ],
                        })
                      }
                    >
                      <Plus className="size-4" />
                      Add reference
                    </Button>
                  </div>
                )}
                {tab === "shots" && (
                  <div className="space-y-4">
                    <p className="text-sm text-muted-foreground">
                      {project.shots.reduce(
                        (n, s) => n + s.duration_seconds,
                        0,
                      )}{" "}
                      planned seconds / {project.target_seconds} target. A
                      continuation starts from the previous clip’s last frame.
                    </p>
                    {project.shots.map((shot, index) => (
                      <section
                        key={shot.id}
                        className="space-y-3 rounded-lg border border-border p-4"
                      >
                        <div className="flex flex-wrap justify-between gap-2">
                          <h3 className="font-medium">
                            Shot {index + 1} · {shot.status}
                            {shot.video_job_id ? ` (${shot.progress}%)` : ""}
                          </h3>
                          <div className="flex gap-1">
                            <Button
                              className={actionClass}
                              variant="ghost"
                              disabled={locked || index === 0}
                              onClick={() => moveShot(index, -1)}
                            >
                              Move up
                            </Button>
                            <Button
                              className={actionClass}
                              variant="ghost"
                              disabled={
                                locked || index === project.shots.length - 1
                              }
                              onClick={() => moveShot(index, 1)}
                            >
                              Move down
                            </Button>
                          </div>
                        </div>
                        <Field label="Shot prompt">
                          <Textarea
                            disabled={locked}
                            value={shot.prompt}
                            onChange={(e) =>
                              patchShot(index, { prompt: e.target.value })
                            }
                          />
                        </Field>
                        <div className="grid gap-3 sm:grid-cols-3">
                          <Field label="Clip seconds">
                            <Input
                              disabled={locked}
                              type="number"
                              min={1}
                              max={120}
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
                              value={shot.notes}
                              onChange={(e) =>
                                patchShot(index, { notes: e.target.value })
                              }
                            />
                          </Field>
                        </div>
                        <fieldset className="flex flex-wrap gap-3">
                          <legend className="mb-1 text-sm text-muted-foreground">
                            Visual references
                          </legend>
                          {project.references.map((ref) => (
                            <label
                              key={ref.id}
                              className="flex min-h-11 items-center gap-2 text-sm"
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
                        </fieldset>
                        <div className="grid gap-3 sm:grid-cols-3">
                          {shot.keyframe_path && (
                            <figure>
                              <img
                                className="max-h-48 w-full rounded-md object-contain bg-muted"
                                src={artifactUrl(
                                  project.id,
                                  shot.keyframe_path,
                                )}
                                alt={`Shot ${index + 1} keyframe`}
                              />
                              <figcaption className="text-xs text-muted-foreground">
                                Keyframe
                              </figcaption>
                            </figure>
                          )}
                          {shot.video_path && (
                            <video
                              controls
                              preload="metadata"
                              className="max-h-48 w-full rounded-md bg-muted"
                              src={artifactUrl(project.id, shot.video_path)}
                            />
                          )}
                          {shot.last_frame_path && (
                            <figure>
                              <img
                                className="max-h-48 w-full rounded-md object-contain bg-muted"
                                src={artifactUrl(
                                  project.id,
                                  shot.last_frame_path,
                                )}
                                alt={`Shot ${index + 1} final frame`}
                              />
                              <figcaption className="text-xs text-muted-foreground">
                                Last frame
                              </figcaption>
                            </figure>
                          )}
                        </div>
                        <Failure text={shot.error} />
                        <div className="flex flex-wrap gap-2">
                          <Button
                            variant="outline"
                            className={actionClass}
                            disabled={mediaLocked || !!shot.keyframe_path}
                            onClick={() =>
                              void act("generate keyframe", () =>
                                media("generate_keyframe", shot.id),
                              )
                            }
                          >
                            Create keyframe
                          </Button>
                          <Button
                            className={actionClass}
                            disabled={
                              mediaLocked ||
                              !!shot.video_job_id ||
                              shot.status === "submission_unknown"
                            }
                            onClick={() =>
                              void act("generate clip", () =>
                                media("generate_video", shot.id),
                              )
                            }
                          >
                            Generate clip
                          </Button>
                          <Button
                            variant="outline"
                            className={actionClass}
                            disabled={
                              mediaLocked ||
                              !shot.video_job_id ||
                              shot.status === "ready"
                            }
                            onClick={() =>
                              void act("poll clip", () =>
                                media("poll_video", shot.id),
                              )
                            }
                          >
                            Check render
                          </Button>
                          <Button
                            variant="outline"
                            className={actionClass}
                            disabled={mediaLocked}
                            onClick={() =>
                              void act("reset shot", () =>
                                reset("reset_shot", shot.id),
                              )
                            }
                          >
                            Reset shot
                          </Button>
                          <Button
                            variant="ghost"
                            className={actionClass}
                            disabled={locked}
                            onClick={() =>
                              edit({
                                shots: project.shots.filter(
                                  (_, i) => i !== index,
                                ),
                              })
                            }
                          >
                            Remove
                          </Button>
                        </div>
                      </section>
                    ))}
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        className={actionClass}
                        disabled={locked}
                        onClick={() =>
                          edit({
                            shots: [
                              ...project.shots,
                              {
                                id: `shot_${uid()}`,
                                prompt: "",
                                reference_ids: project.references.map(
                                  (r) => r.id,
                                ),
                                duration_seconds: settings?.video.seconds ?? 8,
                                continuity: project.shots.length
                                  ? "continue"
                                  : "cut",
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
                          })
                        }
                      >
                        <Plus className="size-4" />
                        Add shot
                      </Button>
                      <Button
                        className={actionClass}
                        disabled={
                          mediaLocked ||
                          !project.shots.length ||
                          project.shots.some((s) => s.status !== "ready")
                        }
                        onClick={() =>
                          void act("assemble", () => media("assemble"))
                        }
                      >
                        Assemble final video
                      </Button>
                    </div>
                    {project.final_path && (
                      <video
                        aria-label="Final video"
                        controls
                        preload="metadata"
                        className="max-h-[32rem] w-full rounded-lg bg-muted"
                        src={artifactUrl(project.id, project.final_path)}
                      />
                    )}
                  </div>
                )}
                {tab === "research" && (
                  <div className="space-y-5">
                    <section>
                      <h3 className="mb-2 font-medium">Observed trends</h3>
                      {project.research.length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                          Run research to collect source URLs, observation
                          times, and available metrics. No trends have been
                          recorded yet.
                        </p>
                      ) : (
                        project.research.map((t, i) => (
                          <article
                            key={i}
                            className="mb-3 rounded-md border border-border p-3"
                          >
                            <a
                              className="text-primary underline"
                              target="_blank"
                              rel="noreferrer"
                              href={t.url}
                            >
                              {t.title}
                            </a>
                            <p className="text-xs text-muted-foreground">
                              Observed {t.observed_at}
                            </p>
                            <p className="mt-2 whitespace-pre-wrap text-sm">
                              {t.notes}
                            </p>
                            <p className="text-xs">
                              {Object.entries(t.metrics ?? {})
                                .map(([k, v]) => `${k}: ${v}`)
                                .join(" · ")}
                            </p>
                          </article>
                        ))
                      )}
                    </section>
                    <section>
                      <h3 className="mb-2 font-medium">Original ideas</h3>
                      {project.ideas.length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                          Run plan after research to propose ideas and build the
                          shot list.
                        </p>
                      ) : (
                        project.ideas.map((idea) => (
                          <label
                            key={idea.id}
                            className="mb-3 flex gap-3 rounded-md border border-border p-3"
                          >
                            <input
                              type="radio"
                              name="idea"
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
                            <span>
                              <strong>{idea.title}</strong>
                              <p className="text-sm">{idea.hook}</p>
                              <p className="text-xs text-muted-foreground">
                                {idea.rationale}
                              </p>
                            </span>
                          </label>
                        ))
                      )}
                    </section>
                  </div>
                )}
                {tab === "publish" && (
                  <div className="space-y-4">
                    <Field label="Platform">
                      <select
                        disabled={locked}
                        className={control}
                        value={project.platform}
                        onChange={(e) =>
                          edit({ platform: e.target.value, account_id: "" })
                        }
                      >
                        {[
                          "youtube",
                          "tiktok",
                          "instagram",
                          "facebook",
                          "x",
                          "threads",
                        ].map((p) => (
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
                    <Link
                      className="text-sm text-primary underline"
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
                    <p className="text-sm">
                      Publication: {project.publication.status}
                    </p>
                    <Failure text={project.publication.error} />
                    {project.publication.post_url && (
                      <a
                        className="text-primary underline"
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
                          className="max-h-96 w-full rounded-lg bg-muted"
                          src={artifactUrl(project.id, project.final_path)}
                        />
                        <a
                          className="inline-flex min-h-11 items-center text-primary underline"
                          href={artifactUrl(project.id, project.final_path)}
                          download
                        >
                          Download final MP4
                        </a>
                      </>
                    ) : (
                      <p className="text-sm text-muted-foreground">
                        Assemble the shots before uploading.
                      </p>
                    )}
                    {["blocked", "uploading"].includes(
                      project.publication.status,
                    ) && (
                      <div className="space-y-2 rounded-md border border-border p-3">
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
                          variant="outline"
                          className={actionClass}
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
                )}
                {tab === "schedule" && (
                  <div className="space-y-4">
                    <form
                      className="grid gap-3 rounded-lg border border-border p-4 sm:grid-cols-2"
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
                          value={schedule.name}
                          onChange={(e) =>
                            setSchedule({ ...schedule, name: e.target.value })
                          }
                        />
                      </Field>
                      <Field label="Cron expression">
                        <Input
                          required
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
                      <p className="text-xs text-muted-foreground sm:col-span-2">
                        Uses the existing scheduler timezone. Jobs needing
                        interactive login or approval stop with a blocked
                        reason.
                      </p>
                      <Button
                        type="submit"
                        className={actionClass}
                        disabled={
                          !!busy ||
                          (schedule.content_stage === "publish" &&
                            schedule.publish_mode === "draft")
                        }
                      >
                        Create schedule
                      </Button>
                    </form>
                    {jobs
                      .filter((j) => j.meta?.content_project_id === project.id)
                      .map((j) => (
                        <div
                          key={j.id}
                          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3"
                        >
                          <div>
                            <p className="font-medium">{j.name}</p>
                            <p className="text-xs text-muted-foreground">
                              {j.schedule} · {j.meta?.content_stage} ·{" "}
                              {j.meta?.publish_mode} ·{" "}
                              {j.enabled ? "enabled" : "disabled"}
                            </p>
                          </div>
                          <Button
                            variant="outline"
                            className={actionClass}
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
                        </div>
                      ))}
                    <Link to="/automation/schedules" className="text-sm text-primary underline">
                      Open scheduler and run history
                    </Link>
                  </div>
                )}
              </>
            )}
          </main>
        </div>
      )}
    </PageLayout>
  );
}
