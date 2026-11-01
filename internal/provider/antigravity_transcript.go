package provider

import (
	"os"
	"path/filepath"

	"ackbar/internal/daemon"
)

func (a *AntigravityProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	title := a.ResolveSessionTitle(cwd, nativeID)
	if title != "" {
		return &daemon.SessionMeta{
			Title: title,
		}
	}
	return nil
}

func (a *AntigravityProvider) ResolveSessionTitle(cwd, nativeID string) string {
	return daemon.ReadAntigravitySessionTitle(cwd, nativeID)
}

func (a *AntigravityProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	return daemon.ExtractAntigravityTranscriptMessages(home, nativeID)
}

func (a *AntigravityProvider) CleanSessionFiles(home, cwd, nativeID string) error {
	if nativeID == "" {
		return nil
	}
	_ = os.RemoveAll(filepath.Join(home, ".gemini", "antigravity", "brain", nativeID))
	_ = os.RemoveAll(filepath.Join(home, ".gemini", "antigravity-cli", "brain", nativeID))
	_ = os.RemoveAll(filepath.Join(home, ".antigravity", "brain", nativeID))
	return nil
}
