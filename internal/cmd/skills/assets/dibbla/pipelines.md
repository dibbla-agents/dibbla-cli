# Scheduled batch: pipelines

A **pipeline** is Dibbla's scheduled batch: one Go job, bound to parameter
values, an optional cron expression and a concurrency policy — and, since it
runs while nobody is watching, a set of alerts that reach a person when it
breaks or stops running.

This is the answer to "I need something to run every night", "run this report
every hour", "a scheduled import that tells me when it fails". Reach for it
before inventing a monitor of your own.

## 1. Which surface does the user actually want?

Four things on the platform look schedule-shaped. Picking the wrong one is the
most expensive mistake in this area, so decide here first:

| The user says | Use | Why |
|---|---|---|
| "Run this job every night and **tell me if it breaks**" | **Pipeline** (this doc) | The only scheduled surface with built-in failure *and* silence alerts, a run history, per-task progress and logs |
| "Run a container on a cron" | `jobs:` in `dibbla.yaml` (K8s CronJob — see [manifest.md § 15](manifest.md)) | Simple, no worker to keep alive — **but nothing notifies you when it fails**. If the user wants an alert, this is the wrong surface |
| "Check that my deployed app still works" | [Application checks](manifest.md) (`dibbla-checks.yaml`) | Probes a running app from outside; `application.check.*` events. Not a way to run batch work — a check that pokes an endpoint your batch updates is the workaround people invent when they have not found pipelines |
| "Run my workflow graph on a schedule" | The **Scheduler** in the workflows app | Schedules *workflows*, not jobs. Same app, different model |
| "Run a `dibbla-task.yaml` locally" | `dibbla run` | Unrelated: a local task runner that also uses the word *pipeline*. Nothing scheduled, nothing hosted |

## 2. The job is Go code in a worker

A pipeline runs a **job** that a worker registers over the SDK. The worker is
a long-lived process (deploy it as a Dibbla app like anything else); when a
pipeline is due, the platform dispatches a `job_trigger` event to it.

```go
type ReportJob struct{}

func (j *ReportJob) GetJobID() string   { return "nightly_report" }
func (j *ReportJob) GetJobName() string { return "Nightly Report" }

func (j *ReportJob) GetParameters() []jobs.JobParameter {
    return []jobs.JobParameter{
        {Name: "region",  Type: "string",  Required: true},
        {Name: "dry_run", Type: "boolean", Required: false, Default: true},
    }
}

func (j *ReportJob) Execute(ctx *jobs.JobContext) error {
    ctx.Logger.TaskStarted("collect")
    ctx.Logger.Info("collecting for " + ctx.GetStringArg("region", "unset"))
    ctx.Logger.TaskCompleted()

    for i := 0; i < 25; i++ {
        ctx.Logger.Progress(i+1, 25, "processing rows")
    }
    ctx.Logger.CompleteProgress()
    return nil // a returned error is what makes the run fail — and alert
}
```

Register it with `server.RegisterJob(&ReportJob{})` before `server.Start()`.
Full SDK model — server options, `JobContext` arg helpers, the `Logger`
task/progress API, the `internal/` import footgun — is in
[sdk-go.md](sdk-go.md).

Two things that decide how the run *reads* to a human:

- **What you log is what the UI shows.** `TaskStarted` / `TaskCompleted` /
  `TaskSkipped` draw the task list, `Progress(i, total, msg)` drives the bar,
  `Info` / `Warn` / `Error` become log lines. A job that logs nothing renders
  an empty run screen.
- **Return an error to fail the run.** Swallowing the error and returning
  `nil` makes a broken run look successful — and no alert is sent, because
  from the platform's side nothing broke.

## 3. Creating and running pipelines: the console's Pipelines tab

Pipelines live in the console: **Pipelines** in the main navigation
(`console.dibbla.com/pipelines`). There is **no `dibbla` CLI command and no MCP
tool for pipelines** — do not invent one, and do not tell the user to put a
pipeline in `dibbla.yaml`. (The older separate Pipelines app is still reachable
through **Old view** in the tab's header and shows the same pipelines; the
Scheduler for *workflows* lives there.)

**+ New pipeline** binds one job that a connected worker advertises to:

- **Job** — picked from the job catalog; each job is tagged *Online* or
  *Offline* depending on whether a worker serving it is connected.
- **Title** and optional **Description** — what this pipeline is for. Name
  variants so they share a prefix: `Confluence – Engineering`,
  `Confluence – Sales` (see § 4).
- **Parameters** — typed inputs built from the job's declared parameters.
- **Schedule** — *Manual only*, *Hourly*, *Daily*, *Weekly* or *Custom cron*,
  in UTC, with a preview of the next runs. No schedule = manual trigger only.
- **Runs after** (optional) — the pipelines this one is expected to follow
  (§ 4).
- **If a run is still going** — *Skip if still running* (`skip`), *Queue
  behind the running one* (`queue`) or *Runs may overlap* (`allow`). This is a
  real operational decision: a nightly report usually wants Skip, a queue
  drainer usually wants Queue.

