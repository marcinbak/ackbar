package daemon

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// StandupReport holds a structured standup summary across projects
type StandupReport struct {
	Group           string                  `json:"group"`
	Date            string                  `json:"date"`
	Days            int                     `json:"days"`
	Projects        []StandupProjectSection `json:"projects"`
	TotalShipped    int                     `json:"total_shipped"`
	TotalInProgress int                     `json:"total_in_progress"`
	TotalBlocked    int                     `json:"total_blocked"`
	TotalDiscovered int                     `json:"total_discovered"`
	Markdown        string                  `json:"markdown"`
	SpokenBriefing  string                  `json:"spoken_briefing"`
}

// StandupProjectSection represents standup items under a single project
type StandupProjectSection struct {
	ProjectName string        `json:"project_name"`
	Shipped     []StandupItem `json:"shipped"`
	InProgress  []StandupItem `json:"in_progress"`
	Blocked     []StandupItem `json:"blocked"`
	Discovered  []StandupItem `json:"discovered"`
}

// StandupItem represents an individual line item in the standup report
type StandupItem struct {
	TaskID         string   `json:"task_id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	Substatus      string   `json:"substatus"`
	Branch         string   `json:"branch,omitempty"`
	PRURL          string   `json:"pr_url,omitempty"`
	PRNumber       int      `json:"pr_number,omitempty"`
	Blocker        string   `json:"blocker,omitempty"`
	Workers        []string `json:"workers,omitempty"`
	Deliverables   []string `json:"deliverables,omitempty"`
	ExternalRefKey string   `json:"external_ref_key,omitempty"`
}

// BuildStandupReport gathers tasks from SQLite and generates structured standup data,
// copy-pasteable Markdown, and a spoken voice briefing
func (s *Server) BuildStandupReport(group string, days int) (*StandupReport, error) {
	if days <= 0 {
		days = 1
	}
	if days > 60 {
		days = 60
	}

	tasks, err := s.db.GetTasks()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tasks for standup: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	now := time.Now()

	// Filter tasks by group if specified
	normGroup := strings.TrimSpace(group)
	if len(normGroup) > 64 {
		normGroup = normGroup[:64]
	}
	normGroup = strings.ReplaceAll(normGroup, "\n", "")
	normGroup = strings.ReplaceAll(normGroup, "\r", "")
	if strings.EqualFold(normGroup, "all") {
		normGroup = ""
	}

	var filtered []Task
	for _, t := range tasks {
		if normGroup != "" && !strings.EqualFold(t.GroupName, normGroup) {
			continue
		}
		filtered = append(filtered, t)
	}

	// Group tasks by project
	projectMap := make(map[string]*StandupProjectSection)

	for _, t := range filtered {
		projName := t.ProjectName
		if projName == "" {
			projName = "General"
		}

		sec, exists := projectMap[projName]
		if !exists {
			sec = &StandupProjectSection{
				ProjectName: projName,
			}
			projectMap[projName] = sec
		}

		// Gather worker session identifiers
		var workerLabels []string
		for _, w := range t.Workers {
			label := w.Agent
			if w.Host != "" {
				label += fmt.Sprintf(" (%s)", w.Host)
			}
			workerLabels = append(workerLabels, label)
		}

		// Gather deliverable titles
		var delTitles []string
		for _, d := range t.Deliverables {
			delTitles = append(delTitles, fmt.Sprintf("%s [%s]", d.Title, d.Kind))
		}

		// Extract external ref key if present
		refKey := ""
		if len(t.ExternalRefs) > 0 {
			refKey = t.ExternalRefs[0].RefKey
		}

		item := StandupItem{
			TaskID:         t.ID,
			Title:          t.Title,
			Status:         t.Status,
			Substatus:      t.Substatus,
			Branch:         t.Branch,
			PRURL:          t.PRURL,
			PRNumber:       t.PRNumber,
			Workers:        workerLabels,
			Deliverables:   delTitles,
			ExternalRefKey: refKey,
		}

		// Check if blocked: either task substatus or blocker question (only for non-DONE tasks)
		isBlocked := t.Status != "DONE" && (t.Substatus == "blocked" || t.BlockerQuestion != "")
		if isBlocked {
			item.Blocker = sanitizeMarkdownLine(t.BlockerQuestion)
			if item.Blocker == "" {
				item.Blocker = "Agent blocked waiting for user input or approval"
			}
			sec.Blocked = append(sec.Blocked, item)
			continue
		}

		// Classify into Shipped, InProgress, Discovered
		switch t.Status {
		case "DONE":
			// Included in shipped if completed within lookback window
			isRecent := false
			if t.CompletedAt != nil && t.CompletedAt.After(cutoff) {
				isRecent = true
			} else if t.CompletedAt == nil && t.UpdatedAt.After(cutoff) {
				isRecent = true
			}
			if isRecent {
				sec.Shipped = append(sec.Shipped, item)
			}
		case "IN_PROGRESS", "REVIEW":
			sec.InProgress = append(sec.InProgress, item)
		case "NEW":
			// Only include recently discovered proposals or new backlog items
			if t.CreatedAt.After(cutoff) || t.Substatus == "discovered" {
				sec.Discovered = append(sec.Discovered, item)
			}
		}
	}

	// Sort projects alphabetically
	var projectNames []string
	for p := range projectMap {
		projectNames = append(projectNames, p)
	}
	sort.Strings(projectNames)

	var orderedProjects []StandupProjectSection
	totalShipped := 0
	totalInProgress := 0
	totalBlocked := 0
	totalDiscovered := 0

	for _, p := range projectNames {
		sec := *projectMap[p]
		if len(sec.Shipped) == 0 && len(sec.InProgress) == 0 && len(sec.Blocked) == 0 && len(sec.Discovered) == 0 {
			continue
		}
		totalShipped += len(sec.Shipped)
		totalInProgress += len(sec.InProgress)
		totalBlocked += len(sec.Blocked)
		totalDiscovered += len(sec.Discovered)
		orderedProjects = append(orderedProjects, sec)
	}

	reportGroup := normGroup
	if reportGroup == "" {
		reportGroup = "Fleet Overview"
	}

	dateStr := now.Format("Jan 02, 2006")
	markdown := formatStandupMarkdown(reportGroup, dateStr, days, orderedProjects, totalShipped, totalInProgress, totalBlocked, totalDiscovered)
	spoken := formatStandupSpokenBriefing(reportGroup, dateStr, days, orderedProjects, totalShipped, totalInProgress, totalBlocked, totalDiscovered)

	return &StandupReport{
		Group:           reportGroup,
		Date:            dateStr,
		Days:            days,
		Projects:        orderedProjects,
		TotalShipped:    totalShipped,
		TotalInProgress: totalInProgress,
		TotalBlocked:    totalBlocked,
		TotalDiscovered: totalDiscovered,
		Markdown:        markdown,
		SpokenBriefing:  spoken,
	}, nil
}

func sanitizeMarkdownLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return strings.TrimSpace(s)
}

func formatStandupItemTitle(item StandupItem) string {
	title := sanitizeMarkdownLine(item.Title)
	if item.ExternalRefKey != "" {
		return fmt.Sprintf("[%s] %s", item.ExternalRefKey, title)
	}
	return title
}

func formatStandupMarkdown(group, dateStr string, days int, projects []StandupProjectSection, shipped, inProgress, blocked, discovered int) string {
	var sb strings.Builder

	titlePrefix := "Daily Standup"
	if days > 1 {
		titlePrefix = fmt.Sprintf("Work Retrospective (%d Days)", days)
	}

	cleanGroup := sanitizeMarkdownLine(group)
	sb.WriteString(fmt.Sprintf("## 📅 %s — %s (%s)\n\n", titlePrefix, cleanGroup, dateStr))

	if len(projects) == 0 {
		sb.WriteString("_No active, shipped, or blocked tasks found in this timeframe._\n\n")
		sb.WriteString("---\n")
		sb.WriteString("**Summary:** 0 shipped, 0 in progress, 0 blocked.\n")
		return sb.String()
	}

	for _, p := range projects {
		cleanProj := sanitizeMarkdownLine(p.ProjectName)
		sb.WriteString(fmt.Sprintf("### 📁 %s\n", cleanProj))

		// 1. Shipped
		for _, item := range p.Shipped {
			line := fmt.Sprintf("- [🚀 SHIPPED] %s", formatStandupItemTitle(item))
			if item.PRNumber > 0 {
				line += fmt.Sprintf(" (PR #%d)", item.PRNumber)
			}
			if len(item.Deliverables) > 0 {
				line += fmt.Sprintf(" — Deliverables: %s", strings.Join(item.Deliverables, ", "))
			}
			sb.WriteString(line + "\n")
		}

		// 2. Blocked
		for _, item := range p.Blocked {
			line := fmt.Sprintf("- [⚠️ BLOCKED] %s", formatStandupItemTitle(item))
			if item.Blocker != "" {
				line += fmt.Sprintf(" — Blocker: %s", sanitizeMarkdownLine(item.Blocker))
			}
			if len(item.Workers) > 0 {
				line += fmt.Sprintf(" (%s)", strings.Join(item.Workers, ", "))
			}
			sb.WriteString(line + "\n")
		}

		// 3. In Progress
		for _, item := range p.InProgress {
			statusTag := "[⚙️ IN PROGRESS]"
			if item.Status == "REVIEW" {
				statusTag = "[🔍 IN REVIEW]"
			}
			line := fmt.Sprintf("- %s %s", statusTag, formatStandupItemTitle(item))
			var meta []string
			if item.PRNumber > 0 {
				meta = append(meta, fmt.Sprintf("PR #%d", item.PRNumber))
			} else if item.Branch != "" {
				meta = append(meta, item.Branch)
			}
			if len(item.Workers) > 0 {
				meta = append(meta, strings.Join(item.Workers, ", "))
			}
			if len(meta) > 0 {
				line += fmt.Sprintf(" (%s)", strings.Join(meta, " • "))
			}
			sb.WriteString(line + "\n")
		}

		// 4. Discovered
		for _, item := range p.Discovered {
			line := fmt.Sprintf("- [💡 DISCOVERED] %s", formatStandupItemTitle(item))
			sb.WriteString(line + "\n")
		}

		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("**Summary:** %d shipped, %d in progress, %d blocked, %d discovered.\n", shipped, inProgress, blocked, discovered))

	return sb.String()
}

func formatStandupSpokenBriefing(group, dateStr string, days int, projects []StandupProjectSection, shipped, inProgress, blocked, discovered int) string {
	var sb strings.Builder

	if days > 1 {
		sb.WriteString(fmt.Sprintf("Here is your %d day %s work retrospective for %s. ", days, group, dateStr))
	} else {
		sb.WriteString(fmt.Sprintf("Good day. Here is your %s daily standup for %s. ", group, dateStr))
	}

	sb.WriteString(fmt.Sprintf("You have %d tasks shipped, %d in progress, and %d items blocked. ", shipped, inProgress, blocked))

	// Priority 1: Call out blockers first
	if blocked > 0 {
		sb.WriteString("Attention is required on blocked tasks. ")
		var blockerSnippets []string
		for _, p := range projects {
			for _, b := range p.Blocked {
				snippet := fmt.Sprintf("In %s, %s is blocked", p.ProjectName, b.Title)
				if b.Blocker != "" && len(b.Blocker) < 120 {
					snippet += fmt.Sprintf(": %s", b.Blocker)
				}
				blockerSnippets = append(blockerSnippets, snippet)
				if len(blockerSnippets) >= 2 {
					break
				}
			}
			if len(blockerSnippets) >= 2 {
				break
			}
		}
		sb.WriteString(strings.Join(blockerSnippets, ". ") + ". ")
	}

	// Priority 2: Highlights of shipped work
	if shipped > 0 {
		var shippedSnippets []string
		for _, p := range projects {
			for _, s := range p.Shipped {
				shippedSnippets = append(shippedSnippets, fmt.Sprintf("%s in %s", s.Title, p.ProjectName))
				if len(shippedSnippets) >= 2 {
					break
				}
			}
			if len(shippedSnippets) >= 2 {
				break
			}
		}
		sb.WriteString("Recently shipped: " + strings.Join(shippedSnippets, ", ") + ". ")
	}

	// Priority 3: Active in-progress highlights
	if inProgress > 0 {
		var inProgSnippets []string
		for _, p := range projects {
			for _, ip := range p.InProgress {
				inProgSnippets = append(inProgSnippets, fmt.Sprintf("%s in %s", ip.Title, p.ProjectName))
				if len(inProgSnippets) >= 2 {
					break
				}
			}
			if len(inProgSnippets) >= 2 {
				break
			}
		}
		sb.WriteString("Currently in progress: " + strings.Join(inProgSnippets, ", ") + ". ")
	}

	if blocked == 0 && inProgress == 0 && shipped == 0 {
		sb.WriteString("All queues are clear and no active agent sessions are currently running.")
	} else if blocked == 0 {
		sb.WriteString("All other agent sessions are running smoothly without blockers.")
	}

	return strings.TrimSpace(sb.String())
}
