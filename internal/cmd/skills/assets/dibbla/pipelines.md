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

## 3. Binding the job to a schedule

Pipelines are created in the console (the workflows app, **Pipelines**); there
is **no `dibbla` CLI command for pipelines** — do not invent one, and do not
tell the user to put a pipeline in `dibbla.yaml`. The worker appears under
**Tool servers** once it has connected, with the jobs it advertises; a
pipeline then binds one of those jobs to:

- **Parameter values** — filled from the job's declared parameters.
- **A cron expression** (optional). No cron = manual trigger only.
- **A concurrency policy** — `Skip`, `Queue`, or `Allow parallel`. This is a
  real operational decision: a nightly report usually wants `Skip`, a queue
  drainer usually wants `Queue`.

With no worker connected, every pipeline screen is an empty state waiting for
one. That is the normal first experience, not a misconfiguration.

## 4. Alerts — what reaches a person

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
explicitly: **Organization settings → Integrations**, event type
`pipeline.run.*` (the box is free text — type the pattern exactly).

When a user asks for "a nightly job that alerts if it breaks", this is the
whole answer: a job in a worker, a pipeline with a cron and `Skip`, and the
default subscription on their Slack or email channel. They do not need to
build a health endpoint or an application check to watch it.

## 5. Gotchas

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
