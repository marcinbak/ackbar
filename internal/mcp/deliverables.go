package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func executeListRetros(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	v := url.Values{}
	if since := getString(args, "since"); since != "" {
		v.Set("since", since)
	}
	if until := getString(args, "until"); until != "" {
		v.Set("until", until)
	}
	if nodePath := getString(args, "node_path"); nodePath != "" {
		v.Set("node_path", nodePath)
	}
	if repo := getString(args, "repo"); repo != "" {
		v.Set("repo", repo)
	}
	if host := getString(args, "host"); host != "" {
		v.Set("host", host)
	}
	if agent := getString(args, "agent"); agent != "" {
		v.Set("agent", agent)
	}
	if taskID := getString(args, "task_id"); taskID != "" {
		v.Set("task_id", taskID)
	}
	if limit := getInt(args, "limit", 0); limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	endpoint := "/v1/retros"
	if len(v) > 0 {
		endpoint += "?" + v.Encode()
	}

	var data map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &data); err != nil {
		return formatError(fmt.Sprintf("Failed to list retros: %v", err)), nil
	}

	retrosRaw, _ := data["retros"].([]any)
	count := len(retrosRaw)

	if count == 0 {
		return formatSuccess("No retrospectives found matching filter criteria."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d retrospective(s):\n\n", count))
	for i, rRaw := range retrosRaw {
		r, ok := rRaw.(map[string]any)
		if !ok {
			continue
		}
		id := getString(r, "id")
		taskID := getString(r, "task_id")
		title := getString(r, "title")
		host := getString(r, "host")
		filePath := getString(r, "file_path")
		hasContent, _ := r["has_content_md"].(bool)
		contentChars := getInt(r, "content_chars", 0)

		sb.WriteString(fmt.Sprintf("%d. **%s** (`%s`)\n", i+1, title, id))
		sb.WriteString(fmt.Sprintf("   - Task: `%s` | Host: `%s`\n", taskID, host))
		if filePath != "" {
			sb.WriteString(fmt.Sprintf("   - Path: `%s`\n", filePath))
		}
		if m, ok := r["metrics"].(map[string]any); ok && m != nil {
			repo := getString(m, "repo")
			branch := getString(m, "branch")
			tier := getString(m, "tier")
			rawTokens := getInt(m, "raw_tokens", 0)
			weightedTokens := getInt(m, "weighted_tokens", 0)
			cacheHit, _ := m["cache_hit_pct"].(float64)
			peakContext := getInt(m, "peak_context", 0)
			outcome := getString(m, "outcome")

			sb.WriteString(fmt.Sprintf("   - Repo: `%s` | Branch: `%s` | Tier: %s | Outcome: %s\n", repo, branch, tier, outcome))
			if rawTokens > 0 || weightedTokens > 0 || cacheHit > 0 || peakContext > 0 {
				sb.WriteString(fmt.Sprintf("   - Tokens: raw=%d, weighted=%d, cache_hit=%.1f%%, peak_ctx=%d\n", rawTokens, weightedTokens, cacheHit, peakContext))
			}
			if headlines, ok := m["headlines"].([]any); ok && len(headlines) > 0 {
				sb.WriteString("   - Headlines:\n")
				for _, h := range headlines {
					sb.WriteString(fmt.Sprintf("     * %v\n", h))
				}
			}
		}
		sb.WriteString(fmt.Sprintf("   - Has Markdown Content: %t (%d chars)\n\n", hasContent, contentChars))
	}

	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: strings.TrimSpace(sb.String())}},
	}, nil
}

func executeGetDeliverable(ctx context.Context, client *DaemonClient, args map[string]any) (*CallToolResult, error) {
	delID := getString(args, "deliverable_id")
	if delID == "" {
		delID = getString(args, "id")
	}
	taskID := getString(args, "task_id")
	kind := getString(args, "kind")
	if kind == "" {
		kind = getString(args, "type")
	}

	if delID == "" && taskID == "" {
		return formatError("either deliverable_id (or id) or task_id is required"), nil
	}

	maxChars := getInt(args, "max_chars", 20000)
	if maxChars <= 0 {
		maxChars = 20000
	} else if maxChars > 50000 {
		maxChars = 50000
	}

	charOffset := getInt(args, "char_offset", 0)
	if charOffset < 0 {
		charOffset = 0
	}

	v := url.Values{}
	if delID != "" {
		v.Set("id", delID)
	}
	if taskID != "" {
		v.Set("task_id", taskID)
	}
	if kind != "" {
		v.Set("kind", kind)
	}
	v.Set("max_chars", strconv.Itoa(maxChars))
	v.Set("char_offset", strconv.Itoa(charOffset))

	endpoint := "/v1/deliverables?" + v.Encode()

	var data map[string]any
	if err := client.doRequest(ctx, http.MethodGet, endpoint, nil, &data); err != nil {
		return formatError(fmt.Sprintf("Failed to get deliverable: %v", err)), nil
	}

	id := getString(data, "id")
	tID := getString(data, "task_id")
	title := getString(data, "title")
	dKind := getString(data, "kind")
	host := getString(data, "host")
	filePath := getString(data, "file_path")
	urlStr := getString(data, "url")
	provider := getString(data, "provider")
	contentMD := getString(data, "content_md")
	totalChars := getInt(data, "total_chars", 0)
	retChars := getInt(data, "returned_chars", 0)
	truncated, _ := data["truncated"].(bool)
	nextOffset := getInt(data, "next_char_offset", 0)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Deliverable: %s (`%s`)\n", title, id))
	sb.WriteString(fmt.Sprintf("**Task:** `%s` | **Kind:** %s | **Host:** %s", tID, dKind, host))
	if provider != "" {
		sb.WriteString(fmt.Sprintf(" | **Provider:** %s", provider))
	}
	sb.WriteString("\n")
	if filePath != "" {
		sb.WriteString(fmt.Sprintf("**File:** `%s`\n", filePath))
	}
	if urlStr != "" {
		sb.WriteString(fmt.Sprintf("**URL:** %s\n", urlStr))
	}

	sb.WriteString(fmt.Sprintf("\n*(Chars %d-%d of %d, max_chars=%d, truncated=%t)*\n\n",
		charOffset, charOffset+retChars, totalChars, maxChars, truncated))
	sb.WriteString(contentMD)
	sb.WriteString("\n")

	if truncated {
		sb.WriteString(fmt.Sprintf("\n[Content truncated. Showing chars %d-%d of %d. Call get_deliverable with char_offset=%d to fetch the next chunk.]\n",
			charOffset, charOffset+retChars, totalChars, nextOffset))
	}

	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: strings.TrimSpace(sb.String())}},
	}, nil
}
