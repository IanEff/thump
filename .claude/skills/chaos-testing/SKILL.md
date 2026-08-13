---
name: chaos-testing
description: Use when running live tests or chaos scenarios against the thump rig (tilt up, chaos/*.sh, kubectl against ceph-lab/rook-gke/rook-gce-k3s) — covers the rig layout and how to wait on results without blind polling.
---

# Live testing & chaos engineering in thump

thump's real test surface is a live cluster, not just `task ci`. This skill covers
where that rig lives and — the part that's easy to get wrong — how to wait on a
chaos run's results without burning a fixed-length sleep on every cycle.

## Rig layout

- Three cluster profiles, picked by `tilt up -- --cluster=<name>` (`ceph-lab`,
  `rook-gke`, `rook-gce-k3s`, `thump-test`) — the `CLUSTERS` dict at the top of `Tiltfile` has the
  context/registry/values for each. `rook-gce-k3s` needs `just tunnel` (in
  `~/projects/ceph/rook-gce-k3s`) already running — Tilt has no hook to start it.
- Config overlay per profile: `config/{ceph-lab,rook-gke,rook-gce-k3s,thump-test}/{rattle,whir}`,
  selected by the Helm chart's `configProfile` value.
- Chaos scripts and chaos-mesh CRDs live in `chaos/` — shell scripts
  (`pg-num-starve.sh starve|restore`, `rgw-ratelimit-starve.sh`, `rgw-user-suspend.sh`)
  and YAML faults applied with `kubectl apply -f chaos/<file>.yaml` (network
  delay/loss, OSD chaos, RGW stress). Read the header comment in a script before
  running it — several encode which rig they target and why that fault shape was
  chosen over the alternatives (see `thump-running-notes.md` in the vault for the
  fuller record).
- **A script's own header doesn't always know it's been superseded** — the
  supersession note often lives in the *newer* file, not the old one. E.g.
  `stress-rgw.yaml` looks like a live mechanism on its own header, but
  `rgw-client-delay.yaml` (committed later, same target) documents *in its own
  header* why CPU-steal plateaus short of the SLO threshold. Before running an
  older-looking script, check `git log --oneline -- chaos/` or `ls -la chaos/`
  for a newer file targeting the same thing and read *its* header first.
- Full architecture/gotcha detail is in the vault (`thump-running-notes.md`), not
  duplicated here — read it live per the root `CLAUDE.md`.

## Verifying a leg is actually encrypted

A values file claiming encryption and a wire actually carrying it are two
different claims. The Phase R live-rig check found three of four uncommitted
encryption edits sitting "on disk" weren't live at all — see the
three-mechanism rule below for why. Check the live state with a command, not
the diff.

- **Cilium WireGuard (the substrate mesh, node-to-node):** `cilium encrypt
  status` should show a non-zero peer count on every node. `Encryption:
  Disabled` with the ArgoCD Application reporting `Synced` means the setting
  never reached the daemon — `Synced` only means the manifest matched, not
  that the daemon applied it.
- **MTU, after enabling WireGuard:** the rig runs `routingMode: tunnel`, so
  packets already carry VXLAN overhead before WireGuard adds its own header.
  A mis-detected underlay MTU shows up as **large transfers hanging while
  small ones succeed** — reads exactly like a slow backend and gets
  misdiagnosed as a beat problem. Check `cilium status --verbose | grep -i
  mtu` on a node before concluding anything about a beat's behavior.
- **NATS mTLS + the permission table (beat ↔ JetStream):** a negative-publish
  probe proves authentication and authorization at once — attempt a publish
  the cert shouldn't be allowed and confirm it's refused:
  ```
  kubectl -n thump exec deploy/clank -- \
    nats --server="$NATS_URL" --tlscert=… --tlskey=… --tlsca=… \
    pub thump.decisions '{}'
  ```
  Expected: a permissions violation — clank's cert has no publish grant on
  `thump.decisions`, only hiss's does. Pair it with a `tcpdump` on 4222
  during a real handshake: a TLS handshake with no readable subject names in
  the capture is the only proof the wire itself is encrypted, not just that
  `nats.conf` says so.
- **Ceph msgr2:** `ceph config get mon ms_cluster_mode` should report
  `secure`.

Three things that aren't commands, and matter more than the commands:

1. **Ceph's own wire encryption changes OSD latency, which is the exact
   signal rattle's Ceph detectors fire on.** A chaos run that behaves
   differently after flipping msgr2 to secure mode might be measuring
   encryption overhead, not the fault you injected. Re-baseline in writing
   if the thresholds move.
2. **A latency baseline captured against an idle rig measures nothing.** All
   three traffic generators sat idle through Q-live sessions 2–4 until
   someone ran `just generate-traffic 20`. Capture `ceph osd perf`
   before/after under load, with the generator state named in the record.
3. **Reasoning about this rig from `git status` will be wrong about a third
   of it.** Rig changes reach the cluster through three different mechanisms
   with three different definitions of "on disk": `applications/**` is
   ArgoCD-managed and syncs from the **GitHub remote** — an uncommitted edit
   is invisible to it, and the Application reports `Synced` regardless.
   `*.tf` is OpenTofu-managed and applies the **local working tree** — no
   commit needed for `tofu apply` to take. `provisioning/scripts/*.sh` run
   from the **local working tree at `just up` time** — an edit made after
   cluster creation doesn't apply until the next rebuild. Know which
   mechanism owns the file you're editing before trusting what its diff
   implies about the live cluster.

## Sizing a fault: clear the detection lag, not just the fault's own math

rattle's burn-rate detectors score a **trailing window**, so a detection can land
5–6 minutes after a fault starts. A fault that self-heals inside that lag is
invisible: the pipeline correctly returns `no_action` because live metrics are
already healthy by the time anything reasons about them. That looks identical to a
broken pipeline and it is not one.

Budget a fault as **detection lag + enough remaining degradation for the action to
have something to fix**, and size it against the specific action you're trying to
exercise, not against the fault mechanism alone. Cost of getting this wrong,
measured 2026-07-26: three chaos runs to exercise one action, two of them
correct-but-useless.

| Scenario | Fault | Mark-out | Exercises |
|---|---|---|---|
| `osd-pod-failure.yaml` | 240s | 60s | the two-arm causality proof by hand — too short for anything autonomous |
| `osd-pod-failure-autonomous.yaml` | 480s | 300s | `hold-rebalance` — noout lands inside the [down, not-out] window |
| `osd-pod-failure-accelerate.yaml` | 600s | 60s | `accelerate-recovery` — marked out fast, still degraded when detection lands |

Second trap, same afternoon: an action that reports `applied` has not necessarily
changed anything. thump reads nothing back after a mutation, so **check the value
by hand mid-flight** (`ceph config get osd <knob>`) before believing a `success`
settle — and be suspicious of a win that arrives right as the fault's own duration
expires.

## Arming live: keep `beats.clank.dedupeWindow` short

When a `⚠️ TEMPORARY` arm-live block sets `thump.executor: live` +
`killSwitch.armed: true` in a `deploy/tilt-values-*.yaml`, also set
`beats.clank.dedupeWindow` short (10m, not the 1h production default) — a long
window is a footgun specifically *because* most chaos scenarios run on a ~10-minute
convergence watch, so a stuck/held fingerprint under the production default would
block re-detection for an hour, eating the whole session.

Dedup only ever suppresses a fingerprint that is genuinely still **open**
(`internal/clank/ledger.go`'s `Open()` filters on `isOpen(Status.Phase)` — a
*closed* set, settled to `success`/`partial_non_converging`, is never re-blocked
regardless of `dedupeWindow`'s length or how recently it closed). So a normal
`applied → settled` cycle is unaffected by this value either way — it only matters
for a set that **never closes**: a `hold` (no ack mechanism exists in v1, so a held
set stays "open" forever) or a crash mid-watch leaving a set stuck `acted`.
`dedupeWindow` is what eventually lets a fresh detection re-surface past a stuck
fingerprint — keep it short (10m) so a bad run doesn't cost the rest of the session.

Remember to trigger **both** the `thump` and `clank` Tilt resources after editing —
`tilt trigger thump` alone won't roll clank's Deployment, so a `dedupeWindow` edit
silently won't take effect until clank is triggered too (`tilt trigger clank`).
Verify by reading the live Deployment env, not by trusting the file: `kubectl get
deployment clank -n thump -o jsonpath='{.spec.template.spec.containers[0].env}'`.

## Re-firing a fault: two independent cooldowns, and one active window

Re-firing the same fault to get a second draw (e.g. retrying for a different verdict)
has to clear **three** things, not one — missing any of them wastes the retry:

1. **rattle's own per-fingerprint `Debouncer` (`internal/rattle/rattle.go:232`,
   hardcoded 10m)** — separate from and in addition to clank's configurable
   `DEDUPE_WINDOW`. It holds regardless of whether the underlying fault is still live:
   rattle's `Reconcile` re-evaluates every tick (1m) and would re-fire on a
   still-burning SLO immediately if not for this, so a re-injected fault does not
   produce a fresh `"detection"` line until 10 minutes after the *previous* one, full
   stop.
2. **clank's ledger dedup** (`beats.clank.dedupeWindow`, see below) — gates whether
   clank will *reason* about a detection that does land. Only blocks a still-**open**
   fingerprint (`isOpen(Status.Phase)`), so it clears the moment the prior cycle
   settles to a terminal outcome, independent of the raw window length.
3. **The prior cycle's own `successCriteria.window`** (per action, in
   `config/actions/catalog.yaml`) **is a live measurement in progress, not a cooldown
   — do not touch the fault target while it's open.** Re-injecting the same fault
   mid-window doesn't start a clean second trial; it corrupts the first one. Found
   live, 2026-08-09: re-firing `flagd-cart-failure.sh inject` 46 seconds into
   `disable-cart-failure`'s 5-minute convergence watch flipped `cartFailure` back to
   `"on"` right as thump was checking whether it had cleared — the watch correctly
   reported `partial_non_converging` and triggered an unplanned auto-reversal, but
   that result was an artifact of the interference, not a real read on the action.
   Check the catalog's `window` for the contract in play and wait it out (or confirm
   `"settled"` in the beat's log) before touching the target again.

In practice: after a cycle completes, the earliest a *clean* re-fire produces a fresh,
undeduped decision is `max(10m from the last detection, settle window elapsed)` — often
the rattle debounce, not clank's dedup, is the binding constraint.

## Before picking a fault to reach a specific verdict, check the class can reach it

Not every failure class in `config/actions/catalog.yaml` can produce every verdict —
`config/hiss/policy.yaml`'s per-class floor and the class's catalogued remedies (or
lack of one) can rule a verdict out structurally before any live run tells you so.
Found live, 2026-08-09, chasing an Escalate verdict specifically:

- A class whose floor is seeded at or below the zero-corroboration grounding tier
  (e.g. `redundancy_degraded: 0.3`, matching `GroundingZero`) will essentially never
  fail the confidence-floor check — it was seeded there on purpose to unblock
  auto-approval on that class, so it can't be used to reach Escalate.
- A class with no *active* catalogued remedy (a "demoted dead knob," documented in the
  action's own header comment) doesn't produce Escalate either — clank declines the
  detection before a candidate ever forms, so it never reaches hiss at all. A different
  verdict entirely, easy to mistake for "the floor wasn't cleared."
- Read `policy.yaml`'s floor comments and the catalog's per-action header comments for
  both signals before spending a live cycle on a fault chosen by domain alone (e.g.
  "try the Ceph one instead") — grep `applicableFailureClasses` across
  `config/actions/catalog.yaml` to see which classes have a live remedy at all, and
  cross-check each against its floor before assuming any fault targeting that domain is
  a viable path to the verdict you want.

## What a chaos run produces, and where to look

The chain to watch is rattle detects → clank reasons → hiss governs → thump renders
(dry-run). Each beat's pods log through `slog`; `kubectl logs -n thump -l
app.kubernetes.io/component=<beat> -f` follows one. The audit trail for a clank run is
its transcript, checkpointed per turn to S3/GCS at
`transcripts/<fingerprint>/<RunID>/*.json` — find `RunID` by matching its nanosecond
timestamp to the `"reasoned"` slog line. When a decision looks wrong, read the
transcript; don't reconstruct it from memory.

**Tell the user, up front, what you're waiting for and how long it should take.** Before
starting any wait (the `until`-loop and `Monitor` patterns below), state: what signal
ends the wait (the specific log line, decision, or `Ready` condition), and a wall-clock
estimate for when it should land (e.g. "burn-rate detector needs a ~5m window, so first
detection around 12:35 PT; convergence watch after that runs ~10m more"). While waiting,
surface elapsed/remaining time at natural check-in points, not just at the end — a
run that's gone quiet for ten minutes with no time context reads as stalled even when
it's on track. If a run is taking meaningfully longer than the stated estimate, say so
rather than letting the estimate go stale.

**Before building any log filter, grep the actual `slog` call sites for the exact
message string** (`grep -rn 'slog\.\(Info\|Error\|Warn\)(' internal/<beat>/*.go`) —
don't guess a plausible-sounding one. rattle logs `"detection"`, not `"detected"`;
a filter built on the wrong guess matches nothing and silently never fires, which
reads identically to "nothing has happened yet."

## This rig runs on real infra — treat runtime as spend, not attention

A kind cluster, NATS, three beats, and a 20-minute settle window all cost real
wall-clock time on real machines whether or not anyone's watching — event-driven
waiting isn't just about not annoying the user with "still waiting" messages, it's about
not leaving a live rig spinning through dead time nobody's using. Apply this beyond
just the wait mechanism:

- **Before firing a scenario with a long settle window (10m+), verify everything
  checkable offline or with a cheap live read first** — preconditions actually
  readable via `kubectl`/`ceph` by hand, the target Application/ConfigMap/secret
  actually exists, `task ci` green on the relevant package. A row that 127's three
  seconds in because of a typo cost nothing; a row that runs the full 20 minutes
  before failing for a reason a 5-second check would've caught is the expensive
  version of the same bug — the osd-down-accelerate session paid for this twice.
- **A failed or interrupted run is a stopping point to reassess, not a reflex retry.**
  Before re-firing the same expensive scenario, name out loud why this attempt should
  behave differently — if the answer is "not sure, let's see," that's the signal to
  ask before spending another full settle window.
- **Don't let the rig sit provisioned longer than the work in front of it.** If a
  session is pausing for a stretch (compaction, a long side investigation, handing
  off), that's worth naming as a point to consider scaling down or tearing down
  rather than leaving a live cluster idle by default.

## Waiting on results — no blind fixed-length polling

A chaos scenario's effects don't land on a fixed schedule — a burn-rate detector,
a governance verdict, and a pod actually going `Ready` all take different, unpredictable
amounts of time. Setting a rote wakeup (e.g. always ~590s) either fires too early and
wastes a round trip, or too late and stalls the session. Match the wait mechanism to
what's actually being waited on:

- **"Tell me once X happens"** (a specific log line appears, a pod goes Ready, a
  decision lands) — `Bash` with `run_in_background: true` running an `until`-loop that
  exits the moment the condition is true, e.g.:

  ```bash
  until kubectl logs -n thump -l app.kubernetes.io/component=hiss --since-time=2026-08-09T13:22:37Z | grep -q '"decision"'; do sleep 5; done
  ```

  One notification, arrives exactly when the condition fires — no polling interval to
  guess.
- **Anchor with `--since-time=<fixed RFC3339 timestamp>`, not `--since=Xm`, the moment
  you can — never a relative sliding window if the same scenario might fire twice in
  one session.** `--since=5m` is evaluated fresh at *each* poll, so its window slides
  forward with wall-clock time; a wait launched after a re-fire can still see the
  *previous* fire's already-logged line inside that trailing window and report a false
  positive instantly. Found live, 2026-08-09: a second wait for `slo_burn:cart`'s
  detection matched the first run's detection line from ~90 seconds earlier and
  reported "done" with no actual second detection having happened yet. Stamp the
  fault's own injection time and use that as `--since-time` for every wait tied to it.
- **Never nest backgrounding.** Don't put `&`/`nohup ... &` *inside* a script also
  passed to `Bash` with `run_in_background: true` — the outer call launches the
  detached inner process and then exits immediately (nothing left to wait on at the
  tool-tracking level), so the tool reports "completed" right away while the real
  `until`-loop is still running unobserved, or gets killed with the parent's process
  group. Found live, 2026-08-09: this produced an instant false-positive "completed"
  notification with an empty result, mistaken for a wait that was too fast. Pass the
  blocking `until`-loop itself as the top-level command with `run_in_background: true`
  — that's the whole mechanism, no inner `&` needed.
- **"Tell me every time X happens"** across a whole run (every detection, every
  proposal, every decision) — `Monitor` on a `tail -f`/`kubectl logs -f` piped through
  a line-buffered `grep` that covers success *and* failure signatures
  (`gatePassed|escalated|rejected|panic|Traceback`) — a filter that only matches the
  happy path goes silent on a crash, and silence reads identically to "still running."
- **Cross-process waits inside `/loop` dynamic mode** — only then reach for
  `ScheduleWakeup`, and pick `delaySeconds` from how fast the specific thing being
  watched actually changes (a burn-rate window, a reconcile interval), not a default.
  If a `Monitor` or backgrounded `Bash` is already tracking the work, `ScheduleWakeup`
  is just the long fallback heartbeat (1200s+) — it is never the primary signal.
  **Outside `/loop`, don't call it at all** — a plain live-testing session (not
  invoked via `/loop`) has no dynamic-loop context for it to resume, and it's not a
  generic "wait N seconds" tool. A `Monitor` or a backgrounded `Bash` `until`-loop is
  the whole mechanism there; waiting on their notification is free — it costs nothing
  to just sit idle until one fires.
- **Clean up what you started.** Chaos runs leave `kubectl port-forward`, `kubectl logs
  -f`, and `tilt` processes behind — `pkill -f "port-forward svc/<name>"` or `kill %N`
  before ending the session, and always `chaos/<script>.sh restore` to undo a fault
  before moving on, even mid-investigation.
