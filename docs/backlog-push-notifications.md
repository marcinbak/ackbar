# Backlog: Real-Time Blocker & Permission Push Notifications

> **Tracking Doc:** Feature evaluation, architecture options, and implementation plan for instant mobile alerts when agent sessions require human feedback.

---

## 1. Context & Motivation

When running autonomous AI coding agents (Claude Code, Antigravity, OpenAI Codex), agents often pause mid-execution waiting on developer approval for:
* **Tool Permissions:** Executing shell commands (`bash`), filesystem modifications (`rm`, `write`), or git checkouts.
* **Clarifying Questions:** Multiple-choice questions or missing requirement clarifications.
* **Plan Reviews:** Design document approvals (`implementation_plan.md`).
* **Fatal Errors:** Crashes, syntax failures, or token exhaustion.

Without push notifications, developers must manually check the dashboard or risk leaving sessions idling for hours unnoticed.

---

## 2. Architecture & Delivery Options Evaluated

### Option A: Free `ntfy.sh` Open-Source Relay + Ackbar Deep-Linking ⭐ *(Recommended for Free Personal Setup)*
* **Cost:** **$0 / year** (Does not require a paid $99/year Apple Developer account).
* **Delivery Mechanism:** Uses the free, open-source **ntfy** iOS and Android apps (which already hold native Apple APNs push entitlements).
* **Lock-Screen Experience:**
  * **Alert Title:** `❓ Claude Code @devbox Needs Attention`
  * **Alert Body:** `Review PR and run database migration?`
  * **Interactive Action Buttons on Lock Screen:**
    * `[⚡ Allow (y)]` ➔ Dispatches instant unblock POST to `ackbard` via Tailscale/Webhook without unlocking the phone.
    * `[🛑 Deny (n)]` ➔ Sends rejection signal.
    * `[📱 Open Card]` ➔ Deep-links (`ackbar://attention?session_id=...`) directly into Ackbar Mobile in **Fullscreen Attention Mode**.

```
┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
│  Agent Blocks   │ ────► │  ackbard Daemon │ ────► │  ntfy.sh Server │ ────► │   iPhone Lock   │
│ (Claude/Codex)  │       │ (Webhook POST)  │       │ (APNs Provider) │       │     Screen      │
└─────────────────┘       └─────────────────┘       └─────────────────┘       └─────────────────┘
                                                                                       │
                                    ┌──────────────────────────────────────────────────┘
                                    ▼
                ┌───────────────────────────────────────────────┐
                │ ⚡ [Allow]   🛑 [Deny]   📱 [Open in Ackbar] │
                └───────────────────────────────────────────────┘
```

---

### Option B: Native APNs & Firebase Cloud Messaging (FCM)
* **Cost:** **$99 / year** Apple Developer Program fee required.
* **Delivery Mechanism:** Direct HTTP/2 push to Apple APNs (`api.push.apple.com`) using `.p8` AuthKey or Firebase Cloud Messaging.
* **Pros:** Self-contained entirely within the `Ackbar` mobile app binary without installing a third-party companion app.
* **Cons:** Cannot be used with free Apple Personal Teams; FCM on iOS still relies on APNs for background delivery.

---

### Option C: Telegram Bot or Discord Webhook
* **Cost:** **$0 / year**.
* **Delivery Mechanism:** `ackbard` sends a message to a private Telegram bot or Discord channel.
* **Interactive Buttons:** Telegram supports inline keyboard buttons (`[✅ Allow]`, `[❌ Deny]`) that make webhook calls back to `ackbard`.

---

### Option D: Background Tailscale Sync + Local Notifications
* **Cost:** **$0 / year**.
* **Delivery Mechanism:** `flutter_local_notifications` inside Ackbar Mobile triggers banners whenever a background sync detects new blocked sessions.
* **Limitation:** iOS aggressively throttles background execution after the screen locks for a few minutes, making delivery delayed or unreliable when the phone is asleep.

---

## 3. Implementation Status

### Phase 1: Daemon Dispatcher & Web UI Fleet Sync (`ackbard` & `web/`) ✅
* [x] Created `internal/daemon/ntfy.go`:
  * Added `ntfy_topic` and `ntfy_server` (default `https://ntfy.sh`) to SQLite `settings` (`/v1/settings`), env vars (`ACKBAR_NTFY_TOPIC`, `ACKBAR_NTFY_SERVER`), and CLI flags (`-ntfy-topic`, `-ntfy-server`).
  * Triggers non-blocking push notifications when tasks transition into `REVIEW` (with `[Open PR #<num>]` view action and `ackbar://session?id=...` deep-links) or into `blocked` (`ackbar://session?id=...`).
  * Triggers push notifications when sessions (or subagents) enter `StateBlocked` (`Permission Needed` vs `Question`).
  * Uses the Ackbar app icon (`AckbarNotificationIconURL`), sets the notification title to the **Session Name**, and prefixes the message body with the **Project Group Path** (`📂 <Group Path>`).
  * Added `POST /v1/notifications/test` endpoint and Web UI **⚙️ Settings → 🔔 Push Notifications (`ntfy.sh`)** section with 1-click test notification and optional propagation of `ntfy_topic` / `ntfy_server` to all connected fleet hosts.

### Phase 2: Mobile Deep-Linking (`mobile/`) ✅
* [x] Configured custom URL scheme `ackbar://` in `mobile/ios/Runner/Info.plist` and `mobile/android/app/src/main/AndroidManifest.xml`.
* [x] Handled cold-start (`onGenerateRoute`) and warm-start (`WidgetsBindingObserver.didPushRouteInformation`) deep links in `MainNavigationShell`:
  * `ackbar://session?id=<id>` ➔ Switches to **Fleet** tab and opens `SessionDetailSheet`.
  * `ackbar://attention?session_id=<id>` ➔ Switches to **Attention** tab and opens `SessionDetailSheet`.
  * `ackbar://task?id=<id>` ➔ Switches to **Plans** tab and opens `PlanDiffViewerModal`.

### Phase 3: Lock-Screen Action Endpoints (Future)
* [ ] Expose a token-authorized webhook trigger in `ackbard` for lock-screen allow/deny callbacks:
  * `POST /v1/notifications/action?id=<session_id>&action=allow`
  * `POST /v1/notifications/action?id=<session_id>&action=deny`

---

## 4. Open Decisions
* Whether to provide a built-in UI toggle in the mobile **Hosts** or **Settings** screen to configure the `ntfy` topic directly from the app.
* Priority filtering (e.g. notify only on permission requests, but ignore low-priority questions).
