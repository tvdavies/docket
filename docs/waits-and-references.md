# Waits, references, and activity

Workflow status answers **which stage owns a task**. An active wait answers **which external condition prevents that stage from continuing**. Keeping these concepts separate avoids adding temporary lanes for CI, feedback, approvals, and other dependencies.

## One active wait

A task may have zero or one active wait:

```yaml
wait:
  id: wait-8f17d9c8297c9e22
  kind: plan_feedback
  reason: Awaiting review of plan v2
  reference: https://example.com/plan-v2
  since: 2026-08-18T15:00:00Z
  actor: planner
```

Set a wait without changing status:

```sh
docket wait set JOB-0001 \
  --kind plan_feedback \
  --reason "Awaiting review of plan v2" \
  --ref https://example.com/plan-v2
```

The command emits `task.waiting`. A second wait is rejected until the active one is resolved.

A human or deterministic watcher must resolve the exact wait ID it observed:

```sh
WAIT_ID=$(docket wait show JOB-0001 --json | jq -r .id)
docket comment JOB-0001 "Approved without changes"
docket wait resolve JOB-0001 --wait-id "$WAIT_ID" --result approved
```

Resolution emits `task.resumed`; it does not move the task. Requiring the wait ID prevents a delayed CI process or webhook from clearing a newer wait that replaced the condition it originally observed.

Wait kinds are workspace conventions. Docket accepts letters, numbers, dots, hyphens, and underscores. Dispatch-style examples include `plan_feedback`, `github_pr_change`, `scope_decision`, and `human_merge`.

## Typed references

References are durable external links rather than prose that automation must parse from comments:

```sh
docket reference add JOB-0001 \
  --kind plan \
  --url https://files.example/plan.html \
  --title "Plan v2"

docket reference add JOB-0001 \
  --kind pr \
  --url https://github.com/example/repo/pull/42

docket reference list JOB-0001
docket reference remove JOB-0001 ref-8c7d...
```

Each reference has a stable ID, kind, URL, optional title, timestamp, and actor. Adding and removing references emits `task.reference_added` and `task.reference_removed`.

Absolute URI schemes are supported. Use HTTPS for shareable artifacts and `file:///...` only for deliberate machine-local references such as local transcripts.

## Activity timeline

`docket show` and the web task drawer expose one chronological activity stream assembled from existing authoritative records:

- task events and status transitions from `events.jsonl`;
- immutable comments;
- session attach/detach records;
- wait and resume events; and
- attachment and reference events.

The timeline is a computed view, not a second source of truth. Existing workspaces require no migration. The JSON context bundle retains the original `comments`, `sessions`, and other collections while also exposing `activity` for agents and interfaces that need temporal context.

## Automation pattern

A token-free external watcher should:

1. read the active wait and remember its ID;
2. inspect the external system without invoking a model;
3. do nothing while its fingerprint is unchanged;
4. add any new context as comments, attachments, or references; and
5. resolve that exact wait ID.

A `task.resumed` handler can then wake the stage owner. Docket records the durable coordination state; provider-specific polling, webhooks, and agent spawning remain outside Docket.
