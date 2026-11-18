package agent

import (
	"fmt"
	"strings"
)

type HeadlessExecMode string

const (
	HeadlessModeNone       HeadlessExecMode = ""
	HeadlessModePersistent HeadlessExecMode = "persistent"
	HeadlessModeACP        HeadlessExecMode = "acp"
	HeadlessModeOneShot    HeadlessExecMode = "oneshot"
)

type NativeIDStrategy string

const (
	NativeIDEagerUUID NativeIDStrategy = "eager_uuid"
	NativeIDLazyAdopt NativeIDStrategy = "lazy_adopt"
)

type Capabilities struct {
	SupportsTmux             bool
	SupportsHeadless         bool
	HeadlessMode             HeadlessExecMode
	NativeIDStrategy         NativeIDStrategy
	SupportsNativeHooks      bool
	SupportsHookShim         bool
	SupportsMultiAccount     bool
	SupportsOAuth            bool
	SupportsTranscript       bool
	SupportsSubagents        bool
	SupportsWorkspaceDocs    bool
	SupportsModelSelection   bool
	SupportsEffortLevel      bool
	SupportsYoloMode         bool
	SupportsSystemPrompt     bool
	SupportsExtraDirs        bool
	LocksExtraDirsOnResume   bool `json:"locks_extra_dirs_on_resume"`
	SupportedPermissionModes []string
}

type ConfigValidationInput struct {
	EngineType     string
	Model          string
	Effort         string
	PermissionMode string
	YoloMode       bool
	SystemPrompt   string
	AccountID      string
	ExtraDirs      []string
}

var validEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true,
}

func (c Capabilities) ValidateConfig(in ConfigValidationInput) error {
	engine := strings.ToLower(strings.TrimSpace(in.EngineType))
	switch engine {
	case "":
		if !c.SupportsTmux && !c.SupportsHeadless {
			return fmt.Errorf("agent supports neither tmux nor headless execution")
		}
	case EngineTmux:
		if !c.SupportsTmux {
			return fmt.Errorf("tmux engine is not supported by this agent")
		}
	case EngineHeadless:
		if !c.SupportsHeadless {
			return fmt.Errorf("headless engine is not supported by this agent")
		}
		switch c.HeadlessMode {
		case HeadlessModePersistent, HeadlessModeACP, HeadlessModeOneShot:
		default:
			return fmt.Errorf("headless mode %q is not valid", c.HeadlessMode)
		}
	default:
		return fmt.Errorf("unsupported engine type %q", in.EngineType)
	}

	if m := strings.TrimSpace(in.Model); m != "" && !strings.EqualFold(m, "default") && !c.SupportsModelSelection {
		return fmt.Errorf("model selection is not supported by this agent")
	}

	if eff := strings.ToLower(strings.TrimSpace(in.Effort)); eff != "" && eff != "default" {
		if !c.SupportsEffortLevel {
			return fmt.Errorf("effort level is not supported by this agent")
		}
		if !validEfforts[eff] {
			return fmt.Errorf("invalid effort %q", in.Effort)
		}
	}

	if in.YoloMode && !c.SupportsYoloMode {
		return fmt.Errorf("yolo mode is not supported by this agent")
	}

	if pm := strings.TrimSpace(in.PermissionMode); pm != "" && !strings.EqualFold(pm, "default") {
		pmLower := strings.ToLower(pm)
		if pmLower == "bypasspermissions" && !c.SupportsYoloMode {
			return fmt.Errorf("permission_mode %q is not supported by this agent", in.PermissionMode)
		}
		if len(c.SupportedPermissionModes) == 0 {
			return fmt.Errorf("permission_mode %q is not supported by this agent", in.PermissionMode)
		}
		allowed := false
		for _, candidate := range c.SupportedPermissionModes {
			if strings.EqualFold(strings.TrimSpace(candidate), pm) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("permission_mode %q is not supported by this agent", in.PermissionMode)
		}
	}

	if strings.TrimSpace(in.SystemPrompt) != "" && !c.SupportsSystemPrompt {
		return fmt.Errorf("system prompt is not supported by this agent")
	}

	if len(in.ExtraDirs) > 0 && !c.SupportsExtraDirs {
		return fmt.Errorf("extra directories are not supported by this agent")
	}

	if acc := strings.TrimSpace(in.AccountID); acc != "" && !strings.EqualFold(acc, "default") && !c.SupportsMultiAccount {
		return fmt.Errorf("multi-account selection is not supported by this agent")
	}

	return nil
}
