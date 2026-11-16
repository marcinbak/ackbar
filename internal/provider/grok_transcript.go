package provider

import (
	"os"

	"ackbar/internal/daemon"
)

func (g *GrokProvider) ReadSessionMetadata(cwd, nativeID string) *daemon.SessionMeta {
	home, _ := os.UserHomeDir()
	return daemon.ReadGrokSessionMeta(home, cwd, nativeID)
}

func (g *GrokProvider) ResolveSessionTitle(cwd, nativeID string) string {
	home, _ := os.UserHomeDir()
	if meta := daemon.ReadGrokSessionMeta(home, cwd, nativeID); meta != nil && meta.Title != "" {
		return meta.Title
	}
	return ""
}

func (g *GrokProvider) ExtractTranscript(home, cwd, nativeID string) ([]daemon.TranscriptMessage, error) {
	return daemon.ExtractGrokTranscriptMessages(home, cwd, nativeID)
}
