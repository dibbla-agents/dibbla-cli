# GDPR-ready on Dibbla

This is the companion to [secure-app.md](secure-app.md) for the other question a
customer asks: not *"is it secure?"* but *"what do you do with my data?"* Same
shape — seven situations in one sentence each, what Dibbla already handles, one
sentence to paste to your agent, and how you check afterwards that it was really
done.

You do not need to read the GDPR. You need to know which parts are Dibbla's, which
parts are yours, and what the answer looks like when someone asks for it in writing.

## Your role, and Dibbla's

Two sentences carry everything below.

**For the data in your app, you are the controller and Dibbla is your processor.**
You decided to collect it, you decided what it is for; Dibbla only stores and runs
it because you told it to. Dibbla never uses your application's data for its own
purposes and never trains AI models on it.

**Towards your own customers, Dibbla is a sub-processor.** When a customer asks you
to sign a data processing agreement, they are asking you to name everyone who can
touch their data underneath you. Dibbla is on that list, and so is every service
*you* added — your mail sender, your error tracker, your analytics.

There is a third case worth separating out so you do not mix it up: for **your**
account data — your e-mail address, your organisation, your billing details —
Dibbla is the controller, and that is covered by Dibbla's own privacy policy at
`dibbla.com/privacy`. That one is not your responsibility.

## What Dibbla takes care of

Facts you can quote to a customer today. Nothing here is aspirational.

| | |
|---|---|
| **Where the data is** | Compute, databases, object storage and backups run on Hetzner in **Germany (Nuremberg) and Finland (Helsinki)**. Cloudflare terminates TLS at the edge, so traffic passes through it in transit. Stripe (Ireland) sees billing data for *your* account, never your end users. |
| **Who else can see it** | Google and Microsoft, as sign-in providers, see the e-mail address and name of whoever signs in. An AI model provider (OpenAI, Anthropic or AWS Bedrock) is involved **only if you switch AI features on**, and then only the one your application calls. Nothing goes to a model provider by default. |
| **Logs** | Everything your app writes to stdout lands in the platform's log store together with the platform's own logs. **Production retention is 180 days.** Platform log lines identify people by `user_id` and `org_id`, not by e-mail address. Prompts and responses through the AI gateway are kept **30 days** and then erased. |
| **Deletion at the end** | Deleting an app removes the workload, its volumes, its secrets, its images, its build history and the git repository it deploys from. Deleting the organisation additionally removes every managed database and bucket, normally within minutes. After that a deleted row survives in production backups for **at most 30 days**, and log lines age out with the 180-day retention. |
| **AI training** | Never. Not on your code, not on your data, not on your customers' data. |

**Two things that are honest to know.** Managed databases and buckets are *not*
deleted with an app — they belong to the organisation, because people share them
between apps and redeploy. `dibbla apps delete` names them and gives you the command
that removes them; if you skip that step the data is still there. And the logs of a
deleted app are not erased on the spot today; they age out with the 180-day
retention like every other log line.

**One thing that is not ready yet, stated plainly so you do not promise it.**
Dibbla's written data processing agreement and its published sub-processor list at
`dibbla.com/subprocessors` are being prepared and are not published at the time of
writing. The table above is what is factually true and what you can put in your own
sub-processor list in the meantime. Do not tell a customer that Dibbla has signed a
DPA with you until the document exists — check `dibbla.com/terms` for the current
state before you answer a procurement question in writing.

## How to use this file

Paste the **Ask your agent** sentence for one point into whatever agent you build
with. Do them one at a time; each answer is short.

Several points end with what `REVIEW.md` should show. `REVIEW.md` is the security
review your agent writes at the root of your project before deploying — `dibbla
deploy` refuses to upload without it, and it is committed with your code. Its
**Personal data (GDPR)** section is the one part of that report written to be read
outside your team: it is what you hand a customer who asks how you handle their
data.

**One file, added to — never replaced.** Everything here goes *into* the existing
`REVIEW.md`. Its `---` header block (`Review-status:`, `One-Sentence-Summary:`) is
what the deployments dashboard reads; an agent that rewrites the file from scratch
and drops the header leaves the deploy working and the status blank. If you are
unsure, tell your agent: *"add this to REVIEW.md, keep the header and the sections
that are already there."*

**Two points ask for documents, not code** — your privacy policy (point 2) and your
data processing agreement (point 3). An agent can draft both from what your code
actually does, which is the only way they end up true. Neither is legal advice, and
for a real procurement you will want a lawyer to read the result once.

---

## 1. You cannot say what personal data your app holds

**The risk.** A customer, or their lawyer, asks what you store about their users.
You answer from memory, miss the avatar bucket and the analytics table, and the
answer you gave in writing is wrong.

**Dibbla does.** Makes your agent write the inventory before every deploy: the
pre-deploy checklist's **Personal data (GDPR)** check is mandatory, and a deploy
whose report does not list the stores holding personal data is blocked. Dibbla
cannot know which of your fields are personal — only your code knows that — so the
platform's part is forcing the question, not answering it.

