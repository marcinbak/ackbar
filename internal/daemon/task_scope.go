package daemon

import (
	"strings"
)

type TaskScopeInput struct {
	ProjectNodePath   string
	GroupName         string
	ProjectName       string
	SubprojectName    string
	HasExplicitSubprj bool
	RepoNodePaths     []string
	Cwd               string
	SessionID         string
	IsExplicitMove    bool
}

func (s *Server) matchNode(groupHint, query string) (string, string, bool) {
	q := strings.TrimSpace(query)
	for {
		orig := q
		q = strings.Trim(q, "/")
		q = strings.TrimSuffix(q, ".git")
		q = strings.Trim(q, "/")
		if q == orig {
			break
		}
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return "", "", false
	}
	qLower := strings.ToLower(q)
	groupHint = strings.TrimSpace(groupHint)

	var nodes []*TreeNode
	if s.db != nil {
		nodes, _ = s.db.ListNodes()
	}

	if groupHint != "" {
		target := groupHint + "/" + q
		for _, n := range nodes {
			if strings.EqualFold(n.Path, target) {
				parts := strings.Split(n.Path, "/")
				if len(parts) > 1 {
					return parts[0], strings.Join(parts[1:], "/"), true
				}
				return "Personal", parts[0], true
			}
		}
	}

	for _, n := range nodes {
		if strings.EqualFold(n.Path, q) {
			parts := strings.Split(n.Path, "/")
			if len(parts) > 1 {
				return parts[0], strings.Join(parts[1:], "/"), true
			}
			return "Personal", parts[0], true
		}
	}

	for _, n := range nodes {
		pathParts := strings.Split(n.Path, "/")
		if groupHint != "" && len(pathParts) > 0 && !strings.EqualFold(pathParts[0], groupHint) {
			continue
		}

		cleanGit := strings.ToLower(strings.Trim(strings.TrimSuffix(n.GitURL, ".git"), "/"))
		cleanProj := strings.ToLower(strings.Trim(n.ProjectDir, "/"))
		slug := ""
		if len(pathParts) > 0 {
			slug = strings.ToLower(pathParts[len(pathParts)-1])
		}

		if (cleanGit != "" && (cleanGit == qLower || strings.HasSuffix(cleanGit, "/"+qLower))) ||
			(cleanProj != "" && (cleanProj == qLower || strings.HasSuffix(cleanProj, "/"+qLower))) ||
			(slug != "" && slug == qLower) {
			if len(pathParts) > 1 {
				return pathParts[0], strings.Join(pathParts[1:], "/"), true
			}
			return "Personal", pathParts[0], true
		}
	}

	if s.db != nil {
		if hostPaths, err := s.db.ListNodeHostPaths(""); err == nil {
			for _, hp := range hostPaths {
				hpParts := strings.Split(hp.NodePath, "/")
				if groupHint != "" && len(hpParts) > 0 && !strings.EqualFold(hpParts[0], groupHint) {
					continue
				}
				cleanPDir := strings.ToLower(strings.Trim(hp.ProjectDir, "/"))
				cleanGitR := strings.ToLower(strings.Trim(strings.TrimSuffix(hp.GitRemote, ".git"), "/"))
				if (cleanPDir != "" && (cleanPDir == qLower || strings.HasSuffix(cleanPDir, "/"+qLower))) ||
					(cleanGitR != "" && (cleanGitR == qLower || strings.HasSuffix(cleanGitR, "/"+qLower))) {
					if len(hpParts) > 1 {
						return hpParts[0], strings.Join(hpParts[1:], "/"), true
					}
					return "Personal", hpParts[0], true
				}
			}
		}
	}

	return "", "", false
}

