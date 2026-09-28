# 02 — Scoping, Hierarchy & Privacy Boundaries

## 1. Hierarchical Tree Structure

Rather than using a rigid binary "Work vs. Personal" flag, the system mirrors the natural tree hierarchy already persisted in Ackbar (`tree_nodes`):

```
┌────────────────────────────────────────────────────────────────────────┐
│                        HIERARCHICAL TREE LEVELS                        │
│                                                                        │
│  LEVEL 1: ORGANIZATION / TOP-LEVEL GROUP                               │
│  ├── 🏢 Modemobile                                                     │
│  └── 🏠 Personal                                                       │
│                                                                        │
│  LEVEL 2: PROJECT (Repository / Subsystem)                             │
│  ├── Modemobile ➔ 📱 NGL, 📡 MEA, 🔒 AppLock, 📊 release-monitor        │
│  └── Personal   ➔ 🐙 Ackbar, 🎯 Skip2Q, 📈 MicroGains                 │
│                                                                        │
│  LEVEL 3: SUBPROJECT / COMPONENT (Optional)                            │
│  ├── NGL ➔ ngl-ios, ngl-android, image-generation, Vitals              │
│  └── MEA ➔ ad-mediation-api, news-api, current-android                 │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Privacy Boundaries & Standup Isolation

A critical requirement is that personal projects and experimental weekend hacks must never appear in company daily standups or weekly updates.

### Isolation Rules:
1. **Top-Level Group Filtering:**
   * Standup reports and weekly summaries are strictly scoped to a selected top-level group (e.g. `Modemobile`).
   * When `Modemobile` is active, queries execute:
     ```sql
     SELECT * FROM tasks WHERE group_name = 'Modemobile';
     ```
   * Personal tasks (`group_name = 'Personal'`) are filtered out before reaching any LLM synthesis or copy-paste text buffer.
2. **Tracker Whitelisting:**
   * Any task linked to a corporate tracker (e.g. Jira prefix `NGL-`, `MEA-`, `APPLOCK-`) is automatically bound to the corporate organization.
3. **Directory Path Ingestion Rules:**
   * The daemon automatically classifies tasks based on filesystem paths:
     * `/Users/dev4u/Work/Modemobile/**` ➔ `group: Modemobile`
     * `/Users/dev4u/Personal/**` or `~/Personal/**` ➔ `group: Personal`

---

## 3. UI Scoping & Multi-Team Switching

Inside the Ackbar dashboard:

1. **Top-Level Group Selector:**
   * Prominent pill buttons: `[🏢 Modemobile (4)]`, `[🏠 Personal (2)]`, `[🌐 All (6)]`.
   * Switching groups re-filters the entire board, counters, and metrics instantly.
2. **Dynamic Project Dropdown:**
   * Selecting `Modemobile` dynamically populates the project dropdown with only `NGL`, `MEA`, `AppLock`, and `release-monitor`.
   * Selecting `Personal` populates only `Ackbar`, `Skip2Q`, and `MicroGains`.
3. **Project Swimlanes & Sub-views:**
   * Developers can focus on a single project (e.g. `NGL`) to see all active agents across `ngl-ios`, `ngl-android`, and `image-generation`.
