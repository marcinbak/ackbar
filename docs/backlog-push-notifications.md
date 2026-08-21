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

## 3. Implementation Plan (When Ready to Build)

### Phase 1: Daemon Dispatcher (`ackbard`)
* [ ] Create `internal/daemon/notification.go`:
  * Add notification configuration struct to SQLite (`ntfy_topic`, `webhook_url`, `enabled_events`).
  * Listen to session state transitions in `UpdateSessionState` / `handleHook`.
  * Trigger push dispatch when `session.State == StateBlocked` or `session.State == StateFailed`.
* [ ] Add CLI flag `-ntfy-topic <topic>` and config support in `~/.config/ackbar/config.json`.

### Phase 2: Mobile Deep-Linking (`mobile/`)
* [ ] Configure custom URL scheme `ackbar://` in `mobile/ios/Runner/Info.plist` and `mobile/android/app/src/main/AndroidManifest.xml`.
* [ ] Handle deep links (`ackbar://attention?session_id=<uuid>`):
  * Parse incoming route.
  * Switch navigation to the **Attention** tab.
  * Automatically open the targeted session card in **Fullscreen Attention Mode**.

### Phase 3: Lock-Screen Action Endpoints
* [ ] Expose an unauthenticated or token-authorized webhook trigger in `ackbard` for lock-screen callbacks:
  * `POST /v1/notifications/action?id=<session_id>&action=allow`
  * `POST /v1/notifications/action?id=<session_id>&action=deny`

---

## 4. Open Decisions
* Whether to provide a built-in UI toggle in the mobile **Hosts** or **Settings** screen to configure the `ntfy` topic directly from the app.
* Priority filtering (e.g. notify only on permission requests, but ignore low-priority questions).
