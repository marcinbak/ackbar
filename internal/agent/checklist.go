package agent

type ChecklistStatus string

const (
	ChecklistPending    ChecklistStatus = "pending"
	ChecklistInProgress ChecklistStatus = "in_progress"
	ChecklistDone       ChecklistStatus = "done"
	ChecklistSkipped    ChecklistStatus = "skipped"
	ChecklistFailed     ChecklistStatus = "failed"
)

type ChecklistItem struct {
	ID         string          `json:"id,omitempty"`
	Title      string          `json:"title"`
	ActiveForm string          `json:"active_form,omitempty"`
	Status     ChecklistStatus `json:"status"`
}

func CloneChecklist(src []ChecklistItem) []ChecklistItem {
	if src == nil {
		return nil
	}
	dst := make([]ChecklistItem, len(src))
	copy(dst, src)
	return dst
}