Steering, from the row (▶ and the ⋯ menu) and from the pipeline's own page:
**Run now**, **Stop run** (while a run is going, § 5), **Edit schedule**,
**Edit**, **Duplicate** (same job, parameters and schedule — the way to add a
variant), **Enable/Disable** (Disable stops the schedule; nothing alerts while
disabled) and **Delete**.

What a person sees:

- **The list** — tiles *Total / Failing / Running / Disabled* (click to
  filter), a filter box that also matches database and app, and the columns
  *Pipeline, Job, Writes to, Schedule, Last status, Last run*. Pipelines
  linked with *Runs after* are listed first, together (§ 4).
- **The pipeline page** (`/pipelines/<id>`) — a fact row (*Job & worker*,
  *Writes to*, *Schedule* incl. *Runs after*, *Concurrency*, *Last success*,
  *Alerts*), then *Runs*: a history bar and a table with *Status, Started,
  Duration, Error*. Clicking a run opens the **run panel** — parameters, the
  task list (*Progress*) and searchable *Logs*. This is where "why did last
  night's run fail?" is answered without asking whoever built it.

With no worker connected, every job shows *Offline* and nothing runs. That is
the normal first experience, not a misconfiguration.

## 4. Making a set of pipelines readable: Runs after, variants, Writes to

A handful of pipelines that together solve one problem used to be scattered
across a flat list — order hidden in cron times, the target only in the code,
one copy per parameter value. Three things make the structure visible. Set
them up whenever you create more than one pipeline for the same purpose.

### Order — `runs_after`

A pipeline can declare the pipelines it **runs after**. The console lists
linked pipelines together at the top, under one line that says who runs after
whom and what they end up writing to:

```
LINKED BY RUNS AFTER   Discourse sync, Reddit sync → Classify feedback · writes to lumen_feedback (app lumen-feedback)
```

Read the arrow as "runs after", nothing more. Names before the same arrow are
not claimed to run at the same time — each keeps its own schedule — and the
group has no name or settings of its own: it is just the pipelines that
`runs_after` connects. The dependent pipeline's row and page say *Runs after:
Discourse sync, Reddit sync*. Everything not linked is listed under *Other
pipelines*. This is for understanding how chained pipelines feed a database
and app — it is **not** an orchestrator.

**`runs_after` is information only.** Cron still decides when each pipeline
runs; nothing is held back or chained. If the dependent pipeline's latest run
started while an upstream run was still going, the list shows **Started
early** and its page explains which upstream run overlapped — but the run went
ahead. So the schedule must still leave room: put the dependent cron after the
upstreams normally finish, and make the job tolerate reading data that is
still arriving (incremental reads, not "assume the sync finished").

Rules the platform enforces: the ids must be pipelines in the same
organization, a pipeline cannot run after itself, and a cycle is refused
(`runs_after would make a cycle: A → B → A`). Deleting a pipeline removes it
from every `runs_after`.

**Worked example — "set it up so Classify runs after the syncs":**

1. Check the schedules: the syncs at e.g. 06:00 UTC, Classify at 08:00 UTC
   (after the syncs normally finish). Change Classify's schedule with **Edit
   schedule** if it is earlier.
2. Open Classify → **Edit** → **Runs after** → add *Discourse sync* and
   *Reddit sync* → **Save**.
3. The list now shows the three together under *Linked by Runs after*:
   `Discourse sync, Reddit sync → Classify`.
4. Tell the user plainly: this documents the order and warns if Classify
   starts early; it does not make Classify wait. If it must truly wait, the
   job itself has to check (or the schedule has to leave enough margin).

The same field is available over the pipelines API (`PUT /pipelines/<id>`
with `"runs_after": ["<id>", …]`; `[]` clears it), but the console is the
supported surface.

### Variants — one job, several parameter sets

The same job run with different parameters — Confluence per space, a report
per region — is several pipelines with the same job. The list shows them as
**one row** with the shared title prefix (`Confluence`) that expands into the
variants, each with its parameters (`space=ENG`). The row's status is the most
urgent of its variants. There is no separate group object: **variants are
pipelines that share a job**. Create one with **Duplicate** and change only
what differs. Use a common prefix in the titles; without one the row is named
after the job id.

### Target — **Writes to**

Each pipeline shows the database it writes to and the app that owns that
database (*Writes to: `feedback` (feedback-portal)*). It comes from the
**job's code**, not from a pipeline parameter, so what is shown is always where
the job actually writes. Implement the optional `jobs.DatabaseWriter`
interface on the job:

```go
// Shown as "Writes to" on every pipeline that runs this job.
func (j *ClassifyJob) WritesToDatabase() string { return "feedback" }
```

`server.RegisterJob` picks it up; nothing else to register. Requires sdk-go
**v0.0.27 or later**. A job without it shows `—`. A name the organization has
no database for shows a warning, which usually means a typo or a database that
was never created.