**Ask your agent.**

> "Add to REVIEW.md — keeping the header and the sections already there — the
> Personal data (GDPR) section of the guardrails report, with an inventory under
> four headings. **Stored:** every table, file and
> bucket that holds data about a person, named as store.field. **Deletion:** how one
> person is deleted and which stores that delete does not reach. **Logs:** whether
> any personal data is written to the logs. **Third parties:** who receives personal
> data and which fields they get. Then add, for each field, what it is used for and
> how long it is kept — say 'kept indefinitely' where that is the truth."

**How you know it's done.** `REVIEW.md` has the Personal data (GDPR) section with
those four headings filled in, plus a purpose and a retention for each store. You
can read it aloud to a customer without checking anything else. If the app holds no
personal data, one line saying so is the correct answer.

---

## 2. Your privacy policy does not match what your code does

**The risk.** The policy on your site was written before the feature that collects
phone numbers, or it was copied from a template and lists a cookie banner you never
installed. The gap between the two is the part a regulator reads first.

**Dibbla does.** Nothing here — this is your text about your users, and Dibbla
cannot write it for you. What Dibbla gives you is the ingredients for the part about
hosting: where the data is (Germany and Finland), who the sub-processors are, and
the retention figures in the table above.

**Ask your agent.**

> "Read the personal-data inventory in REVIEW.md and my current privacy policy, and
> list every difference: data the code collects that the policy does not mention,
> claims in the policy the code does not support, and missing items — purpose and
> legal basis per category, retention, the rights people have, and who the data is
> shared with. Then draft the corrected policy. Say that hosting is on Dibbla, in the
> EU (Hetzner, Germany and Finland), and list my other sub-processors by name."

**How you know it's done.** Every store in the inventory appears somewhere in the
policy, and every claim in the policy is one you could point at code for. The
sub-processor list in the policy matches the **Third parties** line in `REVIEW.md` —
if those two disagree, one of them is wrong.

---

## 3. A customer asks for a data processing agreement and you have nothing to send

**The risk.** Your first serious B2B customer sends a procurement questionnaire and
a DPA to sign. It asks you to list your sub-processors, state where data is stored
and confirm that you can delete on request. A week of improvising follows, and the
deal slows down.

**Dibbla does.** Keeps your data in the EU and is the sub-processor you name for
hosting. Dibbla's own DPA and published sub-processor list are in preparation (see
above) — until they exist, name Dibbla in your list with the facts from the table
above, which are true today and verifiable.

**Ask your agent.**

> "Draft a data processing agreement I can give my own customers, as an annex to my
> terms. Take the categories of data and data subjects from the personal-data
> inventory in REVIEW.md, state that hosting is in the EU with Dibbla (Hetzner,
> Germany and Finland) as sub-processor, list every other sub-processor I use with
> what it sees and where it is, and include deletion on termination, breach
> notification and assistance with data-subject requests. Flag anything you had to
> guess so I can check it with a lawyer."

**How you know it's done.** You have one document you can send the same day it is
asked for, its sub-processor list matches the **Third parties** line in `REVIEW.md`,
and nothing in it promises a control you do not have. The last part matters most: a
DPA that promises what your code cannot do is worse than no DPA.

---

## 4. Someone asks for a copy of everything you hold about them

**The risk.** A user exercises their right of access. You have thirty days, the data
sits in four tables and a bucket, and there is no way to assemble it except by hand —
so it is done badly, or late, or not at all.

**Dibbla does.** Nothing at this layer: only your code knows which rows are that
person's. What Dibbla does give you is the shape to build it in — a route in your
app, or a `dibbla.yaml` job you run on demand.

**Ask your agent.**

> "Add a way to export everything the app holds about one person, as JSON plus any
> files, covering every store in the REVIEW.md inventory — including uploads and
> anything in object storage. Make it reachable only by that person or by an admin,
> log that it ran, and note the route and the stores it covers in REVIEW.md."

**How you know it's done.** You can run it once for a real account and read the
result: it contains what the inventory says it should, and nothing belonging to
anyone else. `REVIEW.md` names the export route under the Personal data section.

---

## 5. Someone asks to be forgotten and you can only delete half

**The risk.** You delete the user row. Their name stays on old orders, in the search
index, in a cached profile and in last month's CSV export in a bucket — and you tell
them in writing that they were erased.

**Dibbla does.** Removes everything on its side when you remove the app or the
organisation, and gives you the outer bound in writing: a deleted database row is
unrecoverable from production backups after **30 days**, and log lines age out with
the 180-day retention. Inside your own database, deletion is yours — the pre-deploy
checklist blocks a deploy that stores personal data with no way to delete a person,
and warns when the delete path leaves copies behind.

**Ask your agent.**

> "Trace what happens when one person is deleted. Go through every store in the
> REVIEW.md inventory and tell me which ones the delete reaches and which it does
> not — child tables, search indexes, caches, queued jobs, exports and files in
> object storage. Fix the ones it misses, or say why they must stay, and record the
> result under Deletion in REVIEW.md."