func (s *Server) resolveTaskNodeScope(in TaskScopeInput) (string, string, string) {
	pnp := strings.Trim(strings.TrimSpace(in.ProjectNodePath), "/")
	if pnp != "" {
		var g, p string
		if strings.Contains(pnp, "/") {
			parts := strings.Split(pnp, "/")
			candGroup := parts[0]
			candProject := strings.Join(parts[1:], "/")
			if mg, mp, ok := s.matchNode(candGroup, candProject); ok {
				g, p = mg, mp
			} else if mg, mp, ok := s.matchNode("", pnp); ok {
				g, p = mg, mp
			} else {
				g, p = candGroup, candProject
			}
		} else {
			if mg, mp, ok := s.matchNode(in.GroupName, pnp); ok {
				g, p = mg, mp
			} else if mg, mp, ok := s.matchNode("", pnp); ok {
				g, p = mg, mp
			} else {
				g = in.GroupName
				if g == "" {
					g = "Personal"
				}
				p = pnp
			}
		}

		sub := ""
		if in.HasExplicitSubprj && strings.TrimSpace(in.SubprojectName) != "" {
			subCand := strings.TrimSpace(in.SubprojectName)
			if mg, mp, ok := s.matchNode(g, p+"/"+subCand); ok {
				g, p, sub = mg, mp, ""
			} else {
				sub = subCand
			}
		}
		if g == "" {
			g = "Personal"
		}
		if p == "" {
			p = "General"
		}
		return g, p, sub
	}

	groupName := strings.TrimSpace(in.GroupName)
	projectName := strings.TrimSpace(in.ProjectName)
	subprojectName := strings.TrimSpace(in.SubprojectName)

	if groupName != "" && strings.HasPrefix(strings.ToLower(projectName), strings.ToLower(groupName)+"/") {
		projectName = strings.TrimSpace(projectName[len(groupName)+1:])
	}

	if groupName == "" && strings.Contains(projectName, "/") {
		parts := strings.Split(projectName, "/")
		isKnownGroup := strings.EqualFold(parts[0], "Modemobile") || strings.EqualFold(parts[0], "Personal")
		if !isKnownGroup && s.db != nil {
			if nodes, _ := s.db.ListNodes(); nodes != nil {
				for _, n := range nodes {
					nP := strings.Split(n.Path, "/")
					if len(nP) > 0 && strings.EqualFold(nP[0], parts[0]) {
						isKnownGroup = true
						break
					}
				}
			}
		}
		if isKnownGroup {
			groupName = parts[0]
			projectName = strings.Join(parts[1:], "/")
		}
	}

	if subprojectName != "" {
		combined := projectName + "/" + subprojectName
		if mg, mp, ok := s.matchNode(groupName, combined); ok {
			groupName = mg
			projectName = mp
			subprojectName = ""
		} else if groupName != "" {
			if mg, mp, ok := s.matchNode("", groupName+"/"+combined); ok {
				groupName = mg
				projectName = mp
				subprojectName = ""
			}
		}
	}

	if projectName != "" && !strings.EqualFold(projectName, "General") {
		if mg, mp, ok := s.matchNode(groupName, projectName); ok {
			groupName = mg
			projectName = mp
			if strings.Contains(mp, "/") {
				subprojectName = ""
			}
		}
	}

	if !in.IsExplicitMove && (projectName == "" || strings.EqualFold(projectName, "General")) {
		candidateRepo := ""
		if groupName != "" {
			prefix := strings.ToLower(groupName) + "/"
			for _, r := range in.RepoNodePaths {
				trimmed := strings.Trim(strings.TrimSpace(r), "/")
				if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
					candidateRepo = trimmed
					break
				}
			}
		}
		if candidateRepo == "" {
			for _, r := range in.RepoNodePaths {
				trimmed := strings.Trim(strings.TrimSpace(r), "/")
				if trimmed != "" {
					candidateRepo = trimmed
					break
				}
			}
		}

		if candidateRepo != "" {
			if strings.Contains(candidateRepo, "/") {
				parts := strings.Split(candidateRepo, "/")
				candGroup := parts[0]
				candProject := strings.Join(parts[1:], "/")
				if mg, mp, ok := s.matchNode(candGroup, candProject); ok {
					groupName = mg
					projectName = mp
				} else if mg, mp, ok := s.matchNode("", candidateRepo); ok {
					groupName = mg
					projectName = mp
				} else {
					groupName = candGroup
					projectName = candProject
				}
			} else {
				if mg, mp, ok := s.matchNode(groupName, candidateRepo); ok {
					groupName = mg
					projectName = mp
				} else {
					if groupName == "" {
						groupName = "Personal"
					}
					projectName = candidateRepo
				}
			}
			subprojectName = ""
		}
	}

	if !in.IsExplicitMove && (projectName == "" || strings.EqualFold(projectName, "General")) {
		if in.SessionID != "" && s.db != nil {
			if sess, err := s.db.GetSession(in.SessionID); err == nil && sess != nil {
				sessPath := strings.TrimSpace(sess.NodePath)
				if sessPath != "" && !strings.EqualFold(sessPath, "unassigned") {
					if mg, mp, ok := s.matchNode(groupName, sessPath); ok {
						if groupName == "" || strings.EqualFold(groupName, mg) {
							groupName = mg
							projectName = mp
							subprojectName = ""
						}
					} else {
						parts := strings.Split(strings.Trim(sessPath, "/"), "/")
						if len(parts) > 1 {
							if groupName == "" || strings.EqualFold(groupName, parts[0]) {
								groupName = parts[0]
								projectName = strings.Join(parts[1:], "/")
								subprojectName = ""
							}
						} else if len(parts) == 1 && (groupName == "" || strings.EqualFold(groupName, "Personal")) {
							if groupName == "" {
								groupName = "Personal"
							}
							projectName = parts[0]
							subprojectName = ""
						}
					}
				}
				if (projectName == "" || strings.EqualFold(projectName, "General")) && sess.Cwd != "" {
					if mg, mp, ok := s.matchNode(groupName, sess.Cwd); ok {
						if groupName == "" || strings.EqualFold(groupName, mg) {
							groupName = mg
							projectName = mp
							subprojectName = ""
						}
					}
				}
			}
		}
		if (projectName == "" || strings.EqualFold(projectName, "General")) && in.Cwd != "" {
			if mg, mp, ok := s.matchNode(groupName, in.Cwd); ok {
				if groupName == "" || strings.EqualFold(groupName, mg) {
					groupName = mg
					projectName = mp
					subprojectName = ""
				}
			}
		}
	}

	if groupName == "" {
		groupName = "Personal"
	}
	if projectName == "" {
		projectName = "General"
	}

	if in.IsExplicitMove {
		if in.HasExplicitSubprj {
			subprojectName = strings.TrimSpace(in.SubprojectName)
		} else {
			subprojectName = ""
		}
	}

	return groupName, projectName, subprojectName
}