## 5. Stopping a run — the cancel contract

**Stop run** (row menu, pipeline page or run panel) asks the worker to stop at
once. A job written against sdk-go **v0.0.27 or later** sees the stop through
its context and should end quickly:

```go
func (j *SyncJob) Execute(ctx *jobs.JobContext) error {
    for _, page := range pages {
        if ctx.IsCancelled() {      // check before each write
            return ctx.Err()        // context.Canceled
        }
        if err := fetchAndStore(ctx, page); err != nil { // pass ctx on
            return err
        }
    }
    return nil
}
```

- `*jobs.JobContext` is a `context.Context`: pass it to HTTP and database
  calls, `select` on `ctx.Done()` while waiting, and return `ctx.Err()`.
  `context.Cause(ctx.Context()) == jobs.ErrRunCancelled` tells a stop apart
  from other cancellations.
- Status goes *Running → Stopping… → Stopped*. A stopped run is **not a
  failure**: no `pipeline.run.failed` alert.
- **Older workers** do not hear the stop. If the worker has not confirmed
  within about a minute, the run is closed as **Stopped (unconfirmed)** — the
  job may still be running on the worker, and anything it reports later is
  ignored. The schedule is released, so the next run goes as usual. Upgrade
  the worker's sdk-go to make Stop actually stop the work.
- A stop that arrives after the job already finished is answered with
  *Already stopped* or the run's real outcome.

## 6. Alerts — what reaches a person

Four events, delivered through the platform's ordinary notification system
(email, Slack, webhook, Discourse):

| Event | Fires when |
|---|---|
| `pipeline.run.failed` | The pipeline's last run went from anything that was not failing into failed |
| `pipeline.run.recovered` | A previously failing **or** silent pipeline completes a run |
| `pipeline.run.host_offline` | No worker serving the job was connected at a scheduled time, so nothing ran |
| `pipeline.run.missed` | A scheduled run did not happen for another reason — stored parameters that no longer parse, or a trigger that could not be handed to the worker |

**Alerts follow a transition, not a run.** A pipeline that fails every night
alerts once; the next successful run sends `pipeline.run.recovered`. A
first-ever run that fails does alert. The rule covers any terminal run,
scheduled or triggered by hand.

**Silence alerts once as well.** A worker down for a week against a
five-minute schedule is one `host_offline` event, not two thousand: it fires
on *entering* the state, and only a terminal run clears it — which also
re-arms it, so a second outage after a recovery is news again.

**Three cases deliberately never alert**, because alerting on them trains
people to ignore the alert that matters:

- a run skipped by the concurrency policy (`Skip` is the policy working as
  asked),
- a disabled pipeline (paused on purpose),
- a pipeline with no cron expression (it was never promised a time).

**What an alert carries:** the pipeline name, a one-line cause, the run id and
a link to the run in the console. The cause is bounded and redacted at the
source — a worker's error string is the likeliest place for a secret to appear
in plain text, so token-shaped and credential-shaped substrings are replaced
and multi-line text is cut to its first line. Run logs, stack traces and the
*values* of trigger parameters are never in the notification. The silence
alerts additionally state when the pipeline last completed a run successfully,
or say outright that it never has.

**Subscribing.** The first `pipeline.run.*` event in an organization seeds a
subscription to all four types on the channel the organization has already
connected — so a nightly batch alerts even if nobody opened the notification
settings. With no channel connected anywhere, nothing is seeded and the
default is still available the day one is connected. A subscription the user
disables or deletes stays gone; it is never re-created. To route it
explicitly, an owner or admin runs
`dibbla notifications add 'pipeline.run.*' --org-wide --target ops@example.com`
(or `--channel slack` for the organization's connected Slack), then
`dibbla notifications test <id>` to see it arrive. The event type comes from
the catalog (`dibbla notifications events`); it is not free text. The console's
**Organization settings → Integrations** does the same.

When a user asks for "a nightly job that alerts if it breaks", this is the
whole answer: a job in a worker, a pipeline with a cron and `Skip`, and the
default subscription on their Slack or email channel. They do not need to
build a health endpoint or an application check to watch it.

## 7. Gotchas

- **A pipeline is only as alive as its worker.** Deploy the worker as an app
  so it restarts with the platform; a laptop process that exits at 18:00 turns
  every night's run into `pipeline.run.host_offline`.
- **The SDK defaults to production.** Set `GRPC_SERVER_ADDRESS` deliberately if
  the worker should register anywhere else — see [sdk-go.md](sdk-go.md).
- **Parameters are stored per pipeline.** Changing a job's declared parameters
  can leave a pipeline holding values the job no longer accepts; that surfaces
  as `pipeline.run.missed`, not as a failed run.
- **`recovered` is a real event, and worth keeping subscribed.** Without it a
  fixed pipeline is indistinguishable from one still broken and quiet.
- **Alerts are per organization, not per app.** Subscriptions live in
  organization settings even though a pipeline belongs to one worker.