**How you know it's done.** The **Deletion** line in `REVIEW.md` names the delete
path *and* every store it deliberately does not reach, with a reason for each. There
is no `BLOCKER: no way to delete a person` and no unexplained "incomplete deletion"
warning. When you tell a customer someone was erased, that line is what makes it
true.

---

## 6. You collect more than you need, and keep it forever

**The risk.** The signup form asks for a phone number nobody ever calls, sessions
from two years ago are still in the table, and every CSV export ever generated sits
in a bucket. None of it is used, all of it is yours to lose.

**Dibbla does.** Applies its own retention to everything it holds on your behalf —
logs 180 days, AI prompts and responses 30 days, backups 30 days — and gives you a
scheduled job (`dibbla.yaml` cron) to run your own cleanup. What you keep in your
own database is your decision; nobody will prompt you about it.

**Ask your agent.**

> "Go through the personal-data inventory in REVIEW.md. For each field, tell me what
> it is actually used for in the code, and flag anything collected but never read.
> Then propose a retention period for each store and a scheduled job that enforces
> it — sessions, tokens, uploads, exports and soft-deleted rows first. Record the
> periods under the Personal data section in REVIEW.md."

**How you know it's done.** Every store in the inventory has a retention next to it,
and a job that enforces it or an explicit "kept while the account exists". Anything
collected and never read is either removed from the form or justified in one line.

---

## 7. Personal data ends up in your logs and in services you added yourself

**The risk.** A debug line prints the whole request body, so e-mail addresses are in
the logs for months. An error tracker you added in an afternoon receives the same
payloads, on servers you never checked — and it is not in your privacy policy or
your sub-processor list.

**Dibbla does.** Keeps platform log lines pseudonymous — `user_id` and `org_id`, not
e-mail addresses — and retains logs for 180 days in production. But **what your app
prints, your app owns**: every line it writes to stdout is stored on the same terms,
for the same 180 days, and is readable by everyone in your organisation. Anything
you send to a third party from your own code leaves Dibbla entirely and is outside
all of the above.

**Ask your agent.**

> "Find every place this app writes personal data to the logs — request bodies,
> error payloads, user objects in log lines — and replace it with an id. Then list
> every third party the app sends personal data to: mail, SMS, analytics, error
> tracking, payment, AI. For each, name the fields that leave and where the service
> is hosted. Update the Logs and Third parties lines in REVIEW.md."

**How you know it's done.** The **Logs** line in `REVIEW.md` says ids only, and the
**Third parties** line names every recipient with the fields it gets — and matches
the sub-processor list in your privacy policy and your DPA. Three lists, one set of
names. When they agree, you can answer a procurement questionnaire from memory.

---

## What this file does not cover

Said out loud so you do not assume it is handled.

- **Cookies and tracking consent.** If you load analytics, ad pixels or third-party
  fonts, consent rules apply before they load. The pre-deploy checklist warns about
  trackers that fire without a consent gate; the banner itself is yours to build.
- **A personal-data breach.** If data leaks, you have 72 hours to notify your
  supervisory authority, and possibly the people affected. Knowing who was affected
  depends entirely on the inventory in point 1 — which is the practical reason to
  write it before you need it.
- **Transfers outside the EU/EEA.** Everything Dibbla stores stays in the EU. If
  *you* add a US service, that transfer is yours to justify.
- **Legal advice.** This file, and anything an agent drafts from it, is a starting
  point written from what your code actually does. Have a lawyer read the finished
  policy and DPA once.

---

## Where the words come from

<details>
<summary>GDPR articles for each point</summary>

The seven points are written from the situation backwards. This table maps them onto
the articles a lawyer or a procurement questionnaire will name.

| Point | Article |
|---|---|
| 1. You cannot say what you hold | Art. 30 (records of processing) · Art. 5(2) (accountability) |
| 2. Policy does not match the code | Art. 12–14 (information to data subjects) |
| 3. No agreement to send a customer | Art. 28 (processor obligations, sub-processors) |
| 4. A copy of everything you hold | Art. 15 (access) · Art. 20 (portability) |
| 5. Deleting only half | Art. 17 (erasure) · Art. 28.3(g) (deletion at end of service) |
| 6. More than you need, forever | Art. 5(1)(c) (data minimisation) · Art. 5(1)(e) (storage limitation) |
| 7. Logs and third parties | Art. 5(1)(f) (integrity and confidentiality) · Art. 28 (sub-processors) |
| Not covered: consent for trackers | Art. 6–7 · ePrivacy Directive |
| Not covered: breach | Art. 33–34 |
| Not covered: transfers | Chapter V |

Dibbla's own controls are described as aligned with ISO/IEC 27001:2022 Annex A.
Dibbla AB is not ISO 27001 certified, and nothing in this file should be presented
as a certification. The retention and deletion figures above describe the production
environment as configured at the time of writing; `dibbla.com/privacy` and, once
published, `dibbla.com/subprocessors` are the current authority.

</details>
